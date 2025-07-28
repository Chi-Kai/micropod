package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"micropod/pkg/image"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Usage: go run examples/pull_image.go <image-name>")
	}

	imageName := os.Args[1]
	ctx := context.Background()

	// Create temporary directory for demonstration
	tempDir, err := os.MkdirTemp("", "micropod-demo-")
	if err != nil {
		log.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	fmt.Printf("Using temporary directory: %s\n", tempDir)

	// Create image manager
	manager, err := image.NewManager(tempDir)
	if err != nil {
		log.Fatalf("Failed to create image manager: %v", err)
	}

	// Pull the image
	fmt.Printf("Pulling image: %s\n", imageName)
	img, err := manager.PullImage(ctx, imageName)
	if err != nil {
		log.Fatalf("Failed to pull image: %v", err)
	}

	fmt.Printf("✓ Image pulled successfully!\n")
	fmt.Printf("  Reference: %s\n", img.Ref())
	fmt.Printf("  Digest: %s\n", img.Digest())
	fmt.Printf("  Layers: %d\n", len(img.Layers()))
	for i, layer := range img.Layers() {
		fmt.Printf("    Layer %d: %s\n", i+1, layer)
	}

	// Unpack the image
	unpackDir := filepath.Join(tempDir, "rootfs")
	fmt.Printf("\nUnpacking image to: %s\n", unpackDir)
	
	rootfsPath, err := manager.Unpack(ctx, imageName, unpackDir)
	if err != nil {
		log.Fatalf("Failed to unpack image: %v", err)
	}

	fmt.Printf("✓ Image unpacked successfully to: %s\n", rootfsPath)

	// List some contents of the unpacked image
	fmt.Printf("\nContents of unpacked image:\n")
	entries, err := os.ReadDir(unpackDir)
	if err != nil {
		log.Printf("Warning: failed to read unpack directory: %v", err)
	} else {
		for _, entry := range entries {
			if entry.IsDir() {
				fmt.Printf("  📁 %s/\n", entry.Name())
			} else {
				fmt.Printf("  📄 %s\n", entry.Name())
			}
		}
	}

	fmt.Printf("\nDemo completed successfully! The image is now available locally without Docker.\n")
}