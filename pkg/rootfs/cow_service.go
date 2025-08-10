package rootfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	
	"micropod/pkg/cow"
	"micropod/pkg/image"
)

type CowService struct {
	imageManager *image.Manager
	cowManager   *cow.Manager
	cowDir       string // 保存 CoW 目录路径用于清理
	baseDevices  map[string]*cow.BaseDevice
	snapshots    map[string]*cow.SnapshotDevice
	mutex        sync.RWMutex
}

type CowRootFS struct {
	DevicePath string
	VMId       string
	ImageRef   string
}

func NewCowService(imageDir, deviceDir, cowDir string) (*CowService, error) {
	imageManager, err := image.NewManager(imageDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create image manager: %w", err)
	}
	
	cowManager, err := cow.NewManager(deviceDir, cowDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create cow manager: %w", err)
	}
	
	return &CowService{
		imageManager: imageManager,
		cowManager:   cowManager,
		cowDir:       cowDir,
		baseDevices:  make(map[string]*cow.BaseDevice),
		snapshots:    make(map[string]*cow.SnapshotDevice),
	}, nil
}

func (s *CowService) CreateRootFS(ctx context.Context, imageRef, vmID string) (*CowRootFS, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	// Ensure image is pulled
	_, err := s.imageManager.PullImage(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to pull image: %w", err)
	}
	
	// Get or create base device
	baseDevice, err := s.getOrCreateBaseDevice(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get base device: %w", err)
	}
	
	// Create snapshot device for this VM
	snapshotDevice, err := s.cowManager.CreateSnapshotDevice(vmID, baseDevice)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot device: %w", err)
	}
	
	s.snapshots[vmID] = snapshotDevice
	
	return &CowRootFS{
		DevicePath: snapshotDevice.DevicePath,
		VMId:       vmID,
		ImageRef:   imageRef,
	}, nil
}

func (s *CowService) RemoveRootFS(vmID string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	snapshot, exists := s.snapshots[vmID]
	if !exists {
		// 如果在内存中找不到快照设备，尝试直接清理可能存在的设备
		fmt.Printf("Warning: snapshot device for VM %s not found in memory, attempting cleanup anyway\n", vmID)
		return s.cleanupOrphanedDevice(vmID)
	}
	
	if err := s.cowManager.RemoveSnapshotDevice(snapshot); err != nil {
		return fmt.Errorf("failed to remove snapshot device: %w", err)
	}
	
	delete(s.snapshots, vmID)
	return nil
}

// cleanupOrphanedDevice 尝试清理可能遗留的设备
func (s *CowService) cleanupOrphanedDevice(vmID string) error {
	// 构造可能的设备名称和路径
	snapshotName := fmt.Sprintf("micropod-vm-%s", vmID)
	cowPath := filepath.Join(s.cowDir, fmt.Sprintf("%s.cow", vmID))
	
	fmt.Printf("Attempting to cleanup orphaned device: %s\n", snapshotName)
	fmt.Printf("CoW file path: %s\n", cowPath)
	
	// 尝试移除设备映射（如果存在）
	if err := s.cowManager.RemoveDeviceMapping(snapshotName); err != nil {
		fmt.Printf("Note: failed to remove device mapping %s: %v (may not exist)\n", snapshotName, err)
	} else {
		fmt.Printf("Successfully removed device mapping: %s\n", snapshotName)
	}
	
	// 清理 CoW 文件（如果存在）
	if err := os.Remove(cowPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("Warning: failed to remove CoW file %s: %v\n", cowPath, err)
	} else if err == nil {
		fmt.Printf("Successfully removed CoW file: %s\n", cowPath)
	}
	
	return nil
}

func (s *CowService) CleanupUnusedBaseDevices() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	// Count references to base devices
	baseRefs := make(map[string]int)
	for _, snapshot := range s.snapshots {
		baseRefs[snapshot.BaseDevice]++
	}
	
	// Remove unused base devices
	for imageRef, baseDevice := range s.baseDevices {
		if baseRefs[baseDevice.Name] == 0 {
			if err := s.cowManager.RemoveBaseDevice(baseDevice); err != nil {
				fmt.Printf("Warning: failed to remove base device %s: %v\n", baseDevice.Name, err)
				continue
			}
			delete(s.baseDevices, imageRef)
		}
	}
	
	return nil
}

func (s *CowService) GetRootFSInfo(vmID string) (*CowRootFS, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	
	snapshot, exists := s.snapshots[vmID]
	if !exists {
		return nil, fmt.Errorf("snapshot device for VM %s not found", vmID)
	}
	
	// Find image reference for this base device
	var imageRef string
	for ref, baseDevice := range s.baseDevices {
		if baseDevice.Name == snapshot.BaseDevice {
			imageRef = ref
			break
		}
	}
	
	return &CowRootFS{
		DevicePath: snapshot.DevicePath,
		VMId:       vmID,
		ImageRef:   imageRef,
	}, nil
}

func (s *CowService) getOrCreateBaseDevice(ctx context.Context, imageRef string) (*cow.BaseDevice, error) {
	// Check if base device already exists
	if baseDevice, exists := s.baseDevices[imageRef]; exists {
		return baseDevice, nil
	}
	
	// Create base image if it doesn't exist
	baseImagePath, err := s.imageManager.CreateBaseImage(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to create base image: %w", err)
	}
	
	// Create base device
	baseDevice, err := s.cowManager.CreateBaseDevice(imageRef, baseImagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create base device: %w", err)
	}
	
	s.baseDevices[imageRef] = baseDevice
	return baseDevice, nil
}

// ListActiveRootFS returns all active rootfs devices
func (s *CowService) ListActiveRootFS() []CowRootFS {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	
	var rootfsList []CowRootFS
	for vmID, snapshot := range s.snapshots {
		// Find image reference for this base device
		var imageRef string
		for ref, baseDevice := range s.baseDevices {
			if baseDevice.Name == snapshot.BaseDevice {
				imageRef = ref
				break
			}
		}
		
		rootfsList = append(rootfsList, CowRootFS{
			DevicePath: snapshot.DevicePath,
			VMId:       vmID,
			ImageRef:   imageRef,
		})
	}
	
	return rootfsList
}

// Cleanup removes all devices and cleans up resources
func (s *CowService) Cleanup() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	// Remove all snapshot devices first
	for vmID, snapshot := range s.snapshots {
		if err := s.cowManager.RemoveSnapshotDevice(snapshot); err != nil {
			fmt.Printf("Warning: failed to remove snapshot device for VM %s: %v\n", vmID, err)
		}
	}
	s.snapshots = make(map[string]*cow.SnapshotDevice)
	
	// Remove all base devices
	for imageRef, baseDevice := range s.baseDevices {
		if err := s.cowManager.RemoveBaseDevice(baseDevice); err != nil {
			fmt.Printf("Warning: failed to remove base device for image %s: %v\n", imageRef, err)
		}
	}
	s.baseDevices = make(map[string]*cow.BaseDevice)
	
	return nil
}