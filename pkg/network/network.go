package network

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Config struct {
	VMID         string      `json:"vmId"`
	TapDevice    string      `json:"tapDevice"`
	GuestIP      string      `json:"guestIp"`
	GatewayIP    string      `json:"gatewayIp"`
	Mask         string      `json:"mask"`
	GuestMAC     string      `json:"guestMac"`
	PortMappings map[int]int `json:"portMappings"`
}

func Setup(vmID string, portMappings []string) (*Config, error) {
	config := &Config{
		VMID:         vmID,
		TapDevice:    fmt.Sprintf("tap-%s", vmID[:8]),
		Mask:         "24",
		PortMappings: make(map[int]int),
	}

	// Generate IP addresses (simple static allocation)
	vmIndex := hashVMID(vmID)%254 + 1 // VM index 1-254
	config.GuestIP = fmt.Sprintf("172.18.%d.2", vmIndex)
	config.GatewayIP = fmt.Sprintf("172.18.%d.1", vmIndex)

	// Generate MAC address
	config.GuestMAC = fmt.Sprintf("02:FC:00:00:%02x:%02x", vmIndex, vmIndex)

	// Parse port mappings
	var err error
	config.PortMappings, err = parsePortMappings(portMappings)
	if err != nil {
		return nil, fmt.Errorf("failed to parse port mappings: %w", err)
	}

	// Create TAP device
	if err := createTapDevice(config.TapDevice, config.GatewayIP, config.Mask); err != nil {
		return nil, fmt.Errorf("failed to create TAP device: %w", err)
	}

	// Configure iptables
	if err := setupIptables(config); err != nil {
		cleanupTapDevice(config.TapDevice)
		return nil, fmt.Errorf("failed to setup iptables: %w", err)
	}

	return config, nil
}

func Teardown(config *Config) error {
	if config == nil {
		return nil
	}

	// Clean up iptables rules
	if err := cleanupIptables(config); err != nil {
		return fmt.Errorf("failed to cleanup iptables: %w", err)
	}

	// Remove TAP device
	if err := cleanupTapDevice(config.TapDevice); err != nil {
		return fmt.Errorf("failed to cleanup TAP device: %w", err)
	}

	return nil
}

func parsePortMappings(mappings []string) (map[int]int, error) {
	result := make(map[int]int)

	for _, mapping := range mappings {
		parts := strings.Split(mapping, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid port mapping format: %s (expected host:guest)", mapping)
		}

		hostPort, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid host port: %s", parts[0])
		}

		guestPort, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("invalid guest port: %s", parts[1])
		}

		result[hostPort] = guestPort
	}

	return result, nil
}

func createTapDevice(tapName, gatewayIP, mask string) error {
	commands := [][]string{
		{"ip", "tuntap", "add", "dev", tapName, "mode", "tap"},
		{"ip", "addr", "add", fmt.Sprintf("%s/%s", gatewayIP, mask), "dev", tapName},
		{"ip", "link", "set", tapName, "up"},
	}

	for _, cmd := range commands {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			return fmt.Errorf("failed to execute %v: %w", cmd, err)
		}
	}

	return nil
}

func setupIptables(config *Config) error {
	// Enable IP forwarding
	if err := exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run(); err != nil {
		return fmt.Errorf("failed to enable IP forwarding: %w", err)
	}

	// Get default network interface
	defaultIface, err := getDefaultInterface()
	if err != nil {
		return fmt.Errorf("failed to get default interface: %w", err)
	}

	// NAT rule for outbound traffic
	natCmd := []string{
		"iptables", "-t", "nat", "-A", "POSTROUTING",
		"-s", config.GuestIP, "-o", defaultIface, "-j", "MASQUERADE",
	}
	if err := exec.Command(natCmd[0], natCmd[1:]...).Run(); err != nil {
		return fmt.Errorf("failed to add NAT rule: %w", err)
	}

	// Port forwarding rules
	for hostPort, guestPort := range config.PortMappings {
		dnatCmd := []string{
			"iptables", "-t", "nat", "-A", "PREROUTING",
			"-p", "tcp", "--dport", strconv.Itoa(hostPort),
			"-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%d", config.GuestIP, guestPort),
		}
		if err := exec.Command(dnatCmd[0], dnatCmd[1:]...).Run(); err != nil {
			return fmt.Errorf("failed to add DNAT rule for port %d: %w", hostPort, err)
		}
	}

	return nil
}

func cleanupIptables(config *Config) error {
	// Get default network interface
	defaultIface, err := getDefaultInterface()
	if err != nil {
		return fmt.Errorf("failed to get default interface: %w", err)
	}

	// Remove NAT rule
	natCmd := []string{
		"iptables", "-t", "nat", "-D", "POSTROUTING",
		"-s", config.GuestIP, "-o", defaultIface, "-j", "MASQUERADE",
	}
	exec.Command(natCmd[0], natCmd[1:]...).Run() // Ignore errors for cleanup

	// Remove port forwarding rules
	for hostPort, guestPort := range config.PortMappings {
		dnatCmd := []string{
			"iptables", "-t", "nat", "-D", "PREROUTING",
			"-p", "tcp", "--dport", strconv.Itoa(hostPort),
			"-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%d", config.GuestIP, guestPort),
		}
		exec.Command(dnatCmd[0], dnatCmd[1:]...).Run() // Ignore errors for cleanup
	}

	return nil
}

func cleanupTapDevice(tapName string) error {
	cmd := exec.Command("ip", "link", "delete", tapName)
	return cmd.Run() // May return error if device doesn't exist, which is fine
}

func getDefaultInterface() (string, error) {
	cmd := exec.Command("ip", "route", "show", "default")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	// Parse output to find default interface
	// Example: "default via 192.168.1.1 dev eth0 proto dhcp metric 100"
	parts := strings.Fields(string(output))
	for i, part := range parts {
		if part == "dev" && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}

	return "", fmt.Errorf("could not determine default interface")
}

func hashVMID(vmID string) int {
	hash := 0
	for _, char := range vmID {
		hash = (hash*31 + int(char)) % 254
	}
	if hash < 0 {
		hash = -hash
	}
	return hash
}
