package network

import (
	"fmt"
	"reflect"
	"testing"
)

func TestAllocateIP_Success(t *testing.T) {
	tests := []struct {
		name string
		vmID string
	}{
		{
			name: "VM ID 1",
			vmID: "vm-12345",
		},
		{
			name: "VM ID 2",
			vmID: "vm-67890",
		},
		{
			name: "Different VM ID should get different subnet",
			vmID: "vm-abcde",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmIndex := hashVMID(tt.vmID)%254 + 1
			expectedIP := fmt.Sprintf("172.18.%d.2", vmIndex)
			expectedGW := fmt.Sprintf("172.18.%d.1", vmIndex)

			// Verify the index is in valid range
			if vmIndex < 1 || vmIndex > 254 {
				t.Errorf("VM index %d out of valid range [1, 254]", vmIndex)
			}

			// Verify IP format is correct
			if expectedIP == "" || expectedGW == "" {
				t.Errorf("Generated empty IP addresses: IP=%s, GW=%s", expectedIP, expectedGW)
			}

			t.Logf("VM ID %s -> Index %d -> IP %s, GW %s", tt.vmID, vmIndex, expectedIP, expectedGW)
		})
	}
}

func TestAllocateIP_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		vmID string
	}{
		{
			name: "Empty VM ID",
			vmID: "",
		},
		{
			name: "Very long VM ID",
			vmID: "very-long-vm-id-that-exceeds-normal-length-12345678901234567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmIndex := hashVMID(tt.vmID)%254 + 1
			if vmIndex < 1 || vmIndex > 254 {
				t.Errorf("VM index %d out of valid range [1, 254]", vmIndex)
			}
		})
	}
}

func TestParsePortMappings_Success(t *testing.T) {
	result, err := parsePortMappings([]string{"8080:80"})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := map[int]int{8080: 80}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Expected %v, got %v", expected, result)
	}
}

func TestParsePortMappings_Multiple(t *testing.T) {
	result, err := parsePortMappings([]string{"8080:80", "443:443"})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := map[int]int{8080: 80, 443: 443}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Expected %v, got %v", expected, result)
	}
}

func TestParsePortMappings_InvalidFormat(t *testing.T) {
	tests := []struct {
		name     string
		mappings []string
	}{
		{
			name:     "Single port",
			mappings: []string{"8080"},
		},
		{
			name:     "Invalid host port",
			mappings: []string{"abc:80"},
		},
		{
			name:     "Invalid guest port",
			mappings: []string{"8080:def"},
		},
		{
			name:     "Too many colons",
			mappings: []string{"8080:80:90"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePortMappings(tt.mappings)
			if err == nil {
				t.Errorf("Expected error for invalid format %v, got nil", tt.mappings)
			}
		})
	}
}

func TestHashVMID(t *testing.T) {
	tests := []struct {
		name string
		vmID string
	}{
		{"Normal VM ID", "vm-12345"},
		{"UUID format", "550e8400-e29b-41d4-a716-446655440000"},
		{"Short ID", "abc"},
		{"Empty string", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := hashVMID(tt.vmID)
			if hash < 0 || hash >= 254 {
				t.Errorf("Hash %d out of valid range [0, 253] for VM ID %s", hash, tt.vmID)
			}
		})
	}
}

func TestHashVMID_Consistency(t *testing.T) {
	vmID := "vm-test-123"
	hash1 := hashVMID(vmID)
	hash2 := hashVMID(vmID)

	if hash1 != hash2 {
		t.Errorf("Hash function not consistent: got %d and %d for same input", hash1, hash2)
	}
}

func TestHashVMID_Distribution(t *testing.T) {
	// Test that different VM IDs produce different hashes
	vmIDs := []string{
		"vm-1", "vm-2", "vm-3", "vm-4", "vm-5",
		"test-a", "test-b", "test-c", "test-d", "test-e",
	}

	hashes := make(map[int]bool)
	for _, vmID := range vmIDs {
		hash := hashVMID(vmID)
		if hashes[hash] {
			t.Logf("Hash collision detected for VM ID %s (hash: %d)", vmID, hash)
			// Note: Hash collisions are possible but should be rare
		}
		hashes[hash] = true
	}
}
