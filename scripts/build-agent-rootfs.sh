#!/bin/bash

# build-agent-rootfs.sh
# Creates a minimal rootfs containing Alpine Linux + runc + micropod agent

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
ROOTFS_DIR="/tmp/micropod-agent-rootfs"
OUTPUT_FILE="$PROJECT_ROOT/agent-rootfs.ext4"
ROOTFS_SIZE="256M"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
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

# Check dependencies
check_dependencies() {
    log "Checking dependencies..."
    
    command -v wget >/dev/null 2>&1 || error "wget is required but not installed"
    command -v tar >/dev/null 2>&1 || error "tar is required but not installed"
    command -v mke2fs >/dev/null 2>&1 || error "mke2fs is required but not installed"
    
    if [[ "$OSTYPE" == "darwin"* ]]; then
        command -v gtar >/dev/null 2>&1 || error "gtar is required on macOS (brew install gnu-tar)"
        TAR_CMD="gtar"
    else
        TAR_CMD="tar"
    fi
    
    log "Dependencies check passed"
}

# Build agent binary
build_agent() {
    log "Building agent binary..."
    
    cd "$PROJECT_ROOT"
    
    if [ ! -f "bin/agent" ]; then
        log "Agent binary not found, building..."
        make build-agent || error "Failed to build agent"
    fi
    
    if [ ! -f "bin/agent" ]; then
        error "Agent binary still not found after build"
    fi
    
    log "Agent binary ready: $(ls -lh bin/agent | awk '{print $5}')"
}

# Download and extract Alpine minirootfs
setup_alpine() {
    log "Setting up Alpine Linux base..."
    
    ALPINE_VERSION="3.18"
    ALPINE_ARCH="x86_64"
    ALPINE_URL="https://dl-cdn.alpinelinux.org/alpine/v${ALPINE_VERSION}/releases/${ALPINE_ARCH}/alpine-minirootfs-${ALPINE_VERSION}.0-${ALPINE_ARCH}.tar.gz"
    ALPINE_TAR="/tmp/alpine-minirootfs.tar.gz"
    
    # Clean up previous attempts
    rm -rf "$ROOTFS_DIR"
    mkdir -p "$ROOTFS_DIR"
    
    # Download Alpine minirootfs
    if [ ! -f "$ALPINE_TAR" ]; then
        log "Downloading Alpine minirootfs..."
        wget -q "$ALPINE_URL" -O "$ALPINE_TAR" || error "Failed to download Alpine"
    fi
    
    # Extract Alpine
    log "Extracting Alpine minirootfs..."
    cd "$ROOTFS_DIR"
    $TAR_CMD -xzf "$ALPINE_TAR" || error "Failed to extract Alpine"
    
    log "Alpine Linux base extracted"
}

# Install runc in the rootfs
install_runc() {
    log "Installing runc..."
    
    # Use static runc binary for simplicity
    RUNC_VERSION="v1.1.12"
    RUNC_URL="https://github.com/opencontainers/runc/releases/download/${RUNC_VERSION}/runc.amd64"
    RUNC_BIN="/tmp/runc"
    
    if [ ! -f "$RUNC_BIN" ]; then
        log "Downloading runc ${RUNC_VERSION}..."
        wget -q "$RUNC_URL" -O "$RUNC_BIN" || error "Failed to download runc"
        chmod +x "$RUNC_BIN"
    fi
    
    # Copy runc to rootfs
    cp "$RUNC_BIN" "$ROOTFS_DIR/usr/bin/runc" || error "Failed to copy runc"
    chmod +x "$ROOTFS_DIR/usr/bin/runc"
    
    log "runc installed successfully"
}

# Install agent as init process
install_agent() {
    log "Installing micropod agent as init..."
    
    # Copy agent binary
    cp "$PROJECT_ROOT/bin/agent" "$ROOTFS_DIR/sbin/init" || error "Failed to copy agent"
    chmod +x "$ROOTFS_DIR/sbin/init"
    
    # Create necessary directories
    mkdir -p "$ROOTFS_DIR/containers"
    mkdir -p "$ROOTFS_DIR/proc"
    mkdir -p "$ROOTFS_DIR/sys"
    mkdir -p "$ROOTFS_DIR/dev"
    mkdir -p "$ROOTFS_DIR/tmp"
    mkdir -p "$ROOTFS_DIR/var/log"
    
    # Create basic device nodes
    if command -v mknod >/dev/null 2>&1; then
        mknod "$ROOTFS_DIR/dev/null" c 1 3 2>/dev/null || true
        mknod "$ROOTFS_DIR/dev/zero" c 1 5 2>/dev/null || true
        mknod "$ROOTFS_DIR/dev/random" c 1 8 2>/dev/null || true
        mknod "$ROOTFS_DIR/dev/urandom" c 1 9 2>/dev/null || true
    fi
    
    log "Agent installed as init process"
}

