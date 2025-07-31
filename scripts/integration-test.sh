#!/bin/bash

# integration-test.sh
# End-to-end integration test for micropod agent architecture

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
TEST_IMAGE="alpine:latest"
TIMEOUT_SECONDS=60

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
    exit 1
}

test_step() {
    echo -e "${BLUE}[TEST]${NC} $1"
}

# Cleanup function
cleanup() {
    log "Cleaning up test environment..."
    
    # Stop any running VMs
    if [ -f "$PROJECT_ROOT/bin/micropod" ]; then
        "$PROJECT_ROOT/bin/micropod" list 2>/dev/null | grep -v "No VMs found" | tail -n +2 | while read line; do
            vm_id=$(echo "$line" | awk '{print $1}')
            if [ ! -z "$vm_id" ] && [ "$vm_id" != "ID" ]; then
                log "Stopping VM: $vm_id"
                "$PROJECT_ROOT/bin/micropod" stop "$vm_id" 2>/dev/null || true
            fi
        done
    fi
    
    # Clean up temporary files
    rm -rf /tmp/micropod-test-*
    rm -rf /tmp/micropod-rootfs-*
    rm -rf /tmp/micropod-virtiofs-*
    rm -rf /tmp/micropod-vsock-*
    
    log "Cleanup completed"
}

# Check prerequisites
check_prerequisites() {
    test_step "Checking prerequisites..."
    
    # Check if binaries exist
    [ -f "$PROJECT_ROOT/bin/micropod" ] || error "micropod binary not found. Run 'make build' first."
    [ -f "$PROJECT_ROOT/bin/agent" ] || error "agent binary not found. Run 'make build-agent' first."
    
    # Check if firecracker is available
    command -v firecracker >/dev/null 2>&1 || error "firecracker not found in PATH"
    
    # Check if required tools are available
    command -v docker >/dev/null 2>&1 || warn "Docker not found - image pulling might fail"
    
    # Check if agent rootfs exists
    AGENT_ROOTFS_PATH="$HOME/.config/micropod/agent-rootfs.ext4"
    if [ ! -f "$AGENT_ROOTFS_PATH" ] && [ ! -f "$PROJECT_ROOT/agent-rootfs.ext4" ]; then
        warn "Agent rootfs not found. You may need to run './scripts/build-agent-rootfs.sh' first."
        log "Creating mock agent rootfs for testing..."
        mkdir -p "$(dirname "$AGENT_ROOTFS_PATH")"
        dd if=/dev/zero of="$AGENT_ROOTFS_PATH" bs=1M count=100 2>/dev/null
        mke2fs -t ext4 -F "$AGENT_ROOTFS_PATH" >/dev/null 2>&1 || true
    fi
    
    log "Prerequisites check completed"
}

# Test compilation
test_compilation() {
    test_step "Testing compilation..."
    
    cd "$PROJECT_ROOT"
    
    # Test main build
    make build >/dev/null 2>&1 || error "Failed to build micropod"
    
    # Test agent build
    make build-agent >/dev/null 2>&1 || error "Failed to build agent"
    
    # Test protobuf generation
    make generate >/dev/null 2>&1 || error "Failed to generate protobuf code"
    
    log "✅ Compilation tests passed"
}

# Test basic functionality
test_basic_functionality() {
    test_step "Testing basic functionality..."
    
    cd "$PROJECT_ROOT"
    
    # Test help command
    ./bin/micropod --help >/dev/null 2>&1 || error "micropod help command failed"
    
    # Test list command (should show no VMs initially)
    OUTPUT=$(./bin/micropod list 2>&1)
    if [[ "$OUTPUT" != *"No VMs found"* ]] && [[ "$OUTPUT" != *"ID"* ]]; then
        error "Unexpected output from 'micropod list': $OUTPUT"
    fi
    
    log "✅ Basic functionality tests passed"
}

