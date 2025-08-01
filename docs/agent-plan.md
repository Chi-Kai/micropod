# Agent-based Architecture: Detailed Implementation Plan

**Core Principle:** Evolve `micropod` from a VM runner into a true container runtime by separating VM management from container lifecycle management. This is achieved by introducing a lightweight Guest Agent inside the VM, controlled by `micropod` on the host.

**Architecture:**

*   **Host (`micropod`):** Manages the VM lifecycle, networking, and filesystem sharing. It acts as a gRPC client, sending commands to the Guest Agent.
*   **Guest (VM):** Runs a generic rootfs containing a minimal Linux, `runc`, and our custom **Guest Agent**.
*   **Guest Agent:** A gRPC server running as PID 1. It listens on a Vsock connection and translates RPC calls into `runc` commands to manage containers.
*   **Communication:** gRPC over Vsock for robust, high-performance, and extensible communication.
*   **Filesystem:** `virtio-fs` for sharing the container's root filesystem directly from the host to the guest, avoiding slow and cumbersome disk image creation.

---

### Phase 1: API Definition with gRPC and Protobuf

First, we define the contract between the host and the agent. This makes the system predictable and extensible.

**Create `pkg/agent/api/agent.proto`:**

```protobuf
syntax = "proto3";

package api;

option go_package = "micropod/pkg/agent/api";

// The Agent service defines the RPCs the guest agent will expose.
service Agent {
  // Creates and starts a new container inside the VM.
  rpc CreateContainer(CreateContainerRequest) returns (CreateContainerResponse);
  
  // Future RPCs for enhanced functionality
  // rpc StopContainer(StopContainerRequest) returns (StopContainerResponse);
  // rpc ExecInContainer(ExecInContainerRequest) returns (stream ExecInContainerResponse);
  // rpc GetContainerLogs(GetContainerLogsRequest) returns (stream LogEntry);
}

message CreateContainerRequest {
  string container_id = 1;
  // OCI bundle spec, including entrypoint, cmd, env vars, etc.
  // For simplicity, we'll start with just the basics.
  repeated string process_args = 2;
  // The path inside the VM where virtio-fs shares the rootfs.
  string rootfs_path = 3; 
}

message CreateContainerResponse {
  string container_id = 1;
  uint32 pid = 2;
  string status = 3; // e.g., "RUNNING", "FAILED"
  string error_message = 4;
}
```
*(We will need to generate Go code from this proto file using `protoc`)*

---

### Phase 2: Build the Generic Rootfs

This step remains conceptually the same, but its contents are now more specific.

**Generic `rootfs.ext4` Contents:**

1.  **Base OS:** Alpine Linux.
2.  **OCI Runtime:** `runc` binary.
3.  **Guest Agent:** The compiled Go binary from our gRPC server implementation (see Phase 3). It will be configured as the `init` process (PID 1).
4.  **Directory Structure:** Pre-create a directory like `/containers` where `runc` bundles will be stored.

---

### Phase 3: Implement the Guest Agent (gRPC Server)

The agent is now a gRPC server that implements the `Agent` service defined in our `.proto` file.

**`agent/main.go` (Guest VM):**

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"google.golang.org/grpc"
	"github.com/mdlayher/vsock"

	pb "micropod/pkg/agent/api" // Imported from generated protobuf code
)

