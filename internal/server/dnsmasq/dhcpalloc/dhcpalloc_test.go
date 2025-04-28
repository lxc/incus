package dhcpalloc

import (
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/v6/internal/iprange"
)

func Test_DHCPValidIP(t *testing.T) {
	tests := []struct {
		name     string
		subnet   string
		ranges   []string
		ip       string
		expected bool
	}{
		{name: "in subnet, no ranges", subnet: "192.168.0.0/16", ranges: nil, ip: "192.168.0.2", expected: true},
		{name: "not in subnet, no ranges", subnet: "192.168.0.0/16", ranges: nil, ip: "10.10.0.2", expected: false},
		{name: "in subnet, in given range", subnet: "192.168.0.0/16", ranges: []string{"192.168.0.0-192.168.0.10"}, ip: "192.168.0.2", expected: true},
		{name: "in subnet, not in given range", subnet: "192.168.0.0/16", ranges: []string{"192.168.0.0-192.168.0.10"}, ip: "192.168.0.12", expected: false},
		{name: "not in subnet, in given range", subnet: "192.168.0.0/16", ranges: []string{"10.10.0.0-10.10.0.10"}, ip: "10.10.0.2", expected: false},
		{name: "not in subnet, not in given range", subnet: "192.168.0.0/16", ranges: []string{"192.168.0.0-192.168.0.10"}, ip: "10.10.0.12", expected: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// arrange
			_, subnet, _ := net.ParseCIDR(test.subnet)
			var ranges []iprange.Range
			for _, rangesString := range test.ranges {
				rangeIps := strings.Split(rangesString, "-")

				ranges = append(ranges,
					iprange.Range{
						Start: net.ParseIP(rangeIps[0]),
						End:   net.ParseIP(rangeIps[1]),
					})
			}

			ip := net.ParseIP(test.ip)

			// act
			isValidIP := DHCPValidIP(subnet, ranges, ip)

			// assert
			assert.Equal(t, test.expected, isValidIP)
		})
	}
}