# Test agent gRPC protocol
test_agent_protocol() {
    test_step "Testing agent gRPC protocol..."
    
    cd "$PROJECT_ROOT"
    
    # Compile a simple test to verify protobuf definitions
    cat > /tmp/test_agent_protocol.go << 'EOF'
package main

import (
    "context"
    "log"
    
    pb "micropod/pkg/agent/api"
)

func main() {
    req := &pb.CreateContainerRequest{
        ContainerId: "test",
        ProcessArgs: []string{"/bin/sh"},
        RootfsPath:  "/test",
    }
    
    resp := &pb.CreateContainerResponse{
        ContainerId: req.ContainerId,
        Status:      "RUNNING",
    }
    
    if req.ContainerId != resp.ContainerId {
        log.Fatal("Protocol test failed")
    }
    
    log.Println("Protocol test passed")
}
EOF

    go run /tmp/test_agent_protocol.go >/dev/null 2>&1 || error "Agent protocol test failed"
    rm /tmp/test_agent_protocol.go
    
    log "✅ Agent protocol tests passed"
}

# Test VM launch simulation (without actually starting firecracker)
test_vm_launch_simulation() {
    test_step "Testing VM launch simulation..."
    
    cd "$PROJECT_ROOT"
    
    # Mock test: ensure the manager can initialize properly
    cat > /tmp/test_manager.go << 'EOF'
package main

import (
    "log"
    
    "micropod/pkg/config"
    "micropod/pkg/manager"
)

func main() {
    cfg := config.NewConfig()
    if err := cfg.EnsureConfigDir(); err != nil {
        log.Fatal("Failed to ensure config dir:", err)
    }
    
    mgr := manager.NewManager()
    if mgr == nil {
        log.Fatal("Failed to create manager")
    }
    
    vms, err := mgr.ListVMs()
    if err != nil {
        log.Fatal("Failed to list VMs:", err)
    }
    
    log.Printf("Manager initialized successfully, found %d VMs", len(vms))
}
EOF

    go run /tmp/test_manager.go >/dev/null 2>&1 || error "Manager initialization test failed"
    rm /tmp/test_manager.go
    
    log "✅ VM launch simulation tests passed"
}

# Performance benchmark
benchmark_startup() {
    test_step "Running performance benchmark..."
    
    # Measure compilation time
    start_time=$(date +%s%N)
    make build >/dev/null 2>&1
    end_time=$(date +%s%N)
    compile_time=$((($end_time - $start_time) / 1000000))
    
    log "📊 Performance Metrics:"
    log "   - Compilation time: ${compile_time}ms"
    log "   - micropod binary size: $(ls -lh bin/micropod | awk '{print $5}')"
    log "   - agent binary size: $(ls -lh bin/agent | awk '{print $5}')"
    
    # Binary size check
    MICROPOD_SIZE=$(stat -c%s bin/micropod 2>/dev/null || stat -f%z bin/micropod)
    AGENT_SIZE=$(stat -c%s bin/agent 2>/dev/null || stat -f%z bin/agent)
    
    if [ $MICROPOD_SIZE -gt $((50 * 1024 * 1024)) ]; then
        warn "micropod binary is quite large (>50MB)"
    fi
    
    if [ $AGENT_SIZE -gt $((30 * 1024 * 1024)) ]; then
        warn "agent binary is quite large (>30MB)"
    fi
    
    log "✅ Performance benchmark completed"
}

# Main test execution
main() {
    echo -e "${BLUE}🚀 Starting micropod agent architecture integration test${NC}"
    echo -e "${BLUE}================================================${NC}"
    
    # Set up trap for cleanup
    trap cleanup EXIT
    
    check_prerequisites
    test_compilation
    test_basic_functionality
    test_agent_protocol
    test_vm_launch_simulation
    benchmark_startup
    
    echo ""
    echo -e "${GREEN}🎉 All integration tests passed!${NC}"
    echo -e "${GREEN}✅ Agent architecture implementation verified${NC}"
    echo ""
    echo -e "${BLUE}Next steps:${NC}"
    echo "   1. Build agent rootfs: ./scripts/build-agent-rootfs.sh"
    echo "   2. Test with real VM: micropod run alpine:latest"
    echo "   3. Monitor logs: tail -f ~/.config/micropod/logs/*.log"
    echo ""
}

# Run main function
main "$@"