// agentServer implements the Agent gRPC service.
type agentServer struct {
	pb.UnimplementedAgentServer
	mu           sync.Mutex
	containers   map[string]*containerState
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
	log.Printf("Received CreateContainer request for ID: %s", req.ContainerId)

	bundlePath := filepath.Join("/containers", req.ContainerId)
	if err := os.MkdirAll(bundlePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create bundle directory: %w", err)
	}

	// 1. Create OCI runtime spec (config.json)
	if err := createOciSpec(req, bundlePath); err != nil {
		return nil, fmt.Errorf("failed to create OCI spec: %w", err)
	}

	// 2. Use runc to run the container in a detached state
	// We use 'runc run' instead of 'create/start' for simplicity here,
	// but with the --detach flag, it becomes non-blocking.
	cmd := exec.Command("runc", "run", "--detach", req.ContainerId)
	cmd.Dir = "/containers"
	cmd.Stdout = os.Stdout // Redirect agent's stdout for now
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		log.Printf("runc run failed: %v", err)
		return &pb.CreateContainerResponse{
			ContainerId: req.ContainerId,
			Status:      "FAILED",
			ErrorMessage: err.Error(),
		}, nil
	}

	log.Printf("Container %s started successfully", req.ContainerId)
	
	// A more robust implementation would get the PID from `runc state`.
	s.mu.Lock()
	s.containers[req.ContainerId] = &containerState{ID: req.ContainerId, Status: "RUNNING"}
	s.mu.Unlock()

	return &pb.CreateContainerResponse{
		ContainerId: req.ContainerId,
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
```

---

### Phase 4: Adapt `micropod` (Host gRPC Client)

`micropod` now becomes a gRPC client that drives the agent. It's responsible for preparing the `virtio-fs` share before launching the VM.

**`pkg/manager/manager.go` (Host):**

```go
// ... imports ...
import (
    "google.golang.org/grpc"
    "github.com/mdlayher/vsock"
    "github.com/google/uuid"
    "micropod/pkg/agent/api" // Import generated protobuf code
)

// RunVM function needs a complete overhaul
func (m *Manager) RunVM(imageName string, portMappings []string) (string, error) {
	fmt.Printf("Preparing to run image: %s
", imageName)
	vmID := uuid.New().String()
	ctx := context.Background()

	// 1. Unpack image rootfs on the HOST
	// This directory will be shared with the guest via virtio-fs.
	unpackedPath := filepath.Join("/tmp/micropod-rootfs", vmID)
	imageInfo, err := m.imageService.Unpack(imageName, unpackedPath)
	if err != nil {
		return "", fmt.Errorf("failed to unpack image: %w", err)
	}

	// 2. Configure and Launch the VM with Vsock and Virtio-fs
	// The generic rootfs (with the agent) is the main drive.
	// The unpacked image rootfs is the virtio-fs share.
	fcClient, err := m.firecracker.LaunchVM(ctx, &firecracker.VMConfig{
		VMID:         vmID,
		KernelPath:   m.config.GetKernelPath(),
		RootfsPath:   m.config.GetAgentRootfsPath(), // Path to generic agent rootfs
		VsockEnabled: true,
		Virtiofs: &firecracker.VirtiofsConfig{
			ShareDir:  unpackedPath,
			MountPoint: "/container_rootfs", // Where it will appear inside the VM
		},
		// ... other configs like network ...
	})
	if err != nil {
		return "", fmt.Errorf("failed to launch VM: %w", err)
	}

	// 3. Connect to the Guest Agent via gRPC over Vsock
	conn, err := grpc.DialContext(ctx, "vsock:3:1024", grpc.WithInsecure(),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			// The vsock CID is 3 for the first VM in Firecracker
			return vsock.Dial(3, 1024, nil)
		}),
	)
	if err != nil {
		return "", fmt.Errorf("failed to dial guest agent via gRPC: %w", err)
	}
	defer conn.Close()

	agentClient := pb.NewAgentClient(conn)

	// 4. Send CreateContainer RPC
	fmt.Println("Sending CreateContainer command to agent...")
	req := &pb.CreateContainerRequest{
		ContainerId: vmID,
		ProcessArgs: append(imageInfo.Entrypoint, imageInfo.Cmd...),
		RootfsPath:  "/container_rootfs", // This must match the MountPoint in VirtiofsConfig
	}
	
	resp, err := agentClient.CreateContainer(ctx, req)
	if err != nil {
		return "", fmt.Errorf("CreateContainer RPC failed: %w", err)
	}

	if resp.Status != "RUNNING" {
		return "", fmt.Errorf("container failed to start in guest: %s", resp.ErrorMessage)
	}

	fmt.Printf("Agent response: Container %s is RUNNING.
", resp.ContainerId)

	// ... (Save VM state, etc.) ...
	return vmID, nil
}
```

---

### Summary and Advantages (Revised)

This refined architecture provides a solid, modern foundation for `micropod`.

*   **Clear Separation:** `micropod` manages infrastructure (VMs), Agent manages containers.
*   **Standardization:** Uses OCI `runc` for container execution and gRPC for communication, both industry standards.
*   **High Performance:** `virtio-fs` and `vsock` provide near-native speed for filesystem access and control commands.
*   **Extreme Extensibility:** Adding new features like `stop`, `exec`, or `logs` is as simple as defining a new RPC in the `.proto` file and implementing the handler. The communication channel is already built for it.
*   **Robustness:** gRPC provides type safety, protocol validation, and a clear error model, reducing bugs and simplifying development compared to raw JSON.

This plan is a direct path to transforming `micropod` into a powerful, lightweight, and professional-grade container runtime.

---

## 🚀 Implementation Roadmap

### Timeline Overview: **7-10 Days**

```
Week 1: Foundation    Week 2: Integration    Week 3: Polish
├─ Phase 1-2 (3 days)  ├─ Phase 3-4 (3 days)   ├─ Phase 5-6 (2-4 days)
├─ Proto + Agent       ├─ VM Integration       ├─ Testing + Optimization
└─ Core gRPC Logic     └─ End-to-End Flow      └─ Documentation
```

---

### Phase 1: gRPC Protocol Foundation (Day 1)

**Goal:** 建立Host-Guest通信契约

**Tasks:**
1. **Create protobuf definitions** `pkg/agent/api/agent.proto`
   ```bash
   mkdir -p pkg/agent/api
   # Copy the proto definition from plan above
   ```

2. **Setup code generation**
   ```makefile
   # Add to Makefile
   .PHONY: generate
   generate:
   	protoc --go_out=. --go-grpc_out=. pkg/agent/api/agent.proto
   
   # Install dependencies
   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
   ```

3. **Generate Go code**
   ```bash
   make generate
   # Should create pkg/agent/api/agent.pb.go and agent_grpc.pb.go
   ```

4. **Update go.mod dependencies**
   ```bash
   go get google.golang.org/grpc
   go get github.com/mdlayher/vsock
   ```

---

### Phase 2: Guest Agent Implementation (Day 2)

**Goal:** 实现VM内的Agent服务器

**Tasks:**
1. **Create agent main** `cmd/agent/main.go`
   ```go
   // Implement the agentServer struct and CreateContainer RPC
   // Use the exact code from the plan above
   ```

2. **OCI spec generation logic**
   ```go
   // Implement createOciSpec function
   // Generate minimal but functional config.json for runc
   ```

3. **Build agent binary**
   ```bash
   # Add to Makefile
   build-agent:
   	CGO_ENABLED=0 GOOS=linux go build -o bin/agent cmd/agent/main.go
   ```

4. **Test agent compilation**
   ```bash
   make build-agent
   # Should create bin/agent binary
   ```

---

### Phase 3: Agent Rootfs Creation (Day 3)

**Goal:** 构建包含Agent的通用VM rootfs

**Tasks:**
1. **Create rootfs build script** `scripts/build-agent-rootfs.sh`
   ```bash
   #!/bin/bash
   # Download Alpine minirootfs
   # Install runc in the rootfs
   # Copy agent binary as init process
   # Create /containers directory
   # Package as rootfs.ext4
   ```

2. **Create Dockerfile approach** (Alternative)
   ```dockerfile
   # Build a minimal Alpine image with agent
   FROM alpine:latest
   RUN apk add --no-cache runc
   COPY bin/agent /sbin/init
   RUN mkdir -p /containers
   ```

3. **Integrate with existing config**
   ```go
   // Add to pkg/config/config.go
   func (c *Config) GetAgentRootfsPath() string {
       return filepath.Join(c.GetConfigDir(), "agent-rootfs.ext4")
   }
   ```

---

### Phase 4: Firecracker virtio-fs Support (Day 4)

**Goal:** 扩展Firecracker client支持vsock和virtio-fs

**Tasks:**
1. **Add virtio-fs structures** to `pkg/firecracker/client.go`
   ```go
   type VirtioFSConfig struct {
       SharedDir    string `json:"shared_dir"`
       MountTag     string `json:"mount_tag"`
   }
   
   type VsockConfig struct {
       GuestCID uint32 `json:"guest_cid"`
       UDSPath  string `json:"uds_path"`
   }
   ```

2. **Extend LaunchVM method**
   ```go
   func (c *Client) LaunchVMWithAgent(
       kernelPath, agentRootfsPath string,
       virtioFS *VirtioFSConfig,
       vsock *VsockConfig,
       // ... other params
   ) error
   ```

3. **Implement Firecracker API calls**
   ```go
   // PUT /drives for agent rootfs
   // PUT /vsock for guest communication
   // PUT /fs for virtio-fs mount
   ```

---

### Phase 5: Manager gRPC Integration (Day 5-6)

**Goal:** 重构Manager成为gRPC客户端

**Tasks:**
1. **Refactor RunVM method** in `pkg/manager/manager.go`
   ```go
   func (m *Manager) RunVM(imageName string, portMappings []string) (string, error) {
       // 1. Unpack image to host directory (keep existing)
       // 2. Launch VM with agent rootfs + virtio-fs share
       // 3. Connect via gRPC over vsock  
       // 4. Send CreateContainer RPC
       // 5. Handle response and store state
   }
   ```

2. **Remove rootfs creation dependency**
   ```go
   // Remove rootfsCreator from Manager struct
   // Remove ext4 creation logic
   // Simplify cleanup logic
   ```

3. **Add gRPC client logic**
   ```go
   func (m *Manager) connectToAgent(vmID string) (pb.AgentClient, error) {
       // Implement vsock dialer
       // Create gRPC connection
       // Return agent client
   }
   ```

4. **Update VM state structure** in `pkg/state/store.go`
   ```go
   type VM struct {
       // Remove RootfsPath (no longer needed)
       // Add UnpackedPath (virtio-fs share directory)
       // Add AgentConnected bool
   }
   ```

---

### Phase 6: Testing & Integration (Day 7-8)

**Goal:** 端到端验证和错误处理

**Tasks:**
1. **Integration test script**
   ```bash
   #!/bin/bash
   # Test full flow: micropod run nginx
   # Verify container starts inside VM
   # Check network connectivity
   # Verify cleanup works
   ```

2. **Error handling improvements**
   ```go
   // Add connection timeouts
   // Implement retry logic for gRPC calls  
   // Add graceful agent failure handling
   // Improve error messages
   ```

3. **Performance measurement**
   ```bash
   # Compare startup times: old vs new approach
   # Memory usage comparison
   # Create benchmark script
   ```

---

### Phase 7: Documentation & Polish (Day 9-10)

**Goal:** 完善文档和用户体验

**Tasks:**
1. **Update README.md**
   - Add architecture diagram
   - Update installation instructions
   - Add agent rootfs build instructions

2. **Create deployment guide**
   - Prerequisites (kernel version, firecracker)
   - Agent rootfs setup
   - Troubleshooting guide

3. **Code cleanup**
   - Remove unused rootfs creation code
   - Add comprehensive logging
   - Improve error messages

---

### Success Metrics

**Performance Targets:**
- ✅ Container startup time: < 2 seconds (vs current ~5-8 seconds)  
- ✅ Memory overhead: < 50MB per container
- ✅ No sudo requirements for normal operation

**Functional Requirements:**
- ✅ `micropod run nginx` works end-to-end
- ✅ Network port mapping functional  
- ✅ Multiple containers per VM support
- ✅ Graceful error handling and cleanup

**Quality Assurance:**
- ✅ Integration tests pass
- ✅ No memory leaks in long-running tests
- ✅ Clear error messages for common failures

---

### Dependencies & Prerequisites

**External Dependencies:**
```bash
# Install protobuf compiler
sudo apt-get install protobuf-compiler  # Ubuntu/Debian
# or
brew install protobuf  # macOS

# Install Firecracker (if not already installed)
# Ensure kernel supports vsock and virtio-fs
```

**Go Dependencies:**
```go
// Add to go.mod
google.golang.org/grpc v1.60.0
google.golang.org/protobuf v1.31.0
github.com/mdlayher/vsock v1.2.1
```

**Configuration Changes:**
```yaml
# firecracker.yaml - add agent rootfs path
agent_rootfs: "/path/to/agent-rootfs.ext4"
enable_vsock: true
enable_virtio_fs: true
```

This implementation plan provides a clear, incremental path to achieve the agent-based architecture while maintaining backwards compatibility during development.
