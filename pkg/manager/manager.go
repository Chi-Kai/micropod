package manager

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	pb "micropod/pkg/agent/api"
	"micropod/pkg/config"
	"micropod/pkg/firecracker"
	"micropod/pkg/image"
	"micropod/pkg/metrics"
	"micropod/pkg/network"
	"micropod/pkg/rootfs"
	"micropod/pkg/state"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

type Manager struct {
	config        *config.Config
	store         *state.Store
	imageService  image.ImageService
	rootfsCreator *rootfs.Creator
	metrics       *metrics.Metrics
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
		metrics:       metrics.NewMetrics(),
	}
}

func (m *Manager) RunVM(imageName string, portMappings []string) (string, error) {
	timer := metrics.NewTimer(fmt.Sprintf("RunVM(%s)", imageName))
	defer timer.Stop()

	vmID := uuid.New().String()
	ctx := context.Background()

	log.Printf("🚀 Starting VM for image: %s (ID: %s)", imageName, vmID)

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

	// 1. Unpack image rootfs on the HOST (will be shared via virtio-fs)
	unpackedPath := filepath.Join("/tmp", "micropod-rootfs-"+vmID)
	_, err = m.imageService.Unpack(ctx, imageName, unpackedPath)
	if err != nil {
		network.Teardown(netConfig)
		return "", fmt.Errorf("failed to unpack image: %w", err)
	}

	// 2. Setup VM configuration with virtio-fs and vsock
	kernelPath := m.config.GetKernelPath()
	agentRootfsPath := m.config.GetAgentRootfsPath()
	socketPath := m.getSocketPath(vmID)
	logFilePath := m.config.GetLogPath(vmID)
	vsockPath := m.getVsockPath(vmID)

	// Configure virtio-fs for sharing container rootfs
	virtioFS := &firecracker.VirtioFSConfig{
		SharedDir: unpackedPath,
		MountTag:  "container_rootfs",
	}

	// Configure vsock for agent communication
	vsock := &firecracker.VsockConfig{
		GuestCID: 3, // First VM gets CID 3
		UDSPath:  vsockPath,
	}

	// Construct kernel boot args with network configuration
	ipBootArg := fmt.Sprintf("ip=%s::%s:255.255.255.0::eth0:none", netConfig.GuestIP, netConfig.GatewayIP)

	client := firecracker.NewClient()

	config := VMConfig{
		VCPUs:    1,
		MemoryMB: 512,
	}

	// 3. Launch VM with agent rootfs + virtio-fs + vsock
	launchConfig := firecracker.LaunchConfig{
		KernelPath: kernelPath,
		RootfsPath: agentRootfsPath,
		VCPUs:      int64(config.VCPUs),
		MemoryMB:   int64(config.MemoryMB),
		BootArgs:   ipBootArg,
		VirtioFS:   virtioFS,
		Vsock:      vsock,
		Network:    netConfig,
		SocketPath: socketPath,
		LogPath:    logFilePath,
	}

	if err := client.LaunchVM(launchConfig); err != nil {
		network.Teardown(netConfig)
		os.RemoveAll(unpackedPath)
		return "", fmt.Errorf("failed to launch VM: %w", err)
	}

	// 4. Connect to Guest Agent via gRPC over vsock
	conn, err := m.connectToAgent(ctx, vsockPath)
	if err != nil {
		network.Teardown(netConfig)
		os.RemoveAll(unpackedPath)
		return "", fmt.Errorf("failed to connect to agent: %w", err)
	}
	defer conn.Close()

	agentClient := pb.NewAgentClient(conn)

	// 5. Send CreateContainer RPC to agent
	fmt.Println("Sending CreateContainer command to agent...")
	req := &pb.CreateContainerRequest{
		ContainerId: vmID,
		ProcessArgs: []string{"/bin/sh"}, // Default shell for now - TODO: extract from image config
		RootfsPath:  "/container_rootfs", // This is where virtio-fs mounts the shared dir
	}

	resp, err := agentClient.CreateContainer(ctx, req)
	if err != nil {
		network.Teardown(netConfig)
		os.RemoveAll(unpackedPath)
		return "", fmt.Errorf("CreateContainer RPC failed: %w", err)
	}

	if resp.Status != "RUNNING" {
		network.Teardown(netConfig)
		os.RemoveAll(unpackedPath)
		return "", fmt.Errorf("container failed to start in guest: %s", resp.ErrorMessage)
	}

	fmt.Printf("Agent response: Container %s is RUNNING.\n", resp.ContainerId)

	vm := state.VM{
		ID:             vmID,
		ImageName:      imageName,
		State:          "Running",
		FirecrackerPid: client.GetPID(),
		VMSocketPath:   socketPath,
		UnpackedPath:   unpackedPath, // Store path to virtio-fs shared directory
		VsockPath:      vsockPath,    // Store vsock socket path
		AgentConnected: true,         // Agent successfully responded
		KernelPath:     kernelPath,
		Network:        netConfig,
		LogFilePath:    logFilePath,
		CreatedAt:      time.Now(),
	}

	if err := m.store.AddVM(vm); err != nil {
		client.Stop()
		network.Teardown(netConfig)
		os.RemoveAll(unpackedPath)
		return "", fmt.Errorf("failed to store VM state: %w", err)
	}

	fmt.Printf("VM launched successfully\n")
	fmt.Printf("  VM ID: %s\n", vmID)
	fmt.Printf("  Image: %s\n", imageName)
	fmt.Printf("  PID: %d\n", client.GetPID())
	fmt.Printf("  Socket: %s\n", socketPath)
	fmt.Printf("  Unpacked Path: %s\n", unpackedPath)
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

	// Clean up unpacked container rootfs directory
	if err := os.RemoveAll(vm.UnpackedPath); err != nil {
		errors = append(errors, fmt.Errorf("failed to remove unpacked path: %w", err))
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

// getVsockPath returns the vsock unix domain socket path for a VM
func (m *Manager) getVsockPath(vmID string) string {
	return filepath.Join("/tmp", "micropod-vsock-"+vmID)
}

// connectToAgent establishes a gRPC connection to the guest agent via vsock
func (m *Manager) connectToAgent(ctx context.Context, vsockPath string) (*grpc.ClientConn, error) {

	log.Printf("Attempting to connect to guest agent via vsock...")

	const maxRetries = 30
	const retryInterval = 1 * time.Second
	const connectionTimeout = 5 * time.Second

	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("Connection attempt %d/%d...", attempt, maxRetries)

		// Create connection context with timeout
		connCtx, cancel := context.WithTimeout(ctx, connectionTimeout)

		// Attempt to establish gRPC connection using the new API
		conn, err := grpc.NewClient(vsockPath,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
				// Custom dialer for vsock connection
				dialer := &net.Dialer{
					Timeout: connectionTimeout,
				}
				return dialer.DialContext(ctx, "unix", addr)
			}),
		)

		if err != nil {
			cancel()
			lastErr = err
		} else {
			// Test the connection by waiting for it to be ready
			if !conn.WaitForStateChange(connCtx, connectivity.Idle) {
				cancel()
				conn.Close()
				lastErr = fmt.Errorf("connection timeout")
			} else {
				cancel()
				// Connection successful, test it
				if err := m.testConnection(conn); err != nil {
					conn.Close()
					lastErr = fmt.Errorf("connection test failed: %w", err)
					log.Printf("Connection test failed (attempt %d): %v", attempt, lastErr)
				} else {
					log.Printf("Successfully connected to guest agent on attempt %d", attempt)
					return conn, nil
				}
			}
		}

		if attempt < maxRetries {
			log.Printf("Connection failed (attempt %d): %v. Retrying in %v...", attempt, lastErr, retryInterval)

			// Check if parent context is cancelled
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("connection cancelled: %w", ctx.Err())
			case <-time.After(retryInterval):
				// Continue to next attempt
			}
		}
	}

	return nil, fmt.Errorf("failed to connect to agent after %d attempts, last error: %w", maxRetries, lastErr)
}

// testConnection performs a basic health check on the connection
func (m *Manager) testConnection(conn *grpc.ClientConn) error {
	// Create a context with short timeout for health check
	_, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Test the connection state
	state := conn.GetState()
	if state != connectivity.Ready {
		return fmt.Errorf("connection not ready, state: %v", state)
	}

	// Optionally, you can make a simple RPC call here to verify the agent is responding
	// For example, if you have a health check service:
	// client := pb.NewHealthServiceClient(conn)
	// _, err := client.Check(ctx, &pb.HealthCheckRequest{})
	// return err

	return nil
}
