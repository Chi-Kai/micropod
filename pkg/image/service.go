package image

import (
	"context"
)

// ImageService defines the interface for managing container images.
type ImageService interface {
	// PullImage pulls an image from a remote registry and stores it locally.
	// refString is the image reference, e.g., "alpine:latest".
	PullImage(ctx context.Context, refString string) (Image, error)

	// GetImage retrieves image information from local storage.
	GetImage(ctx context.Context, refString string) (Image, error)

	// Unpack creates a root filesystem from a locally stored image.
	// It returns the path to the created rootfs.
	Unpack(ctx context.Context, refString string, destPath string) (string, error)

	// DeleteImage removes an image from local storage.
	DeleteImage(ctx context.Context, refString string) error
}

// Image represents a locally stored container image.
type Image interface {
	// Ref returns the original reference string.
	Ref() string
	// Digest returns the manifest digest (sha256:...).
	Digest() string
	// Layers returns the digests of all layers in order.
	Layers() []string
}
