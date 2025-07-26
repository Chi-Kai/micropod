package image

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Handler struct {
	workDir string
}

func NewHandler(workdir string) (*Handler, error) {
	return &Handler{
		workDir: workdir,
	}, nil
}

func (h *Handler) PullAndExport(imageName string) (string, error) {
	if err := h.checkDockerAvailable(); err != nil {
		return "", fmt.Errorf("docker is not available: %w", err)
	}
	
	if err := h.pullImage(imageName); err != nil {
		return "", fmt.Errorf("failed to pull image: %w", err)
	}
	
	containerID, err := h.createContainer(imageName)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}
	
	defer func() {
		h.removeContainer(containerID)
	}()
	
	tarPath, err := h.exportContainer(containerID, imageName)
	if err != nil {
		return "", fmt.Errorf("failed to export container: %w", err)
	}
	
	return tarPath, nil
}

func (h *Handler) checkDockerAvailable() error {
	cmd := exec.Command("docker", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker command not available: %w", err)
	}
	
	cmd = exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker daemon not running: %w", err)
	}
	
	return nil
}

func (h *Handler) pullImage(imageName string) error {
	fmt.Printf("Pulling image: %s\n", imageName)
	
	cmd := exec.Command("docker", "pull", imageName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pull image %s: %w", imageName, err)
	}
	
	return nil
}

func (h *Handler) createContainer(imageName string) (string, error) {
	cmd := exec.Command("docker", "create", imageName)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to create container from image %s: %w", imageName, err)
	}
	
	containerID := strings.TrimSpace(string(output))
	if containerID == "" {
		return "", fmt.Errorf("empty container ID returned")
	}
	
	return containerID, nil
}

func (h *Handler) exportContainer(containerID, imageName string) (string, error) {
	safeImageName := strings.ReplaceAll(imageName, "/", "_")
	safeImageName = strings.ReplaceAll(safeImageName, ":", "_")
	
	tarPath := filepath.Join(h.workDir, fmt.Sprintf("%s.tar", safeImageName))
	
	fmt.Printf("Exporting container to: %s\n", tarPath)
	
	cmd := exec.Command("docker", "export", containerID, "-o", tarPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to export container %s: %w", containerID, err)
	}
	
	if _, err := os.Stat(tarPath); err != nil {
		return "", fmt.Errorf("exported tar file not found: %w", err)
	}
	
	return tarPath, nil
}

func (h *Handler) removeContainer(containerID string) error {
	cmd := exec.Command("docker", "rm", containerID)
	return cmd.Run()
}

func (h *Handler) CleanupTar(tarPath string) error {
	if err := os.Remove(tarPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove tar file %s: %w", tarPath, err)
	}
	return nil
}