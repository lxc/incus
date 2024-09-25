package drivers

import (
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_portRangesFromSlice(t *testing.T) {
	tests := []struct {
		name     string
		ports    []uint64
		expected [][2]uint64
	}{
		{
			name:     "Single port",
			ports:    []uint64{80},
			expected: [][2]uint64{{80, 1}},
		},
		{
			name:     "Single range",
			ports:    []uint64{80, 81, 82, 83},
			expected: [][2]uint64{{80, 4}},
		},
		{
			name:  "Multiple (single) ports",
			ports: []uint64{80, 90, 100},
			expected: [][2]uint64{
				{80, 1},
				{90, 1},
				{100, 1},
			},
		},
		{
			name:  "Multiple ranges",
			ports: []uint64{80, 81, 82, 90, 91, 92, 100, 101, 102},
			expected: [][2]uint64{
				{80, 3},
				{90, 3},
				{100, 3},
			},
		},
		{
			name:  "Mixed ranges and single ports",
			ports: []uint64{80, 81, 82, 87, 90, 91, 92, 88, 100, 101, 102, 89},
			expected: [][2]uint64{
				{80, 3},
				{87, 1},
				{90, 3},
				{88, 1},
				{100, 3},
				{89, 1},
			},
		},
	}

	for i, tt := range tests {
		log.Printf("Running test #%d: %s", i, tt.name)
		ranges := portRangesFromSlice(tt.ports)
		assert.ElementsMatch(t, ranges, tt.expected)
	}
}

func Test_getOptimisedSNATRanges(t *testing.T) {
	tests := []struct {
		name     string
		forward  *AddressForward
		expected map[[2]uint64][2]uint64
	}{
		{
			name: "Equal ports (single)",
			forward: &AddressForward{
				ListenPorts: []uint64{80},
				TargetPorts: []uint64{80},
			},
			expected: map[[2]uint64][2]uint64{
				{80, 1}: {80, 1},
			},
		},
		{
			name: "Equal ports (range)",
			forward: &AddressForward{
				ListenPorts: []uint64{80, 81, 82, 83},
				TargetPorts: []uint64{80, 81, 82, 83},
			},
			expected: map[[2]uint64][2]uint64{
				{80, 4}: {80, 4},
			},
		},
		{
			name: "Unequal ports (single)",
			forward: &AddressForward{
				ListenPorts: []uint64{80},
				TargetPorts: []uint64{8080},
			},
			expected: map[[2]uint64][2]uint64{
				{80, 1}: {8080, 1},
			},
		},
		{
			name: "Unequal ports (range)",
			forward: &AddressForward{
				ListenPorts: []uint64{80, 81, 82, 83},
				TargetPorts: []uint64{90, 91, 92, 93},
			},
			expected: map[[2]uint64][2]uint64{
				{80, 1}: {90, 1},
				{81, 1}: {91, 1},
				{82, 1}: {92, 1},
				{83, 1}: {93, 1},
			},
		},
		{
			name: "Unequal ports (range)",
			forward: &AddressForward{
				ListenPorts: []uint64{80, 81, 82, 83},
				TargetPorts: []uint64{90, 91, 92, 93},
			},
			expected: map[[2]uint64][2]uint64{
				{80, 1}: {90, 1},
				{81, 1}: {91, 1},
				{82, 1}: {92, 1},
				{83, 1}: {93, 1},
			},
		},
		{
			name: "Mixed ranges and single ports",
			forward: &AddressForward{
				ListenPorts: []uint64{80, 81, 82, 83, 200, 201, 202, 203, 100, 101},
				TargetPorts: []uint64{80, 81, 110, 120, 200, 201, 202, 203, 100, 101},
			},
			expected: map[[2]uint64][2]uint64{
				{80, 2}:  {80, 2},
				{82, 1}:  {110, 1},
				{83, 1}:  {120, 1},
				{200, 4}: {200, 4},
				{100, 2}: {100, 2},
			},
		},
	}

	for _, tt := range tests {
		actual := getOptimisedDNATRanges(tt.forward)
		assert.Equal(t, tt.expected, actual)
	}
}
