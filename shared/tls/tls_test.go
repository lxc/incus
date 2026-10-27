package tls

import (
	"testing"
)

func TestSortAddressesByFamily(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "IPv4 only",
			input:    []string{"192.168.1.1", "10.0.0.1"},
			expected: []string{"192.168.1.1", "10.0.0.1"},
		},
		{
			name:     "IPv6 only",
			input:    []string{"2001:db8::1", "2001:db8::2"},
			expected: []string{"2001:db8::1", "2001:db8::2"},
		},
		{
			name:     "Mixed - IPv4 first in input",
			input:    []string{"192.168.1.1", "2001:db8::1", "10.0.0.1", "2001:db8::2"},
			expected: []string{"2001:db8::1", "2001:db8::2", "192.168.1.1", "10.0.0.1"},
		},
		{
			name:     "Mixed - IPv6 first in input",
			input:    []string{"2001:db8::1", "192.168.1.1", "2001:db8::2", "10.0.0.1"},
			expected: []string{"2001:db8::1", "2001:db8::2", "192.168.1.1", "10.0.0.1"},
		},
		{
			name:     "Empty input",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "Single IPv4",
			input:    []string{"192.168.1.1"},
			expected: []string{"192.168.1.1"},
		},
		{
			name:     "Single IPv6",
			input:    []string{"2001:db8::1"},
			expected: []string{"2001:db8::1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sortAddressesByFamily(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("length mismatch: got %d, expected %d", len(result), len(tt.expected))
				return
			}

			for i, addr := range result {
				if addr != tt.expected[i] {
					t.Errorf("address mismatch at index %d: got %s, expected %s", i, addr, tt.expected[i])
				}
			}
		})
	}
}
