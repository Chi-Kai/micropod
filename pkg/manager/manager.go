package manager

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"

	"micropod/pkg/config"
	"micropod/pkg/firecracker"
	"micropod/pkg/rootfs"
	"micropod/pkg/state"
)

type Manager struct {
	config 			  *config.Config
	store         *state.Store
	cowService    *rootfs.CowService
}

type VMConfig struct {
	VCPUs    int
	MemoryMB int
}

func NewManager() *Manager {
	cfg := config.NewConfig()
	if err := cfg.EnsureConfigDir(); err != nil {
		log.Fatal("Error ensuring config directory:", err)
	}

	store, err := state.NewStore(cfg.GetStateFilePath())
	if err != nil {
		log.Fatal("Error initializing store:", err)
	}

	// Initialize CoW service with image directory and device/cow directories
	deviceDir := filepath.Join(cfg.GetRootfsDir(), "devices")
	cowDir := filepath.Join(cfg.GetRootfsDir(), "cow")
	
	cowService, err := rootfs.NewCowService(cfg.GetImageDir(), deviceDir, cowDir)
	if err != nil {
		log.Fatal("Error initializing CoW service:", err)
	}

	return &Manager{
		config:     cfg,
		store:      store,
		cowService: cowService,
	}
}

func (m *Manager) RunVM(imageName string) (string, error) {
	fmt.Printf("Starting VM for image: %s\n", imageName)

	vmID := uuid.New().String()
	ctx := context.Background()

	// Create CoW rootfs device for this VM
	cowRootfs, err := m.cowService.CreateRootFS(ctx, imageName, vmID)
	if err != nil {
		return "", fmt.Errorf("failed to create CoW rootfs: %w", err)
	}

	kernelPath := m.config.GetKernelPath()
	socketPath := m.getSocketPath(vmID)

	client := firecracker.NewClient(socketPath)

	config := VMConfig{
		VCPUs:    1,
		MemoryMB: 512,
	}

	// 构建 Firecracker 启动配置
	fcConfig := firecracker.LaunchConfig{
		KernelPath: kernelPath,
		RootfsPath: cowRootfs.DevicePath,
		VCPUs:      int64(config.VCPUs),
		MemoryMB:   int64(config.MemoryMB),
		SocketPath: socketPath,
		BootArgs:   "console=ttyS0 reboot=k panic=1 pci=off",
	}

	// Launch VM using CoW device path
	if err := client.Launch(fcConfig); err != nil {
		m.cowService.RemoveRootFS(vmID)
		return "", fmt.Errorf("failed to launch VM: %w", err)
	}

	vm := state.VM{
		ID:             vmID,
		ImageName:      imageName,
		State:          "Running",
		FirecrackerPid: client.GetPID(),
		VMSocketPath:   socketPath,
		RootfsPath:     cowRootfs.DevicePath,
		KernelPath:     kernelPath,
		CreatedAt:      time.Now(),
	}

	if err := m.store.AddVM(vm); err != nil {
		client.Stop()
		m.cowService.RemoveRootFS(vmID)
		return "", fmt.Errorf("failed to store VM state: %w", err)
	}

	fmt.Printf("VM launched successfully with CoW optimization\n")
	fmt.Printf("  VM ID: %s\n", vmID)
	fmt.Printf("  Image: %s\n", imageName)
	fmt.Printf("  PID: %d\n", client.GetPID())
	fmt.Printf("  Socket: %s\n", socketPath)
	fmt.Printf("  CoW Device: %s\n", cowRootfs.DevicePath)

	return vmID, nil
}

func (m *Manager) ListVMs() ([]state.VM, error) {
	vms, err := m.store.ListVMs()
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	var runningVMs []state.VM
	for _, vm := range vms {
		if m.isProcessRunning(vm.FirecrackerPid) {
			runningVMs = append(runningVMs, vm)
		} else {
			m.cleanupDeadVM(vm)
		}
	}

	return runningVMs, nil
}

func (m *Manager) StopVM(vmID string) error {
	vm, err := m.store.GetVM(vmID)
	if err != nil {
		return fmt.Errorf("VM not found: %w", err)
	}

	fmt.Printf("Stopping VM: %s\n", vmID)

	// 尝试通过存储的 socket 路径重新连接并优雅停止 VM
	if _, err := os.Stat(vm.VMSocketPath); err == nil {
		fmt.Printf("Found socket %s, attempting graceful shutdown\n", vm.VMSocketPath)
		client := firecracker.NewClient(vm.VMSocketPath)
		if err := client.Stop(); err != nil {
			fmt.Printf("Warning: failed to stop VM gracefully: %v\n", err)
			// Fallback to process kill
			m.fallbackKillProcess(vm)
		} else {
			fmt.Printf("VM stopped gracefully using Firecracker SDK\n")
		}
	} else {
		fmt.Printf("Socket %s not found, using fallback process termination\n", vm.VMSocketPath)
		m.fallbackKillProcess(vm)
	}

	if err := m.cleanup(vm); err != nil {
		fmt.Printf("Warning: cleanup failed: %v\n", err)
	}

	if err := m.store.RemoveVM(vmID); err != nil {
		return fmt.Errorf("failed to remove VM from state: %w", err)
	}

	fmt.Printf("VM %s stopped and cleaned up\n", vmID)
	return nil
}

func (m *Manager) getSocketPath(vmID string) string {
	return filepath.Join("/tmp", fmt.Sprintf("firecracker-%s.sock", vmID[:8]))
}

func (m *Manager) isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	return err == nil
}

func (m *Manager) killProcess(pid int) error {
	if pid <= 0 {
		return nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	if err := process.Kill(); err != nil {
		return fmt.Errorf("failed to kill process: %w", err)
	}

	_, err = process.Wait()
	return err
}

func (m *Manager) fallbackKillProcess(vm *state.VM) {
	if m.isProcessRunning(vm.FirecrackerPid) {
		if err := m.killProcess(vm.FirecrackerPid); err != nil {
			fmt.Printf("Warning: failed to kill process %d: %v\n", vm.FirecrackerPid, err)
		}
	}
}

func (m *Manager) cleanup(vm *state.VM) error {
	var errors []error

	if err := os.Remove(vm.VMSocketPath); err != nil && !os.IsNotExist(err) {
		errors = append(errors, fmt.Errorf("failed to remove socket: %w", err))
	}

	if err := m.cowService.RemoveRootFS(vm.ID); err != nil {
		errors = append(errors, fmt.Errorf("failed to remove CoW rootfs: %w", err))
	}

	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %v", errors)
	}

	return nil
}

func (m *Manager) cleanupDeadVM(vm state.VM) {
	fmt.Printf("Cleaning up dead VM: %s\n", vm.ID)

	if err := m.cleanup(&vm); err != nil {
		fmt.Printf("Warning: failed to cleanup dead VM %s: %v\n", vm.ID, err)
	}

	if err := m.store.RemoveVM(vm.ID); err != nil {
		fmt.Printf("Warning: failed to remove dead VM %s from state: %v\n", vm.ID, err)
	}
}

// CleanupUnusedBaseImages removes base images that are no longer referenced
func (m *Manager) CleanupUnusedBaseImages() error {
	return m.cowService.CleanupUnusedBaseDevices()
}

// GetActiveRootFS returns information about active CoW root filesystems
func (m *Manager) GetActiveRootFS() []rootfs.CowRootFS {
	return m.cowService.ListActiveRootFS()
}