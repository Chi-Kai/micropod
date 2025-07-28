package rootfs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Creator struct {
	rootfsDir string
	mountDir  string
}

func NewCreator(rootfsdir string) (*Creator, error) {

	mountDir := "/tmp/micropod-mounts"
	if err := os.MkdirAll(mountDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create mount directory: %w", err)
	}
	
	return &Creator{
		rootfsDir: rootfsdir,
		mountDir:  mountDir,
	}, nil
}

func (c *Creator) Create(tarPath, vmID string) (string, error) {
	ext4Path := filepath.Join(c.rootfsDir, fmt.Sprintf("%s.ext4", vmID))
	mountPoint := filepath.Join(c.mountDir, vmID)
	
	defer func() {
		c.unmount(mountPoint)
		c.removeMount(mountPoint)
	}()
	
	if err := c.checkSudoAvailable(); err != nil {
		return "", fmt.Errorf("sudo access required: %w", err)
	}
	
	if err := c.createSparseFile(ext4Path); err != nil {
		return "", fmt.Errorf("failed to create sparse file: %w", err)
	}
	
	if err := c.formatExt4(ext4Path); err != nil {
		c.cleanup(ext4Path)
		return "", fmt.Errorf("failed to format ext4: %w", err)
	}
	
	if err := c.createMountPoint(mountPoint); err != nil {
		c.cleanup(ext4Path)
		return "", fmt.Errorf("failed to create mount point: %w", err)
	}
	
	if err := c.mount(ext4Path, mountPoint); err != nil {
		c.cleanup(ext4Path)
		return "", fmt.Errorf("failed to mount: %w", err)
	}
	
	if err := c.extractTar(tarPath, mountPoint); err != nil {
		c.cleanup(ext4Path)
		return "", fmt.Errorf("failed to extract tar: %w", err)
	}
	
	if err := c.unmount(mountPoint); err != nil {
		c.cleanup(ext4Path)
		return "", fmt.Errorf("failed to unmount: %w", err)
	}
	
	return ext4Path, nil
}

// CreateFromDir creates an ext4 filesystem from a directory instead of a tar file
func (c *Creator) CreateFromDir(sourceDir, vmID string) (string, error) {
	ext4Path := filepath.Join(c.rootfsDir, fmt.Sprintf("%s.ext4", vmID))
	mountPoint := filepath.Join(c.mountDir, vmID)
	
	defer func() {
		c.unmount(mountPoint)
		c.removeMount(mountPoint)
	}()
	
	if err := c.checkSudoAvailable(); err != nil {
		return "", fmt.Errorf("sudo access required: %w", err)
	}
	
	if err := c.createSparseFile(ext4Path); err != nil {
		return "", fmt.Errorf("failed to create sparse file: %w", err)
	}
	
	if err := c.formatExt4(ext4Path); err != nil {
		c.cleanup(ext4Path)
		return "", fmt.Errorf("failed to format ext4: %w", err)
	}
	
	if err := c.createMountPoint(mountPoint); err != nil {
		c.cleanup(ext4Path)
		return "", fmt.Errorf("failed to create mount point: %w", err)
	}
	
	if err := c.mount(ext4Path, mountPoint); err != nil {
		c.cleanup(ext4Path)
		return "", fmt.Errorf("failed to mount: %w", err)
	}
	
	if err := c.copyDir(sourceDir, mountPoint); err != nil {
		c.cleanup(ext4Path)
		return "", fmt.Errorf("failed to copy directory: %w", err)
	}
	
	if err := c.unmount(mountPoint); err != nil {
		c.cleanup(ext4Path)
		return "", fmt.Errorf("failed to unmount: %w", err)
	}
	
	return ext4Path, nil
}

func (c *Creator) checkSudoAvailable() error {
	cmd := exec.Command("sudo", "-n", "true")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo access not available (run 'sudo true' first or configure passwordless sudo): %w", err)
	}
	return nil
}

func (c *Creator) createSparseFile(ext4Path string) error {
	fmt.Printf("Creating sparse file: %s\n", ext4Path)
	
	cmd := exec.Command("dd", "if=/dev/zero", "of="+ext4Path, "bs=1M", "count=0", "seek=2048")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create sparse file with dd: %w", err)
	}
	
	return nil
}

func (c *Creator) formatExt4(ext4Path string) error {
	fmt.Printf("Formatting ext4 filesystem: %s\n", ext4Path)
	
	cmd := exec.Command("sudo", "mkfs.ext4", "-F", ext4Path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to format ext4: %w", err)
	}
	
	return nil
}

func (c *Creator) createMountPoint(mountPoint string) error {
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return fmt.Errorf("failed to create mount point: %w", err)
	}
	return nil
}

func (c *Creator) mount(ext4Path, mountPoint string) error {
	fmt.Printf("Mounting %s to %s\n", ext4Path, mountPoint)
	
	cmd := exec.Command("sudo", "mount", "-o", "loop", ext4Path, mountPoint)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to mount ext4 file: %w", err)
	}
	
	return nil
}

func (c *Creator) extractTar(tarPath, mountPoint string) error {
	fmt.Printf("Extracting tar %s to %s\n", tarPath, mountPoint)
	
	cmd := exec.Command("sudo", "tar", "-xf", tarPath, "-C", mountPoint)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract tar: %w", err)
	}
	
	return nil
}

func (c *Creator) copyDir(sourceDir, mountPoint string) error {
	fmt.Printf("Copying directory %s to %s\n", sourceDir, mountPoint)
	
	cmd := exec.Command("sudo", "cp", "-a", sourceDir+"/.", mountPoint)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to copy directory: %w", err)
	}
	
	return nil
}

func (c *Creator) unmount(mountPoint string) error {
	if c.isMounted(mountPoint) {
		fmt.Printf("Unmounting %s\n", mountPoint)
		cmd := exec.Command("sudo", "umount", mountPoint)
		if err := cmd.Run(); err != nil {
			fmt.Printf("Warning: failed to unmount %s: %v\n", mountPoint, err)
			return err
		}
	}
	return nil
}

func (c *Creator) isMounted(mountPoint string) bool {
	cmd := exec.Command("mount")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	
	return strings.Contains(string(output), mountPoint)
}

func (c *Creator) removeMount(mountPoint string) error {
	if err := os.RemoveAll(mountPoint); err != nil {
		fmt.Printf("Warning: failed to remove mount point %s: %v\n", mountPoint, err)
		return err
	}
	return nil
}

func (c *Creator) cleanup(ext4Path string) {
	if err := os.Remove(ext4Path); err != nil && !os.IsNotExist(err) {
		fmt.Printf("Warning: failed to cleanup ext4 file %s: %v\n", ext4Path, err)
	}
}

func (c *Creator) RemoveRootfs(ext4Path string) error {
	if err := os.Remove(ext4Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove rootfs file %s: %w", ext4Path, err)
	}
	return nil
}

func (c *Creator) GetSizeGB(ext4Path string) (float64, error) {
	info, err := os.Stat(ext4Path)
	if err != nil {
		return 0, fmt.Errorf("failed to stat ext4 file: %w", err)
	}
	
	sizeGB := float64(info.Size()) / (1024 * 1024 * 1024)
	return sizeGB, nil
}