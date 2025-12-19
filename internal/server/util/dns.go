package util

import (
	"net"
)

// Zone suffixes.
const (
	// IPv4Arpa represents the IPv4 reverse DNS suffix.
	IPv4Arpa = ".in-addr.arpa"

	// IPv6Arpa represents the IPv6 reverse DNS suffix.
	IPv6Arpa = ".ip6.arpa"
)

// ReverseDNS takes an IPv4 or IPv6 address and returns the matching ARPA record.
func ReverseDNS(ip net.IP) (arpa string) {
	if ip == nil {
		return ""
	}

	// Deal with IPv4.
	if ip.To4() != nil {
		return uitoa(uint(ip[15])) + "." + uitoa(uint(ip[14])) + "." + uitoa(uint(ip[13])) + "." + uitoa(uint(ip[12])) + IPv4Arpa + "."
	}

	// Deal with IPv6.
	buf := make([]byte, 0, len(ip)*4+len(IPv6Arpa))

	// Add it, in reverse, to the buffer.
	for i := len(ip) - 1; i >= 0; i-- {
		v := ip[i]
		buf = append(buf, hexDigit[v&0xF],
			'.',
			hexDigit[v>>4],
			'.')
	}

	// Add the suffix.
	buf = append(buf, IPv6Arpa[1:]+"."...)
	return string(buf)
}

// Convert unsigned integer to decimal string.
func uitoa(val uint) string {
	// Avoid string allocation.
	if val == 0 {
		return "0"
	}

	// Big enough for 64bit value base 10.
	var buf [20]byte
	i := len(buf) - 1
	for val >= 10 {
		q := val / 10
		buf[i] = byte('0' + val - q*10)
		i--
		val = q
	}

	// val < 10
	buf[i] = byte('0' + val)
	return string(buf[i:])
}

const hexDigit = "0123456789abcdef"
