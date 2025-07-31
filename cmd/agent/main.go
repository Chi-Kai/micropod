package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/mdlayher/vsock"
	"google.golang.org/grpc"

	pb "micropod/pkg/agent/api" // Import generated protobuf code
)

// agentServer implements the Agent gRPC service.
type agentServer struct {
	pb.UnimplementedAgentServer
	mu         sync.Mutex
	containers map[string]*containerState
}

type containerState struct {
	ID     string
	Status string
	Pid    int
}

func newAgentServer() *agentServer {
	return &agentServer{
		containers: make(map[string]*containerState),
	}
}

// CreateContainer is the RPC handler for creating and starting a container.
func (s *agentServer) CreateContainer(ctx context.Context, req *pb.CreateContainerRequest) (*pb.CreateContainerResponse, error) {
	log.Printf("📦 Received CreateContainer request for ID: %s", req.ContainerId)
	log.Printf("   Process args: %v", req.ProcessArgs)
	log.Printf("   Rootfs path: %s", req.RootfsPath)

	// Validate request
	if req.ContainerId == "" {
		return &pb.CreateContainerResponse{
			ContainerId:  req.ContainerId,
			Status:       "FAILED",
			ErrorMessage: "container ID cannot be empty",
		}, nil
	}

	if len(req.ProcessArgs) == 0 {
		return &pb.CreateContainerResponse{
			ContainerId:  req.ContainerId,
			Status:       "FAILED",
			ErrorMessage: "process args cannot be empty",
		}, nil
	}

	// Check if container already exists
	s.mu.Lock()
	if _, exists := s.containers[req.ContainerId]; exists {
		s.mu.Unlock()
		return &pb.CreateContainerResponse{
			ContainerId:  req.ContainerId,
			Status:       "FAILED",
			ErrorMessage: "container with this ID already exists",
		}, nil
	}
	s.mu.Unlock()

	bundlePath := filepath.Join("/containers", req.ContainerId)
	log.Printf("📁 Creating bundle directory: %s", bundlePath)

	if err := os.MkdirAll(bundlePath, 0755); err != nil {
		log.Printf("❌ Failed to create bundle directory: %v", err)
		return &pb.CreateContainerResponse{
			ContainerId:  req.ContainerId,
			Status:       "FAILED",
			ErrorMessage: fmt.Sprintf("failed to create bundle directory: %v", err),
		}, nil
	}

	// 1. Create OCI runtime spec (config.json)
	log.Printf("📝 Creating OCI runtime spec...")
	if err := createOciSpec(req, bundlePath); err != nil {
		log.Printf("❌ Failed to create OCI spec: %v", err)
		return &pb.CreateContainerResponse{
			ContainerId:  req.ContainerId,
			Status:       "FAILED",
			ErrorMessage: fmt.Sprintf("failed to create OCI spec: %v", err),
		}, nil
	}

	// 2. Check if rootfs path exists and is accessible
	if _, err := os.Stat(req.RootfsPath); err != nil {
		log.Printf("❌ Rootfs path not accessible: %v", err)
		return &pb.CreateContainerResponse{
			ContainerId:  req.ContainerId,
			Status:       "FAILED",
			ErrorMessage: fmt.Sprintf("rootfs path not accessible: %v", err),
		}, nil
	}

	// 3. Use runc to run the container in a detached state
	log.Printf("🏃 Starting container with runc...")
	cmd := exec.Command("runc", "run", "--detach", req.ContainerId)
	cmd.Dir = "/containers"

	// Capture output for better error reporting
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("❌ runc run failed: %v", err)
		log.Printf("runc output: %s", string(output))

		// Clean up failed container bundle
		os.RemoveAll(bundlePath)

		return &pb.CreateContainerResponse{
			ContainerId:  req.ContainerId,
			Status:       "FAILED",
			ErrorMessage: fmt.Sprintf("runc run failed: %v\nOutput: %s", err, string(output)),
		}, nil
	}

	log.Printf("✅ Container %s started successfully", req.ContainerId)
	log.Printf("runc output: %s", string(output))

	// Get actual PID from runc state
	pid, err := s.getContainerPID(req.ContainerId)
	if err != nil {
		log.Printf("⚠️  Could not get container PID: %v", err)
		pid = 0 // Set to 0 if we can't get the real PID
	}

	// Store container state
	s.mu.Lock()
	s.containers[req.ContainerId] = &containerState{
		ID:     req.ContainerId,
		Status: "RUNNING",
		Pid:    pid,
	}
	s.mu.Unlock()

	return &pb.CreateContainerResponse{
		ContainerId: req.ContainerId,
		Pid:         uint32(pid),
		Status:      "RUNNING",
	}, nil
}

func createOciSpec(req *pb.CreateContainerRequest, bundlePath string) error {
	spec := map[string]interface{}{
		"ociVersion": "1.0.2-dev",
		"process": map[string]interface{}{
			"terminal": false,
			"args":     req.ProcessArgs,
			"cwd":      "/",
		},
		"root": map[string]interface{}{
			"path":     req.RootfsPath, // Use the path shared by virtio-fs
			"readonly": false,
		},
		"linux": map[string]interface{}{
			"namespaces": []map[string]string{
				{"type": "pid"}, {"type": "ipc"}, {"type": "uts"}, {"type": "mount"}, {"type": "network"},
			},
		},
	}
	specBytes, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal OCI spec: %w", err)
	}
	return os.WriteFile(filepath.Join(bundlePath, "config.json"), specBytes, 0644)
}

// getContainerPID retrieves the PID of a running container using runc state
func (s *agentServer) getContainerPID(containerID string) (int, error) {
	cmd := exec.Command("runc", "state", containerID)
	cmd.Dir = "/containers"

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to get container state: %w", err)
	}

	// Parse JSON output to extract PID
	var state struct {
		Pid int `json:"pid"`
	}

	if err := json.Unmarshal(output, &state); err != nil {
		return 0, fmt.Errorf("failed to parse runc state output: %w", err)
	}

	return state.Pid, nil
}

func main() {
	// Listen on vsock port 1024
	l, err := vsock.Listen(1024, nil)
	if err != nil {
		log.Fatalf("failed to listen on vsock: %v", err)
	}
	defer l.Close()

	log.Println("Guest Agent gRPC server is ready and listening on vsock port 1024...")

	server := grpc.NewServer()
	pb.RegisterAgentServer(server, newAgentServer())

	if err := server.Serve(l); err != nil {
		log.Fatalf("failed to serve gRPC: %v", err)
	}
}
