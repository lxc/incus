package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/v6/internal/server/instance/drivers/cfg"
)

// Test roundDownToBlockSize.
func TestRoundDownToBlockSize(t *testing.T) {
	const blockSize = 128 * 1024 * 1024

	value := roundDownToBlockSize(1073741824, blockSize)
	assert.Equal(t, int64(1073741824), value)

	value = roundDownToBlockSize(1000000000, blockSize)
	assert.Equal(t, int64(805306368), value)
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
