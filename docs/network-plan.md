# Micropod Network Design Plan

This document outlines the design for the networking functionality of `micropod`. The primary goals are:
1.  Provide network connectivity to MicroVMs to enable tasks like running a web server and testing it from the host.
2.  Establish a foundational network model that is compatible with future evolution towards a standard CNI (Container Network Interface) model as seen in Kubernetes.

## 1. Chosen Approach: Linux Bridge + TAP Devices

We will implement a network model based on a Linux Bridge and TAP devices. This is a classic, robust, and well-understood model that forms the basis for many container and VM networking solutions, including several Kubernetes CNI plugins.

### Network Topology

```
      +--------------------------------------------------+
      |                    Host System                   |
      |                                                  |
      |   +------------------+      +------------------+   |
      |   |    MicroVM 1     |      |    MicroVM 2     |   |
      |   | (e.g. httpd)     |      |                  |   |
      |   | eth0: 10.10.0.2  |      | eth0: 10.10.0.3  |   |
      |   +-------+----------+      +---------+--------+   |
      |           |                           |          |
      |      +----+----+                   +--+-+--+      |
      |      | tap-vm1 |                   |tap-vm2|      |
      |      +----+----+                   +--+--+-+      |
      |           |                           |          |
      |      +----------------------------------+        |
      |      |      Linux Bridge (micropod0)      |        |
      |      |      Gateway: 10.10.0.1/24       |        |
      |      +----------------+------------------+        |
      |                       |                          |
      |                       | (iptables NAT)           |
      |                       |                          |
      |                  +----+-----+                     |
      |                  | eth0     |                     |  <-- Host physical NIC
      |                  +----------+                     |
      |                       |                          |
      +-----------------------|--------------------------+
                              |
                         To Internet
```

### Workflow

1.  **Initialization**: On startup, `micropod` will ensure a persistent Linux Bridge (e.g., `micropod0`) exists. It will assign a static subnet gateway IP (e.g., `10.10.0.1/24`) to this bridge. It will also set up a `MASQUERADE` iptables rule to allow traffic from this subnet to be NAT-ed for external network access.
2.  **VM Creation**: For each MicroVM, `micropod` will:
    a. Create a new TAP device.
    b. Allocate a unique IP address from the defined subnet for the VM.
    c. Attach the TAP device to the `micropod0` bridge.
    d. Configure the Firecracker VM to use this TAP device as its network interface.
3.  **Connectivity**: All MicroVMs will be on the same L2 network, able to communicate with each other and the host via the bridge. Outbound internet access is provided by the host's NAT rule.

## 2. Implementation Details (Go-native)

To ensure the implementation is robust, portable, and does not depend on shell command execution (`ip`, `iptables`), we will use dedicated Go libraries to interact directly with the kernel's networking subsystems.

### Core Libraries

-   **`github.com/vishvananda/netlink`**: For all network device operations, including creating and managing the bridge and TAP devices.
-   **`github.com/coreos/go-iptables`**: For programmatically adding and managing the required NAT rules in `iptables`.

### Proposed Code Structure

The core logic will reside in the `pkg/network/` directory.

#### `pkg/network/manager.go`
A `Manager` struct will be the central point for all network operations.

```go
package network

import (
    "net"
    "github.com/vishvananda/netlink"
    "github.com/coreos/go-iptables/iptables"
)

// Manager handles all network operations for micropod.
type Manager struct {
    bridge     netlink.Link
    ipam       *ipam.IPAM
    iptables   *iptables.IPTables
    subnet     *net.IPNet
    bridgeName string
}

// NewManager creates and initializes a new network manager.
func NewManager(bridgeName, subnetCIDR string) (*Manager, error) { /* ... */ }

// Setup creates the bridge, configures IP, and sets up NAT.
func (m *Manager) Setup() error { /* ... */ }

// CreateTAPDevice creates a new TAP device for a VM, attaches it to the bridge,
// and returns the device and its allocated IP.
func (m *Manager) CreateTAPDevice() (netlink.Link, net.IP, error) { /* ... */ }

// Teardown cleans up network resources.
func (m *Manager) Teardown() error { /* ... */ }
```

#### `pkg/network/ipam/ipam.go` (New Package)
A simple IP Address Management (IPAM) module will be created to handle the allocation and release of IP addresses within the defined subnet, preventing conflicts.

```go
package ipam

import (
    "net"
    "sync"
)

// IPAM manages IP address allocation for a single subnet.
type IPAM struct {
    subnet    *net.IPNet
    gateway   net.IP
    allocated map[string]bool
    lock      sync.Mutex
}

// New creates a new IPAM for the given subnet.
func New(subnet *net.IPNet) (*IPAM, error) { /* ... */ }

// Allocate finds and returns the next available IP in the subnet.
func (i *IPAM) Allocate() (net.IP, error) { /* ... */ }

// Release marks an IP as available again.
func (i *IPAM) Release(ip net.IP) error { /* ... */ }
```

## 3. Advantages of this Approach

-   **Robust & Type-Safe**: Using Go libraries avoids the fragility of parsing command-line tool output.
-   **Zero Shell Dependencies**: The final `micropod` binary will not require `ip` or `iptables` to be in the host's `PATH`.
-   **Testable**: Logic can be unit-tested more easily than shell-executing code.
-   **Future-Proof**: This design directly aligns with the foundational concepts of CNI, making a future transition straightforward. The bridge and IPAM logic can be refactored into a standalone CNI plugin.
