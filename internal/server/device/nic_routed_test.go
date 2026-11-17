package device

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutedFreeAddress(t *testing.T) {
	tests := []struct {
		name     string
		subnets  []string
		used     []string
		expected string
	}{
		{
			name:     "Skips the IPv4 network address",
			subnets:  []string{"192.0.2.0/24"},
			expected: "192.0.2.1",
		},
		{
			name:     "Skips used addresses",
			subnets:  []string{"192.0.2.0/24"},
			used:     []string{"192.0.2.1", "192.0.2.2"},
			expected: "192.0.2.3",
		},
		{
			name:     "Skips the IPv4 broadcast address and moves on to the next subnet",
			subnets:  []string{"192.0.2.0/30", "198.51.100.0/24"},
			used:     []string{"192.0.2.1", "192.0.2.2"},
			expected: "198.51.100.1",
		},
		{
			name:     "Uses all addresses of an IPv4 /31",
			subnets:  []string{"192.0.2.0/31"},
			expected: "192.0.2.0",
		},
		{
			name:     "Uses the address of an IPv4 /32",
			subnets:  []string{"192.0.2.0/32", "198.51.100.7/32"},
			used:     []string{"192.0.2.0"},
			expected: "198.51.100.7",
		},
		{
			name:     "Skips the IPv6 subnet-router anycast address",
			subnets:  []string{"2001:db8::/64"},
			expected: "2001:db8::1",
		},
		{
			name:    "Fails when all addresses are used",
			subnets: []string{"192.0.2.0/30"},
			used:    []string{"192.0.2.1", "192.0.2.2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subnets := []netip.Prefix{}
			for _, s := range tt.subnets {
				subnets = append(subnets, netip.MustParsePrefix(s))
			}

			used := map[netip.Addr]bool{}
			for _, s := range tt.used {
				used[netip.MustParseAddr(s)] = true
			}

			addr, err := routedFreeAddress(subnets, used)
			if tt.expected == "" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, addr.String())
		})
	}
}
