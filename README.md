# MicroPod - Secure Container Engine

MicroPod is a command-line tool that runs OCI container images in Firecracker microVMs for enhanced security isolation. It provides stronger security boundaries compared to traditional containers by leveraging hardware virtualization.

## Features

- **Enhanced Security**: Run containers in isolated Firecracker microVMs with hardware-level isolation
- **OCI Compatibility**: Works with standard Docker images and OCI container images
- **Simple CLI**: Easy-to-use command-line interface similar to Docker
- **VM Management**: List, run, and stop containerized VMs with persistent state tracking

## Prerequisites

### System Requirements

- **Linux only** (Ubuntu 18.04+, CentOS 7+, or similar)
- Root/sudo access (required for filesystem operations)
- x86_64 architecture

### Dependencies

1. **Firecracker**: Download the latest release from [GitHub](https://github.com/firecracker-microvm/firecracker/releases)
   ```bash
   wget https://github.com/firecracker-microvm/firecracker/releases/latest/download/firecracker-v1.4.1-x86_64.tgz
   tar -xzf firecracker-v1.4.1-x86_64.tgz
   sudo cp release-v1.4.1-x86_64/firecracker-v1.4.1-x86_64 /usr/local/bin/firecracker
   sudo chmod +x /usr/local/bin/firecracker
   ```

2. **Docker**: Required for pulling and processing container images
   ```bash
   # Ubuntu/Debian
   sudo apt-get update
   sudo apt-get install docker.io
   sudo systemctl start docker
   sudo usermod -aG docker $USER
   ```

3. **Guest Kernel**: Download a compatible Linux kernel for the microVMs
   ```bash
   ./scripts/download-kernel.sh
   ```

## Installation

1. Clone the repository:
   ```bash
   git clone <repository-url>
   cd micropod
   ```

2. Build the binary:
   ```bash
   go build -o micropod ./cmd/micropod
   ```

3. (Optional) Install to system PATH:
   ```bash
   sudo cp micropod /usr/local/bin/
   ```

## Usage

### Run a Container in a MicroVM

```bash
./micropod run nginx:latest
```

This command will:
1. Pull the nginx image using Docker
2. Create an ext4 root filesystem from the image
3. Launch a Firecracker microVM with the filesystem
4. Return a unique VM ID

### List Running VMs

```bash
./micropod list
```

Shows all running VMs with their IDs, images, states, PIDs, and creation times.

### Stop a VM

```bash
./micropod stop <vm-id>
```

Stops the specified VM and cleans up all associated resources.

## Architecture

MicroPod uses a modular architecture:

- **CLI Layer** (`cmd/micropod`): Cobra-based command-line interface
- **Manager** (`pkg/manager`): Core orchestration and workflow management
- **State Store** (`pkg/state`): JSON-based VM state persistence
- **Image Handler** (`pkg/image`): Docker CLI integration for image processing
- **Rootfs Creator** (`pkg/rootfs`): ext4 filesystem creation from container tarballs
- **Firecracker Client** (`pkg/firecracker`): REST API client for Firecracker communication

## Configuration

MicroPod stores its configuration and state in `~/.config/micropod/`:

- `vms.json`: Running VM state database
- `vmlinux`: Guest Linux kernel (downloaded by script)
- `rootfs/`: VM root filesystem files (*.ext4)
- `images/`: Temporary container image exports (*.tar)

## Security Considerations

- **Sudo Required**: MicroPod requires sudo access for mounting loop devices and creating ext4 filesystems
- **File Permissions**: VM files are created with appropriate permissions (0644 for configs, 0755 for executables)
- **Resource Isolation**: Each VM runs in complete hardware isolation via Firecracker
- **Process Isolation**: Firecracker processes run as separate system processes

## Troubleshooting

### Common Issues

1. **"sudo access not available"**
   - Run `sudo true` first, or configure passwordless sudo
   - Ensure your user is in the sudo group

2. **"firecracker binary not available"**
   - Install Firecracker binary in your PATH
   - Verify with `firecracker --version`

3. **"docker daemon not running"**
   - Start Docker: `sudo systemctl start docker`
   - Add user to docker group: `sudo usermod -aG docker $USER`

4. **"kernel file not found"**
   - Run `./scripts/download-kernel.sh` to download a compatible kernel
   - Or manually place a vmlinux kernel at `~/.config/micropod/vmlinux`

### Debug Mode

Set environment variable for detailed logging:
```bash
export MICROPOD_DEBUG=1
./micropod run alpine:latest
```

## Limitations (V1.0 MVP)

- No networking support (containers run in isolated environment)
- No volume mounting
- No container orchestration
- Single-container VMs only
- Linux host required
- Depends on Docker daemon

## Future Roadmap

- **V2.0**: Native OCI image handling, TAP networking, port forwarding
- **V3.0**: Kubernetes CRI compatibility, multi-container pods, vsock communication

## Contributing

This is a learning/demonstration project. Feel free to fork and experiment!

## License

MIT License - see LICENSE file for details.