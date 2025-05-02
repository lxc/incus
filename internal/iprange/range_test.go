package iprange_test

import (
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/v6/internal/iprange"
)

// parseRange is a custom parse function to not depend on other packages.
func parseRange(rangeString string) iprange.Range {
	ips := strings.Split(rangeString, "-")

	start := net.ParseIP(ips[0])

	var end net.IP
	if len(ips) == 2 {
		end = net.ParseIP(ips[1])
	}

	return iprange.Range{
		Start: start,
		End:   end,
	}
}

func TestRange_ContainsIP(t *testing.T) {
	type containsIPTest struct {
		name        string
		rangeString string // a string of format <ip>-<ip> optionally just an <ip>
		testIP      string
		expected    bool
	}

	tests := []containsIPTest{
		{
			name:        "ip below range",
			rangeString: "10.10.0.0-10.16.0.0",
			testIP:      "10.0.0.1",
			expected:    false,
		},
		{
			name:        "ip is lower bound",
			rangeString: "10.10.0.0-10.16.0.0",
			testIP:      "10.10.0.0",
			expected:    true,
		},
		{
			name:        "ip in range",
			rangeString: "10.10.0.0-10.16.0.0",
			testIP:      "10.12.59.1",
			expected:    true,
		},
		{
			name:        "ip is upper bound",
			rangeString: "10.10.0.0-10.16.0.0",
			testIP:      "10.16.0.0",
			expected:    true,
		},
		{
			name:        "ip above range",
			rangeString: "10.10.0.0-10.16.0.0",
			testIP:      "10.23.59.1",
			expected:    false,
		},
		{
			name:        "range has no end and ip is below range",
			rangeString: "10.10.0.1",
			testIP:      "10.2.59.1",
			expected:    false,
		},
		{
			name:        "range has no end and ip is in range",
			rangeString: "10.10.0.1",
			testIP:      "10.10.0.1",
			expected:    true,
		},
		{
			name:        "range has no end and ip is above range",
			rangeString: "10.10.0.1",
			testIP:      "10.23.59.1",
			expected:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// arrange
			r := parseRange(test.rangeString)
			testIP := net.ParseIP(test.testIP)

			// act
			isContained := r.ContainsIP(testIP)

			// assert
			assert.Equal(t, test.expected, isContained)
		})
	}
}

func TestRange_String(t *testing.T) {
	type stringTest struct {
		name        string
		rangeString string // a string of format <ip>-<ip> optionally just an <ip>
		expected    string
	}

	tests := []stringTest{
		{
			name:        "start and end",
			rangeString: "10.10.0.0-10.16.0.5",
			expected:    "10.10.0.0-10.16.0.5",
		},
		{
			name:        "start only",
			rangeString: "10.10.0.0",
			expected:    "10.10.0.0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// arrange
			r := parseRange(test.rangeString)

			// act
			s := r.String()

			// assert
			assert.Equal(t, test.expected, s)
		})
	}
}
