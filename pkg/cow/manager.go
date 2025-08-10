package cow

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Manager struct {
	deviceDir string
	cowDir    string
}

type BaseDevice struct {
	Name       string
	LoopDevice string
	ImagePath  string
	Size       int64
}

type SnapshotDevice struct {
	Name       string
	BaseDevice string
	CowDevice  string
	DevicePath string
}

func NewManager(deviceDir, cowDir string) (*Manager, error) {
	if err := os.MkdirAll(deviceDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create device directory: %w", err)
	}
	if err := os.MkdirAll(cowDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cow directory: %w", err)
	}
	
	return &Manager{
		deviceDir: deviceDir,
		cowDir:    cowDir,
	}, nil
}

func (m *Manager) CreateBaseDevice(imageName, imagePath string) (*BaseDevice, error) {
	deviceName := fmt.Sprintf("micropod-base-%s", sanitizeName(imageName))
	
	if m.baseDeviceExists(deviceName) {
		return m.getExistingBaseDevice(deviceName, imagePath)
	}
	
	loopDevice, err := m.createLoopDevice(imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create loop device: %w", err)
	}
	
	if err := m.createLinearDevice(deviceName, loopDevice); err != nil {
		m.detachLoopDevice(loopDevice)
		return nil, fmt.Errorf("failed to create linear device: %w", err)
	}
	
	stat, err := os.Stat(imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat image: %w", err)
	}
	
	return &BaseDevice{
		Name:       deviceName,
		LoopDevice: loopDevice,
		ImagePath:  imagePath,
		Size:       stat.Size(),
	}, nil
}

func (m *Manager) CreateSnapshotDevice(vmID string, baseDevice *BaseDevice) (*SnapshotDevice, error) {
	snapshotName := fmt.Sprintf("micropod-vm-%s", vmID)
	cowPath := filepath.Join(m.cowDir, fmt.Sprintf("%s.cow", vmID))
	
	if err := m.createCowFile(cowPath, baseDevice.Size/10); err != nil {
		return nil, fmt.Errorf("failed to create cow file: %w", err)
	}
	
	cowLoopDevice, err := m.createLoopDevice(cowPath)
	if err != nil {
		os.Remove(cowPath)
		return nil, fmt.Errorf("failed to create cow loop device: %w", err)
	}
	
	if err := m.createSnapshotMapping(snapshotName, baseDevice.Name, cowLoopDevice); err != nil {
		m.detachLoopDevice(cowLoopDevice)
		os.Remove(cowPath)
		return nil, fmt.Errorf("failed to create snapshot mapping: %w", err)
	}
	
	devicePath := fmt.Sprintf("/dev/mapper/%s", snapshotName)
	return &SnapshotDevice{
		Name:       snapshotName,
		BaseDevice: baseDevice.Name,
		CowDevice:  cowLoopDevice,
		DevicePath: devicePath,
	}, nil
}

func (m *Manager) RemoveSnapshotDevice(snapshot *SnapshotDevice) error {
	if err := m.removeDeviceMapping(snapshot.Name); err != nil {
		return fmt.Errorf("failed to remove snapshot mapping: %w", err)
	}
	
	if err := m.detachLoopDevice(snapshot.CowDevice); err != nil {
		return fmt.Errorf("failed to detach cow loop device: %w", err)
	}
	
	cowPath := m.getCowPath(snapshot.Name)
	if err := os.Remove(cowPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove cow file: %w", err)
	}
	
	return nil
}

func (m *Manager) RemoveBaseDevice(baseDevice *BaseDevice) error {
	if err := m.removeDeviceMapping(baseDevice.Name); err != nil {
		return fmt.Errorf("failed to remove base device mapping: %w", err)
	}
	
	if err := m.detachLoopDevice(baseDevice.LoopDevice); err != nil {
		return fmt.Errorf("failed to detach loop device: %w", err)
	}
	
	return nil
}

func (m *Manager) createLoopDevice(imagePath string) (string, error) {
	cmd := exec.Command("sudo", "losetup", "--find", "--show", imagePath)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to create loop device: %w", err)
	}
	
	loopDevice := strings.TrimSpace(string(output))
	return loopDevice, nil
}

func (m *Manager) detachLoopDevice(loopDevice string) error {
	cmd := exec.Command("sudo", "losetup", "-d", loopDevice)
	return cmd.Run()
}

func (m *Manager) createLinearDevice(deviceName, loopDevice string) error {
	// Get device size in 512-byte sectors
	sizeCmd := exec.Command("sudo", "blockdev", "--getsz", loopDevice)
	sizeOutput, err := sizeCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get device size: %w", err)
	}
	
	size := strings.TrimSpace(string(sizeOutput))
	
	// Create the device mapping table
	table := fmt.Sprintf("0 %s linear %s 0", size, loopDevice)
	
	cmd := exec.Command("sudo", "dmsetup", "create", deviceName, "--readonly")
	cmd.Stdin = strings.NewReader(table)
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create linear device: %w", err)
	}
	
	return nil
}

func (m *Manager) createCowFile(cowPath string, sizeBytes int64) error {
	cmd := exec.Command("dd", "if=/dev/zero", "of="+cowPath, "bs=1M", "count=0", fmt.Sprintf("seek=%d", sizeBytes/(1024*1024)))
	return cmd.Run()
}

func (m *Manager) createSnapshotMapping(snapshotName, baseDeviceName, cowLoopDevice string) error {
	baseDevicePath := fmt.Sprintf("/dev/mapper/%s", baseDeviceName)
	
	// Get device size in 512-byte sectors
	sizeCmd := exec.Command("sudo", "blockdev", "--getsz", baseDevicePath)
	sizeOutput, err := sizeCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get base device size: %w", err)
	}
	
	size := strings.TrimSpace(string(sizeOutput))
	
	// Create the snapshot mapping table
	table := fmt.Sprintf("0 %s snapshot %s %s P 8", size, baseDevicePath, cowLoopDevice)
	
	cmd := exec.Command("sudo", "dmsetup", "create", snapshotName)
	cmd.Stdin = strings.NewReader(table)
	
	return cmd.Run()
}

func (m *Manager) removeDeviceMapping(deviceName string) error {
	cmd := exec.Command("sudo", "dmsetup", "remove", deviceName)
	return cmd.Run()
}

// RemoveDeviceMapping 导出的设备映射移除方法
func (m *Manager) RemoveDeviceMapping(deviceName string) error {
	return m.removeDeviceMapping(deviceName)
}

func (m *Manager) baseDeviceExists(deviceName string) bool {
	devicePath := fmt.Sprintf("/dev/mapper/%s", deviceName)
	_, err := os.Stat(devicePath)
	return err == nil
}

func (m *Manager) getExistingBaseDevice(deviceName, imagePath string) (*BaseDevice, error) {
	cmd := exec.Command("sudo", "dmsetup", "table", deviceName)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get device table: %w", err)
	}
	
	parts := strings.Fields(string(output))
	if len(parts) < 4 {
		return nil, fmt.Errorf("invalid device table format")
	}
	
	loopDevice := parts[3]
	
	stat, err := os.Stat(imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat image: %w", err)
	}
	
	return &BaseDevice{
		Name:       deviceName,
		LoopDevice: loopDevice,
		ImagePath:  imagePath,
		Size:       stat.Size(),
	}, nil
}

func (m *Manager) getCowPath(snapshotName string) string {
	vmID := strings.TrimPrefix(snapshotName, "micropod-vm-")
	return filepath.Join(m.cowDir, fmt.Sprintf("%s.cow", vmID))
}

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, ":", "_")
	name = strings.ReplaceAll(name, ".", "_")
	return name
}