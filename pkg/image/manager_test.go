package image

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestManager_PullImage(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "micropod-test-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create manager
	manager, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	ctx := context.Background()

	t.Run("pull small image", func(t *testing.T) {
		// Test with a very small image (hello-world is typically ~20KB)
		imageName := "hello-world:latest"

		img, err := manager.PullImage(ctx, imageName)
		if err != nil {
			t.Fatalf("Failed to pull image: %v", err)
		}

		if img.Ref() != imageName {
			t.Errorf("Expected ref %s, got %s", imageName, img.Ref())
		}

		if img.Digest() == "" {
			t.Error("Expected non-empty digest")
		}

		if len(img.Layers()) == 0 {
			t.Error("Expected at least one layer")
		}
	})

	t.Run("get existing image", func(t *testing.T) {
		imageName := "hello-world:latest"

		img, err := manager.GetImage(ctx, imageName)
		if err != nil {
			t.Fatalf("Failed to get image: %v", err)
		}

		if img.Ref() != imageName {
			t.Errorf("Expected ref %s, got %s", imageName, img.Ref())
		}
	})

	t.Run("unpack image", func(t *testing.T) {
		imageName := "hello-world:latest"
		unpackDir := filepath.Join(tempDir, "unpack")

		rootfsPath, err := manager.Unpack(ctx, imageName, unpackDir)
		if err != nil {
			t.Fatalf("Failed to unpack image: %v", err)
		}

		if rootfsPath != unpackDir {
			t.Errorf("Expected rootfs path %s, got %s", unpackDir, rootfsPath)
		}

		// Check if some files exist in the unpacked directory
		if _, err := os.Stat(unpackDir); os.IsNotExist(err) {
			t.Error("Unpack directory does not exist")
		}
	})
}

func TestManager_GetImage_NotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "micropod-test-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	manager, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	ctx := context.Background()
	_, err = manager.GetImage(ctx, "nonexistent:latest")
	if err == nil {
		t.Error("Expected error for nonexistent image")
	}
}
