package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/v6/shared/api"
)

// Test that an instance with a local root disk device just gets its pool property modified.
func TestInternalImportRootDevicePopulate_LocalDevice(t *testing.T) {
	instancePoolName := "test"
	localDevices := make(map[string]map[string]string, 0)

	localRootDev := map[string]string{
		"type": "disk",
		"path": "/",
		"pool": "oldpool",
		"size": "15GiB",
	}

	localDevices["root"] = localRootDev

	internalImportRootDevicePopulate(instancePoolName, localDevices, nil, nil)

	assert.Equal(t, instancePoolName, localDevices["root"]["pool"])
	assert.Equal(t, localRootDev["type"], localDevices["root"]["type"])
	assert.Equal(t, localRootDev["path"], localDevices["root"]["path"])
	assert.Equal(t, localRootDev["size"], localDevices["root"]["size"])
}

// Test that an instance with no local root disk device but has a root disk from its old expanded profile devices,
// that doesn't match the root disk in the new profiles, gets it added back as a local disk, with the pool property
// modified.
func TestInternalImportRootDevicePopulate_ExpandedDeviceProfileDeviceMismatch(t *testing.T) {
	instancePoolName := "test"
	localDevices := make(map[string]map[string]string, 0)
	expandedDevices := make(map[string]map[string]string, 0)

	expandedRootDev := map[string]string{
		"type": "disk",
		"path": "/",
		"pool": "oldpool",
		"size": "15GiB",
	}

	expandedDevices["root"] = expandedRootDev

	profiles := []api.Profile{
		{
			Name: "default",
		},
	}

	internalImportRootDevicePopulate(instancePoolName, localDevices, expandedDevices, profiles)

	assert.Equal(t, instancePoolName, localDevices["root"]["pool"])
	assert.Equal(t, expandedRootDev["type"], localDevices["root"]["type"])
	assert.Equal(t, expandedRootDev["path"], localDevices["root"]["path"])
	assert.Equal(t, expandedRootDev["size"], localDevices["root"]["size"])
}

// Test that an instance with no local root disk device but has a root disk from its old expanded profile devices,
// that matches the new profile root disk device (excluding pool name), and the new profile root disk matches the
// target pool, then no local root disk device is added, and the instance will continue to use the profile root
// disk device.
func TestInternalImportRootDevicePopulate_ExpandedDeviceProfileDeviceMatch(t *testing.T) {
	instancePoolName := "test"
	localDevices := make(map[string]map[string]string, 0)
	expandedDevices := make(map[string]map[string]string, 0)

	expandedRootDev := map[string]string{
		"type": "disk",
		"path": "/",
		"pool": "oldpool",
		"size": "15GiB",
	}

	expandedDevices["root"] = expandedRootDev

	profiles := []api.Profile{
		{
			Name: "default",
			ProfilePut: api.ProfilePut{
				Devices: make(map[string]map[string]string, 0),
			},
		},
	}

	profiles[0].Devices["root"] = map[string]string{
		"type": "disk",
		"path": "/",
		"pool": instancePoolName,
		"size": "15GiB",
	}

	internalImportRootDevicePopulate(instancePoolName, localDevices, expandedDevices, profiles)

	assert.Equal(t, len(localDevices), 0)
}

// Test that for an instance with no local root disk device, if the new profile root disk device doesn't match the
// target pool that the old expanded root device is added as a local root disk device (with the pool modified).
func TestInternalImportRootDevicePopulate_ExpandedDeviceProfileDevicePoolMismatch(t *testing.T) {
	instancePoolName := "test"
	localDevices := make(map[string]map[string]string, 0)
	expandedDevices := make(map[string]map[string]string, 0)

	expandedRootDev := map[string]string{
		"type": "disk",
		"path": "/",
		"pool": "oldpool",
		"size": "15GiB",
	}

	expandedDevices["root"] = expandedRootDev

	profiles := []api.Profile{
		{
			Name: "default",
			ProfilePut: api.ProfilePut{
				Devices: make(map[string]map[string]string, 0),
			},
		},
	}

	profiles[0].Devices["root"] = map[string]string{
		"type": "disk",
		"path": "/",
		"pool": "wrongpool",
		"size": "15GiB",
	}

	internalImportRootDevicePopulate(instancePoolName, localDevices, expandedDevices, profiles)

	assert.Equal(t, instancePoolName, localDevices["root"]["pool"])
	assert.Equal(t, expandedRootDev["type"], localDevices["root"]["type"])
	assert.Equal(t, expandedRootDev["path"], localDevices["root"]["path"])
	assert.Equal(t, expandedRootDev["size"], localDevices["root"]["size"])
}

// Test that if old config has no root disk device, and neither does new profiles, then a basic local root disk
// device is added using the target pool.
func TestInternalImportRootDevicePopulate_NoExistingRootDiskDevice(t *testing.T) {
	instancePoolName := "test"
	localDevices := make(map[string]map[string]string, 0)

	internalImportRootDevicePopulate(instancePoolName, localDevices, nil, nil)

	assert.Equal(t, instancePoolName, localDevices["root"]["pool"])
	assert.Equal(t, "disk", localDevices["root"]["type"])
	assert.Equal(t, "/", localDevices["root"]["path"])
}

// Test that if old config has no root disk device, and neither does new profiles, then a basic local root disk
// device is added using the target pool, and if there is already a local device called "root", then this new root
// disk device is added under an automatically generated name.
func TestInternalImportRootDevicePopulate_NoExistingRootDiskDeviceNameConflict(t *testing.T) {
	instancePoolName := "test"
	localDevices := make(map[string]map[string]string, 0)

	localConflictingRootDev := map[string]string{
		"type":    "nic",
		"nictype": "bridged",
		"name":    "eth0",
	}

	localDevices["root"] = localConflictingRootDev // Conflicting device called "root".

	internalImportRootDevicePopulate(instancePoolName, localDevices, nil, nil)

	assert.Equal(t, instancePoolName, localDevices["root0"]["pool"])
	assert.Equal(t, "disk", localDevices["root0"]["type"])
	assert.Equal(t, "/", localDevices["root0"]["path"])
}
