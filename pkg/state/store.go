package state

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type VM struct {
	ID             string    `json:"id"`
	ImageName      string    `json:"imageName"`
	State          string    `json:"state"`
	FirecrackerPid int       `json:"firecrackerPid"`
	VMSocketPath   string    `json:"vmSocketPath"`
	RootfsPath     string    `json:"rootfsPath"`
	KernelPath     string    `json:"kernelPath"`
	CreatedAt      time.Time `json:"createdAt"`
}

type Store struct {
	filePath string
	mutex    sync.RWMutex
}

func NewStore(filepath string) (*Store, error) {
	
	store := &Store{
		filePath: filepath,
	}
	return store, nil
}

func (s *Store) AddVM(vm VM) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	vms, err := s.loadVMs()
	if err != nil {
		return fmt.Errorf("failed to load VMs: %w", err)
	}
	
	vms = append(vms, vm)
	
	if err := s.saveVMs(vms); err != nil {
		return fmt.Errorf("failed to save VMs: %w", err)
	}
	
	return nil
}

func (s *Store) GetVM(id string) (*VM, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	
	vms, err := s.loadVMs()
	if err != nil {
		return nil, fmt.Errorf("failed to load VMs: %w", err)
	}
	
	for _, vm := range vms {
		if vm.ID == id {
			return &vm, nil
		}
	}
	
	return nil, fmt.Errorf("VM with ID %s not found", id)
}

func (s *Store) RemoveVM(id string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	vms, err := s.loadVMs()
	if err != nil {
		return fmt.Errorf("failed to load VMs: %w", err)
	}
	
	var updatedVMs []VM
	found := false
	for _, vm := range vms {
		if vm.ID != id {
			updatedVMs = append(updatedVMs, vm)
		} else {
			found = true
		}
	}
	
	if !found {
		return fmt.Errorf("VM with ID %s not found", id)
	}
	
	if err := s.saveVMs(updatedVMs); err != nil {
		return fmt.Errorf("failed to save VMs: %w", err)
	}
	
	return nil
}

func (s *Store) ListVMs() ([]VM, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	
	return s.loadVMs()
}

func (s *Store) UpdateVMState(id string, state string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	vms, err := s.loadVMs()
	if err != nil {
		return fmt.Errorf("failed to load VMs: %w", err)
	}
	
	found := false
	for i, vm := range vms {
		if vm.ID == id {
			vms[i].State = state
			found = true
			break
		}
	}
	
	if !found {
		return fmt.Errorf("VM with ID %s not found", id)
	}
	
	if err := s.saveVMs(vms); err != nil {
		return fmt.Errorf("failed to save VMs: %w", err)
	}
	
	return nil
}

func (s *Store) loadVMs() ([]VM, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}
	
	var vms []VM
	if len(data) > 0 {
		if err := json.Unmarshal(data, &vms); err != nil {
			return nil, fmt.Errorf("failed to unmarshal state file: %w", err)
		}
	}
	
	return vms, nil
}

func (s *Store) saveVMs(vms []VM) error {
	data, err := json.MarshalIndent(vms, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal VMs: %w", err)
	}
	
	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}
	
	return nil
}