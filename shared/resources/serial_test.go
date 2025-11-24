//go:build linux

package resources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
)

// TestScanSerialDevice_InvalidSymlink test the scanSerialDevice function with an invalid symlink
// only by-id and by-path are valid directories.
func TestScanSerialDevice_InvalidSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "test")

	err := os.MkdirAll(testDir, 0o755)
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create a broken symlink.
	brokenSymlink := filepath.Join(testDir, "broken")
	err = os.Symlink("/non/existent/path", brokenSymlink)
	if err != nil {
		t.Fatalf("Failed to create broken symlink: %v", err)
	}

	deviceTable := make(map[string]*api.ResourcesSerialDevice)
	err = scanSerialDevice(deviceTable, testDir)
	if err != nil {
		t.Fatalf("Expected scanSerialDevice to skip broken udev symlink, got error")
	}

	// Should return error immediately when encountering broken symlinks.
	if len(deviceTable) != 0 {
		t.Errorf("Expected empty device table when error occurs, got %d devices", len(deviceTable))
	}
}

// TestBuildResourcesSerial test the buildResourcesSerial function with the minimal valid data.
func TestBuildResourcesSerial(t *testing.T) {
	deviceTable := map[string]*api.ResourcesSerialDevice{
		"ttyUSB0": {
			ID:         "ttyUSB0",
			DeviceID:   "/dev/serial/by-id/fake-id_1234-5678",
			DevicePath: "/dev/serial/by-path/pci-1234:00:14.0-usb-0:2:1.0-port0",
		},
		"ttyACM0": {
			ID:         "ttyACM0",
			DeviceID:   "/dev/serial/by-id/usb-fake-id_999-8888",
			DevicePath: "/dev/serial/by-path/pci-1234:00:14.0-usb-0:2:1.1-port0",
		},
	}

	result := buildResourcesSerial(deviceTable)

	if result.Total != 2 {
		t.Errorf("Expected Total 2, got %d", result.Total)
	}

	if len(result.Devices) != 2 {
		t.Errorf("Expected 2 devices, got %d", len(result.Devices))
	}

	// Check that devices are in the result.
	deviceMap := make(map[string]api.ResourcesSerialDevice)
	for _, dev := range result.Devices {
		deviceMap[dev.ID] = dev
	}

	dev, exists := deviceMap["ttyUSB0"]
	if exists {
		if dev.DeviceID != deviceTable["ttyUSB0"].DeviceID {
			t.Errorf("DeviceID mismatch for ttyUSB0")
		}
	} else {
		t.Error("Device ttyUSB0 not found in result")
	}

	dev, exists = deviceMap["ttyACM0"]
	if exists {
		if dev.DevicePath != deviceTable["ttyACM0"].DevicePath {
			t.Errorf("DevicePath mismatch for ttyACM0")
		}
	} else {
		t.Error("Device ttyACM0 not found in result")
	}
}

// TestBuildResourcesSerial_Empty test the buildResourcesSerial function with an empty device table.
func TestBuildResourcesSerial_Empty(t *testing.T) {
	deviceTable := make(map[string]*api.ResourcesSerialDevice)
	result := buildResourcesSerial(deviceTable)

	if result.Total != 0 {
		t.Errorf("Expected Total 0, got %d", result.Total)
	}

	if len(result.Devices) != 0 {
		t.Errorf("Expected 0 devices, got %d", len(result.Devices))
	}

	jsonData, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal JSON: %v", err)
	}

	jsonStr := string(jsonData)
	if jsonStr != `{"devices":[],"total":0}` {
		t.Errorf("Expected JSON: {\"devices\":[],\"total\":0}, got: %s", jsonStr)
	}
}
