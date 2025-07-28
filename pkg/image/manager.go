package image

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
)

// Manager implements ImageService using OCI-native operations.
type Manager struct {
	imageDir string
}

// NewManager creates a new image manager with the specified storage directory.
func NewManager(imageDir string) (*Manager, error) {
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create image directory: %w", err)
	}
	return &Manager{imageDir: imageDir}, nil
}

// image represents a locally stored container image.
type image struct {
	ref    string
	digest string
	layers []string
}

func (i *image) Ref() string {
	return i.ref
}

func (i *image) Digest() string {
	return i.digest
}

func (i *image) Layers() []string {
	return i.layers
}

// PullImage pulls an image from a remote registry and stores it locally.
func (m *Manager) PullImage(ctx context.Context, refString string) (Image, error) {
	// Parse the image reference to validate it
	_, err := name.ParseReference(refString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image reference %s: %w", refString, err)
	}

	// Check if image already exists locally
	if img, err := m.GetImage(ctx, refString); err == nil {
		return img, nil
	}

	// Pull the image
	img, err := crane.Pull(refString)
	if err != nil {
		return nil, fmt.Errorf("failed to pull image %s: %w", refString, err)
	}

	// Get image digest
	digest, err := img.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get image digest: %w", err)
	}

	// Create or get the OCI layout path
	layoutPath := m.getLayoutPath(refString)
	p, err := layout.FromPath(layoutPath)
	if err != nil {
		// If path does not exist, create it
		p, err = layout.Write(layoutPath, empty.Index)
		if err != nil {
			return nil, fmt.Errorf("failed to create layout at path %s: %w", layoutPath, err)
		}
	}

	// Append the pulled image to the layout
	if err := p.AppendImage(img); err != nil {
		return nil, fmt.Errorf("failed to append image to layout: %w", err)
	}

	// Get layer information
	layers, err := m.getImageLayers(img)
	if err != nil {
		return nil, fmt.Errorf("failed to get image layers: %w", err)
	}

	return &image{
		ref:    refString,
		digest: digest.String(),
		layers: layers,
	}, nil
}

// GetImage retrieves image information from local storage.
func (m *Manager) GetImage(ctx context.Context, refString string) (Image, error) {
	layoutPath := m.getLayoutPath(refString)
	
	// Check if layout exists
	if _, err := os.Stat(layoutPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("image %s not found locally", refString)
	}

	// Load the layout
	p, err := layout.FromPath(layoutPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load layout from path %s: %w", layoutPath, err)
	}

	// Get the index
	index, err := p.ImageIndex()
	if err != nil {
		return nil, fmt.Errorf("failed to get image index: %w", err)
	}

	// Get the first image from the index
	manifest, err := index.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("failed to get index manifest: %w", err)
	}

	if len(manifest.Manifests) == 0 {
		return nil, fmt.Errorf("no images found in layout")
	}

	// Get the image
	img, err := p.Image(manifest.Manifests[0].Digest)
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	// Get image digest
	digest, err := img.Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to get image digest: %w", err)
	}

	// Get layer information
	layers, err := m.getImageLayers(img)
	if err != nil {
		return nil, fmt.Errorf("failed to get image layers: %w", err)
	}

	return &image{
		ref:    refString,
		digest: digest.String(),
		layers: layers,
	}, nil
}

// Unpack creates a root filesystem from a locally stored image.
func (m *Manager) Unpack(ctx context.Context, refString string, destPath string) (string, error) {
	// Get the image to validate it exists
	_, err := m.GetImage(ctx, refString)
	if err != nil {
		return "", fmt.Errorf("failed to get image %s: %w", refString, err)
	}

	// Create destination directory
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Load the layout to get the actual v1.Image
	layoutPath := m.getLayoutPath(refString)
	p, err := layout.FromPath(layoutPath)
	if err != nil {
		return "", fmt.Errorf("failed to load layout: %w", err)
	}

	index, err := p.ImageIndex()
	if err != nil {
		return "", fmt.Errorf("failed to get image index: %w", err)
	}

	manifest, err := index.IndexManifest()
	if err != nil {
		return "", fmt.Errorf("failed to get index manifest: %w", err)
	}

	v1img, err := p.Image(manifest.Manifests[0].Digest)
	if err != nil {
		return "", fmt.Errorf("failed to get v1 image: %w", err)
	}

	// Get layers
	layers, err := v1img.Layers()
	if err != nil {
		return "", fmt.Errorf("failed to get layers: %w", err)
	}

	// Extract each layer in order
	for i, layer := range layers {
		if err := m.extractLayer(layer, destPath); err != nil {
			return "", fmt.Errorf("failed to extract layer %d: %w", i, err)
		}
	}

	return destPath, nil
}

// DeleteImage removes an image from local storage.
func (m *Manager) DeleteImage(ctx context.Context, refString string) error {
	layoutPath := m.getLayoutPath(refString)
	
	if err := os.RemoveAll(layoutPath); err != nil {
		return fmt.Errorf("failed to remove image directory: %w", err)
	}
	
	return nil
}

// getLayoutPath returns the OCI layout path for a given image reference.
func (m *Manager) getLayoutPath(refString string) string {
	// Convert image reference to a safe directory name
	safeRef := strings.ReplaceAll(refString, "/", "_")
	safeRef = strings.ReplaceAll(safeRef, ":", "_")
	return filepath.Join(m.imageDir, safeRef)
}

// getImageLayers extracts layer digests from a v1.Image.
func (m *Manager) getImageLayers(img v1.Image) ([]string, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, err
	}

	var layerDigests []string
	for _, layer := range layers {
		digest, err := layer.Digest()
		if err != nil {
			return nil, err
		}
		layerDigests = append(layerDigests, digest.String())
	}

	return layerDigests, nil
}

// extractLayer extracts a single layer to the destination path.
func (m *Manager) extractLayer(layer v1.Layer, destPath string) error {
	rc, err := layer.Uncompressed()
	if err != nil {
		return fmt.Errorf("failed to get uncompressed layer: %w", err)
	}
	defer rc.Close()

	// The layer is a tar archive, extract it
	tr := tar.NewReader(rc)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// Skip whiteout files (they start with .wh.)
		if strings.Contains(header.Name, ".wh.") {
			continue
		}

		target := filepath.Join(destPath, header.Name)
		
		// Ensure the target is within destPath (security check)
		if !strings.HasPrefix(target, filepath.Clean(destPath)+string(os.PathSeparator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", target, err)
			}
		case tar.TypeReg:
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory for %s: %w", target, err)
			}

			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file %s: %w", target, err)
			}

			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("failed to write file %s: %w", target, err)
			}
			f.Close()
		case tar.TypeSymlink:
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory for %s: %w", target, err)
			}

			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("failed to create symlink %s: %w", target, err)
			}
		}
	}

	return nil
}