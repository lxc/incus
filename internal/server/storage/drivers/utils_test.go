package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test GetVolumeMountPath.
func TestGetVolumeMountPath(t *testing.T) {
	poolName := "testpool"

	// Test custom volume.
	path := GetVolumeMountPath(poolName, VolumeTypeCustom, "testvol")
	expected := GetPoolMountPath(poolName) + "/custom/testvol"
	assert.Equal(t, expected, path)

	// Test custom volume snapshot.
	path = GetVolumeMountPath(poolName, VolumeTypeCustom, "testvol/snap1")
	expected = GetPoolMountPath(poolName) + "/custom-snapshots/testvol/snap1"
	assert.Equal(t, expected, path)

	// Test image volume.
	path = GetVolumeMountPath(poolName, VolumeTypeImage, "fingerprint")
	expected = GetPoolMountPath(poolName) + "/images/fingerprint"
	assert.Equal(t, expected, path)

	// Test container volume.
	path = GetVolumeMountPath(poolName, VolumeTypeContainer, "testvol")
	expected = GetPoolMountPath(poolName) + "/containers/testvol"
	assert.Equal(t, expected, path)

	// Test virtual-machine volume.
	path = GetVolumeMountPath(poolName, VolumeTypeVM, "testvol")
	expected = GetPoolMountPath(poolName) + "/virtual-machines/testvol"
	assert.Equal(t, expected, path)
}
