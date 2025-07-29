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
	"micropod/pkg/image"
	"micropod/pkg/network"
	"micropod/pkg/rootfs"
	"micropod/pkg/state"
)

type Manager struct {
	config 			  *config.Config
	store         *state.Store
	imageService  image.ImageService
	rootfsCreator *rootfs.Creator
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

	imageService, err := image.NewManager(cfg.GetImageDir())
	if err != nil {
		log.Fatal("Error initializing image service:", err)
	}

	rootfsCreator, err := rootfs.NewCreator(cfg.GetRootfsDir())
	if err != nil {
		log.Fatal("Error initializing rootfs creator:", err)
	}

	return &Manager{
		config:        cfg,
		store:         store,
		imageService:  imageService,
		rootfsCreator: rootfsCreator,
	}
}

func (m *Manager) RunVM(imageName string, portMappings []string) (string, error) {
	fmt.Printf("Starting VM for image: %s\n", imageName)

	vmID := uuid.New().String()
	ctx := context.Background()

	// Setup network
	netConfig, err := network.Setup(vmID, portMappings)
	if err != nil {
		return "", fmt.Errorf("failed to setup network: %w", err)
	}

	// Pull the image if not exists locally
	_, err = m.imageService.PullImage(ctx, imageName)
	if err != nil {
		network.Teardown(netConfig)
		return "", fmt.Errorf("failed to pull image: %w", err)
	}

	// Create temporary directory for unpacking
	tempDir := filepath.Join("/tmp", "micropod-unpack-"+vmID)
	defer os.RemoveAll(tempDir)

	// Unpack the image to the temporary directory
	_, err = m.imageService.Unpack(ctx, imageName, tempDir)
	if err != nil {
		network.Teardown(netConfig)
		return "", fmt.Errorf("failed to unpack image: %w", err)
	}

	// Create ext4 rootfs from the unpacked directory
	rootfsPath, err := m.rootfsCreator.CreateFromDir(tempDir, vmID)
	if err != nil {
		network.Teardown(netConfig)
		return "", fmt.Errorf("failed to create rootfs: %w", err)
	}

	kernelPath := m.config.GetKernelPath()
	socketPath := m.getSocketPath(vmID)

	// Construct kernel boot args with network configuration
	ipBootArg := fmt.Sprintf("ip=%s::%s:255.255.255.0::eth0:none", netConfig.GuestIP, netConfig.GatewayIP)

	client := firecracker.NewClient(socketPath)

	config := VMConfig{
		VCPUs:    1,
		MemoryMB: 512,
	}

	if err := client.LaunchVM(kernelPath, rootfsPath, config.VCPUs, config.MemoryMB, ipBootArg, netConfig); err != nil {
		network.Teardown(netConfig)
		m.rootfsCreator.RemoveRootfs(rootfsPath)
		return "", fmt.Errorf("failed to launch VM: %w", err)
	}

	vm := state.VM{
		ID:             vmID,
		ImageName:      imageName,
		State:          "Running",
		FirecrackerPid: client.GetPID(),
		VMSocketPath:   socketPath,
		RootfsPath:     rootfsPath,
		KernelPath:     kernelPath,
		Network:        netConfig,
		CreatedAt:      time.Now(),
	}

	if err := m.store.AddVM(vm); err != nil {
		client.Stop()
		network.Teardown(netConfig)
		m.rootfsCreator.RemoveRootfs(rootfsPath)
		return "", fmt.Errorf("failed to store VM state: %w", err)
	}

	fmt.Printf("VM launched successfully\n")
	fmt.Printf("  VM ID: %s\n", vmID)
	fmt.Printf("  Image: %s\n", imageName)
	fmt.Printf("  PID: %d\n", client.GetPID())
	fmt.Printf("  Socket: %s\n", socketPath)
	fmt.Printf("  Rootfs: %s\n", rootfsPath)
	fmt.Printf("  Network: %s -> %s\n", netConfig.GuestIP, netConfig.TapDevice)

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

	if m.isProcessRunning(vm.FirecrackerPid) {
		if err := m.killProcess(vm.FirecrackerPid); err != nil {
			fmt.Printf("Warning: failed to kill process %d: %v\n", vm.FirecrackerPid, err)
		}
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

func (m *Manager) cleanup(vm *state.VM) error {
	var errors []error

	// Clean up network resources
	if vm.Network != nil {
		if err := network.Teardown(vm.Network); err != nil {
			errors = append(errors, fmt.Errorf("failed to teardown network: %w", err))
		}
	}

	if err := os.Remove(vm.VMSocketPath); err != nil && !os.IsNotExist(err) {
		errors = append(errors, fmt.Errorf("failed to remove socket: %w", err))
	}

	if err := m.rootfsCreator.RemoveRootfs(vm.RootfsPath); err != nil {
		errors = append(errors, fmt.Errorf("failed to remove rootfs: %w", err))
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