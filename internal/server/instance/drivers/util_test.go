package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/instance/drivers/cfg"
)

// Test roundUpToBlockSize.
func TestRoundToBlockSize(t *testing.T) {
	const blockSize int64 = 128 * 1024 * 1024 // 128MiB

	// Already block-aligned values are returned unchanged.
	value := roundUpToBlockSize(1073741824, blockSize)
	require.Equal(t, int64(1073741824), value)

	// Unaligned values round up to the next block.
	value = roundUpToBlockSize(1000000000, blockSize)
	require.Equal(t, int64(1073741824), value)

	// Values smaller than a single block round up to one block rather than 0.
	value = roundUpToBlockSize(1, blockSize)
	require.Equal(t, blockSize, value)

	// Values in the second block round up to two blocks rather than 0.
	value = roundUpToBlockSize(blockSize+1, blockSize)
	require.Equal(t, 2*blockSize, value)

	// Zero stays zero.
	value = roundUpToBlockSize(0, blockSize)
	require.Equal(t, int64(0), value)
}

// Test memoryConfigSectionToMap.
func TestMemoryConfigSectionToMap(t *testing.T) {
	result := memoryConfigSectionToMap(
		&cfg.Section{
			Name: "object \"mem0\"",
			Entries: map[string]string{
				"size":         "1024M",
				"host-nodes.0": "0",
				"host-nodes.1": "1",
				"policy":       "bind",
				"share":        "on",
			},
		},
	)

	expected := map[string]any{
		"size":       int64(1073741824),
		"host-nodes": []int{0, 1},
		"policy":     "bind",
		"share":      true,
	}

	assert.Equal(t, expected["size"], result["size"])
	assert.ElementsMatch(t, expected["host-nodes"], result["host-nodes"])
	assert.Equal(t, expected["policy"], result["policy"])
	assert.Equal(t, expected["share"], result["share"])
}

// Test extractTraiingNumber.
func TestExtractTrailingNumber(t *testing.T) {
	value, _ := extractTrailingNumber("mem0", "mem")
	assert.Equal(t, 0, value)

	value, _ = extractTrailingNumber("mem34", "mem")
	assert.Equal(t, 34, value)

	value, _ = extractTrailingNumber("dimm1", "dimm")
	assert.Equal(t, 1, value)

	expectedErr := "Prefix mem not found in dimm1"
	_, err := extractTrailingNumber("dimm1", "mem")
	if err.Error() != expectedErr {
		t.Errorf("unexpected error message: got %q, want %q", err.Error(), expectedErr)
	}
}

// Test qcow2L2CacheSize.
func TestQcow2L2CacheSize(t *testing.T) {
	const cluster int64 = 64 * 1024
	const mib int64 = 1024 * 1024
	const gib int64 = 1024 * mib

	// A small file only needs its own tables (8 bytes per cluster).
	require.Equal(t, 100*gib/cluster*8, qcow2L2CacheSize(100*gib, cluster, false, gib))

	// A large file on a small VM stays at QEMU's 32MiB default.
	require.Equal(t, 32*mib, qcow2L2CacheSize(1024*gib, cluster, false, gib))

	// The cap grows with the memory limit.
	require.Equal(t, 64*mib, qcow2L2CacheSize(1024*gib, cluster, false, 2*gib))
	require.Equal(t, 128*mib, qcow2L2CacheSize(1024*gib, cluster, false, 16*gib))

	// Extended L2 entries take 16 bytes per cluster.
	require.Equal(t, 256*mib, qcow2L2CacheSize(1024*gib, cluster, true, 16*gib))

	// Sizes round up to whole L2 tables.
	require.Equal(t, 3*cluster, qcow2L2CacheSize(gib+1, cluster, false, gib))

	// Unknown geometry gives no size.
	require.Equal(t, int64(0), qcow2L2CacheSize(gib, 0, false, gib))
}
