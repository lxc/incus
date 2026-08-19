package uefi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// errUnexpectedData is a very generic error returned whenever something fails if the parser.
var errUnexpectedData = errors.New("Unexpected data")

// formatGUID formats a GUID.
func formatGUID(guid []byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%x-%x", binary.LittleEndian.Uint32(guid[0:4]), binary.LittleEndian.Uint16(guid[4:6]), binary.LittleEndian.Uint16(guid[6:8]), guid[8:10], guid[10:16])
}

// formatIP formats an IPv4 address.
func formatIP(ip []byte, port ...uint16) string {
	var p uint16
	if len(port) > 0 {
		p = port[0]
	}

	if p == 0 {
		return fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], ip[3])
	}

	return fmt.Sprintf("%d.%d.%d.%d:%d", ip[0], ip[1], ip[2], ip[3], p)
}

// parseIP parses an IPv4 address.
func parseIP(ip string, allowPort ...bool) ([]byte, uint16, error) {
	var p uint64
	if len(allowPort) == 0 || allowPort[0] {
		parts := strings.SplitN(ip, ":", 2)
		if len(parts) == 2 {
			var err error
			ip = parts[0]
			p, err = strconv.ParseUint(parts[1], 10, 16)
			if err != nil {
				return nil, 0, fmt.Errorf("Couldn’t parse port %s: %w", parts[1], err)
			}
		}
	}

	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil, 0, err
	}

	if !addr.Is4() {
		return nil, 0, fmt.Errorf("%s is not a valid IPv4 address", ip)
	}

	return addr.AsSlice(), uint16(p), nil
}

// formatIP6 formats an IPv6 address.
func formatIP6(ip6 []byte, port ...uint16) string {
	var p uint16
	if len(port) > 0 {
		p = port[0]
	}

	ip := netip.AddrFrom16([16]byte(ip6)).String()
	if p == 0 {
		return ip
	}

	return fmt.Sprintf("[%s]:%d", ip, p)
}

// parseIP6 parses an IPv6 address.
func parseIP6(ip string, allowPort ...bool) ([]byte, uint16, error) {
	if strings.Contains(ip, "%") {
		return nil, 0, fmt.Errorf("%s is not a valid IPv6 address", ip)
	}

	var p uint64
	if len(allowPort) == 0 || allowPort[0] {
		parts := strings.SplitN(ip, "]:", 2)
		if len(parts) == 2 {
			var err error
			ip = parts[0] + "]"
			p, err = strconv.ParseUint(parts[1], 10, 16)
			if err != nil {
				return nil, 0, fmt.Errorf("Couldn’t parse port %s: %w", parts[1], err)
			}
		}
	}

	if strings.HasPrefix(ip, "[") && strings.HasSuffix(ip, "]") {
		ip = ip[1 : len(ip)-1]
	}

	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil, 0, err
	}

	b := addr.As16()
	return b[:], uint16(p), nil
}

// ParseAttributes parses UEFI variable attributes and formats them.
func ParseAttributes(rawAttributes uint32) []string {
	attributes := []string{}

	if rawAttributes&0x0000_0001 != 0 {
		attributes = append(attributes, "NON_VOLATILE")
	}

	if rawAttributes&0x0000_0002 != 0 {
		attributes = append(attributes, "BOOTSERVICE_ACCESS")
	}

	if rawAttributes&0x0000_0004 != 0 {
		attributes = append(attributes, "RUNTIME_ACCESS")
	}

	if rawAttributes&0x0000_0008 != 0 {
		attributes = append(attributes, "HARDWARE_ERROR_RECORD")
	}

	if rawAttributes&0x0000_0010 != 0 {
		attributes = append(attributes, "AUTHENTICATED_WRITE_ACCESS")
	}

	if rawAttributes&0x0000_0020 != 0 {
		attributes = append(attributes, "TIME_BASED_AUTHENTICATED_WRITE_ACCESS")
	}

	if rawAttributes&0x0000_0040 != 0 {
		attributes = append(attributes, "APPEND_WRITE")
	}

	if rawAttributes&0x0000_0080 != 0 {
		attributes = append(attributes, "ENHANCED_AUTHENTICATED_ACCESS")
	}

	return attributes
}

// DumpAttributes packs a list of UEFI variable attributes.
func DumpAttributes(attributes []string) uint32 {
	var rawAttributes uint32

	for _, attribute := range attributes {
		switch attribute {
		case "NON_VOLATILE":
			rawAttributes = rawAttributes | 0x0000_0001
		case "BOOTSERVICE_ACCESS":
			rawAttributes = rawAttributes | 0x0000_0002
		case "RUNTIME_ACCESS":
			rawAttributes = rawAttributes | 0x0000_0004
		case "HARDWARE_ERROR_RECORD":
			rawAttributes = rawAttributes | 0x0000_0008
		case "AUTHENTICATED_WRITE_ACCESS":
			rawAttributes = rawAttributes | 0x0000_0010
		case "TIME_BASED_AUTHENTICATED_WRITE_ACCESS":
			rawAttributes = rawAttributes | 0x0000_0020
		case "APPEND_WRITE":
			rawAttributes = rawAttributes | 0x0000_0040
		case "ENHANCED_AUTHENTICATED_ACCESS":
			rawAttributes = rawAttributes | 0x0000_0080
		}
	}

	return rawAttributes
}

// csum16 computes a 16-bit checksum.
func csum16(b []byte) uint16 {
	var sum uint16
	for i := 0; i < len(b)-1; i += 2 {
		sum += binary.LittleEndian.Uint16(b[i : i+2])
	}

	return sum
}

// ParseBootXXXX tries to parse `Boot####` and related variable names.
func ParseBootXXXX(name string) (string, uint16, bool) {
	hasFourDigits := false
	n := len(name)
	if n > 4 {
		hasFourDigits = true
		for _, c := range name[n-4:] {
			if (c < '0' || c > '9') && (c < 'A' || c > 'F') {
				hasFourDigits = false
				break
			}
		}
	}

	if !hasFourDigits {
		return name, 0, false
	}

	i, err := strconv.ParseUint(name[n-4:], 16, 16)
	if err != nil {
		return name, 0, false
	}

	return name[:n-4], uint16(i), true
}