# Create ext4 filesystem image
create_ext4_image() {
    log "Creating ext4 filesystem image..."
    
    # Calculate size in MB
    SIZE_MB=${ROOTFS_SIZE%M}
    
    # Create empty file
    dd if=/dev/zero of="$OUTPUT_FILE" bs=1M count="$SIZE_MB" 2>/dev/null || error "Failed to create image file"
    
    # Create ext4 filesystem
    mke2fs -t ext4 -F "$OUTPUT_FILE" >/dev/null 2>&1 || error "Failed to create ext4 filesystem"
    
    # Mount and copy files
    MOUNT_DIR="/tmp/micropod-mount-$$"
    mkdir -p "$MOUNT_DIR"
    
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # On macOS, we need to use a different approach
        warn "On macOS, using Docker to create the filesystem..."
        create_ext4_with_docker
        return
    fi
    
    # Linux approach
    sudo mount -o loop "$OUTPUT_FILE" "$MOUNT_DIR" || error "Failed to mount image"
    
    # Copy rootfs contents
    sudo cp -a "$ROOTFS_DIR"/* "$MOUNT_DIR"/ || error "Failed to copy rootfs contents"
    
    # Unmount
    sudo umount "$MOUNT_DIR" || error "Failed to unmount image"
    rmdir "$MOUNT_DIR"
    
    log "ext4 image created: $OUTPUT_FILE ($(ls -lh "$OUTPUT_FILE" | awk '{print $5}'))"
}

# Create ext4 using Docker (for macOS)
create_ext4_with_docker() {
    log "Creating ext4 image using Docker..."
    
    # Create a temporary Dockerfile
    cat > /tmp/create-rootfs.Dockerfile << 'EOF'
FROM alpine:3.18
RUN apk add --no-cache e2fsprogs
WORKDIR /work
COPY . .
CMD ["sh", "-c", "mke2fs -t ext4 -F /work/agent-rootfs.ext4 && mkdir -p /mnt && mount -o loop /work/agent-rootfs.ext4 /mnt && cp -a rootfs/* /mnt/ && umount /mnt"]
EOF
    
    # Create the image with Docker
    cd /tmp
    cp -r "$ROOTFS_DIR" ./rootfs
    cp "$OUTPUT_FILE" ./agent-rootfs.ext4
    
    docker build -f create-rootfs.Dockerfile -t micropod-rootfs-builder . >/dev/null 2>&1 || error "Failed to build Docker image"
    docker run --privileged --rm -v "$(pwd):/work" micropod-rootfs-builder || error "Failed to create rootfs with Docker"
    
    cp ./agent-rootfs.ext4 "$OUTPUT_FILE"
    
    # Cleanup
    rm -rf ./rootfs ./agent-rootfs.ext4 create-rootfs.Dockerfile
    docker rmi micropod-rootfs-builder >/dev/null 2>&1 || true
    
    log "ext4 image created with Docker: $OUTPUT_FILE"
}

# Cleanup
cleanup() {
    log "Cleaning up temporary files..."
    rm -rf "$ROOTFS_DIR"
    rm -f /tmp/alpine-minirootfs.tar.gz
    rm -f /tmp/runc
}

# Main execution
main() {
    log "Building micropod agent rootfs..."
    log "Output file: $OUTPUT_FILE"
    log "Rootfs size: $ROOTFS_SIZE"
    
    check_dependencies
    build_agent
    setup_alpine
    install_runc
    install_agent
    create_ext4_image
    cleanup
    
    log "✅ Agent rootfs build completed successfully!"
    log "📁 File: $OUTPUT_FILE"
    log "📏 Size: $(ls -lh "$OUTPUT_FILE" | awk '{print $5}')"
    log ""
    log "🚀 Next steps:"
    log "   1. Copy to config directory: cp $OUTPUT_FILE ~/.config/micropod/"
    log "   2. Test with: micropod run alpine:latest"
}

# Handle Ctrl+C
trap cleanup EXIT

# Run main function
main "$@"