package firecracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"

	"micropod/pkg/network"
)

type Client struct {
	socketPath string
	httpClient *http.Client
	process    *os.Process
}

type BootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args"`
}

type Drive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsReadOnly   bool   `json:"is_read_only"`
	IsRootDevice bool   `json:"is_root_device"`
}

type MachineConfig struct {
	VcpuCount  int `json:"vcpu_count"`
	MemSizeMib int `json:"mem_size_mib"`
}

type NetworkInterface struct {
	IfaceID     string `json:"iface_id"`
	HostDevName string `json:"host_dev_name"`
	GuestMAC    string `json:"guest_mac"`
}

type Action struct {
	ActionType string `json:"action_type"`
}

func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) LaunchVM(kernelPath, rootfsPath string, vcpus int, memoryMB int, bootArgs string, netConfig *network.Config, logPath string) error {
	if err := c.startFirecrackerProcess(logPath); err != nil {
		return fmt.Errorf("failed to start firecracker process: %w", err)
	}

	if err := c.waitForSocket(); err != nil {
		c.killProcess()
		return fmt.Errorf("failed to wait for socket: %w", err)
	}

	if err := c.configureBootSource(kernelPath, bootArgs); err != nil {
		c.killProcess()
		return fmt.Errorf("failed to configure boot source: %w", err)
	}

	if err := c.configureDrive(rootfsPath); err != nil {
		c.killProcess()
		return fmt.Errorf("failed to configure drive: %w", err)
	}

	if err := c.configureMachine(vcpus, memoryMB); err != nil {
		c.killProcess()
		return fmt.Errorf("failed to configure machine: %w", err)
	}

	if netConfig != nil {
		if err := c.configureNetworkInterface(netConfig); err != nil {
			c.killProcess()
			return fmt.Errorf("failed to configure network interface: %w", err)
		}
	}

	if err := c.startInstance(); err != nil {
		c.killProcess()
		return fmt.Errorf("failed to start instance: %w", err)
	}

	return nil
}

func (c *Client) startFirecrackerProcess(logPath string) error {
	if err := c.checkFirecrackerBinary(); err != nil {
		return err
	}

	if err := c.removeSocketFile(); err != nil {
		return err
	}

	fmt.Printf("Starting firecracker process with socket: %s\n", c.socketPath)

	cmd := exec.Command("firecracker", "--api-sock", c.socketPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Redirect VM's console output to log file for kernel and application logs
	if logPath != "" {
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start firecracker: %w", err)
	}

	c.process = cmd.Process

	go func() {
		cmd.Wait()
	}()

	return nil
}

func (c *Client) checkFirecrackerBinary() error {
	cmd := exec.Command("firecracker", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("firecracker binary not available in PATH: %w", err)
	}
	return nil
}

func (c *Client) removeSocketFile() error {
	if err := os.Remove(c.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}
	return nil
}

func (c *Client) waitForSocket() error {
	timeout := time.Now().Add(10 * time.Second)

	for time.Now().Before(timeout) {
		if _, err := os.Stat(c.socketPath); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for socket %s", c.socketPath)
}

func (c *Client) configureBootSource(kernelPath string, bootArgs string) error {
	defaultBootArgs := "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw"
	if bootArgs != "" {
		defaultBootArgs = defaultBootArgs + " " + bootArgs
	}

	bootSource := BootSource{
		KernelImagePath: kernelPath,
		BootArgs:        defaultBootArgs,
	}

	return c.makeAPIRequest("PUT", "/boot-source", bootSource)
}

func (c *Client) configureDrive(rootfsPath string) error {
	drive := Drive{
		DriveID:      "vda",
		PathOnHost:   rootfsPath,
		IsReadOnly:   false,
		IsRootDevice: true,
	}

	return c.makeAPIRequest("PUT", "/drives/vda", drive)
}

func (c *Client) configureMachine(vcpus int, memoryMB int) error {
	machineConfig := MachineConfig{
		VcpuCount:  vcpus,
		MemSizeMib: memoryMB,
	}

	return c.makeAPIRequest("PUT", "/machine-config", machineConfig)
}

func (c *Client) configureNetworkInterface(netConfig *network.Config) error {
	networkInterface := NetworkInterface{
		IfaceID:     "eth0",
		HostDevName: netConfig.TapDevice,
		GuestMAC:    netConfig.GuestMAC,
	}

	return c.makeAPIRequest("PUT", "/network-interfaces/eth0", networkInterface)
}

func (c *Client) startInstance() error {
	action := Action{
		ActionType: "InstanceStart",
	}

	return c.makeAPIRequest("PUT", "/actions", action)
}

func (c *Client) makeAPIRequest(method, path string, body interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest(method, "http://localhost"+path, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *Client) GetPID() int {
	if c.process == nil {
		return 0
	}
	return c.process.Pid
}

func (c *Client) Stop() error {
	if c.process != nil {
		if err := c.process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
		c.process.Wait()
	}

	if err := c.removeSocketFile(); err != nil {
		fmt.Printf("Warning: failed to remove socket file: %v\n", err)
	}

	return nil
}

func (c *Client) killProcess() {
	if c.process != nil {
		c.process.Kill()
		c.process.Wait()
	}
}

func (c *Client) IsRunning() bool {
	if c.process == nil {
		return false
	}

	if err := c.process.Signal(syscall.Signal(0)); err != nil {
		return false
	}

	return true
}
