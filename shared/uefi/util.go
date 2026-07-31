package uefi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

// errUnexpectedData is a very generic error returned whenever something fails if the parser.
var errUnexpectedData = errors.New("Unexpected data")

// errNotImplemented is an error returned whenever part of a dissector is not implemented.
var errNotImplemented = errors.New("Not implemented")

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

// formatIP6 formats an IPv6 address.
func formatIP6(ip6 []byte, port ...uint16) string {
	var p uint16
	if len(port) > 0 {
		p = port[0]
	}

	ip := net.IP(ip6)
	if p == 0 {
		return fmt.Sprintf("[%s]", ip)
	}

	return fmt.Sprintf("%s:%d", ip, p)
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
