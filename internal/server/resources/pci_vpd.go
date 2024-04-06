package resources

import (
	"sort"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
)

func vpdKnownKey(name string) bool {
	// Sanity check.
	if name == "" {
		return false
	}

	// Prefixes and fields we care about (sorted).
	prefixes := []int{'V', 'Y', 'Z'}
	fields := []string{"CC", "EC", "FC", "FN", "MN", "NA", "PN", "RM", "SN"}

	// Extract the prefix.
	prefix := int(name[0])

	// Check if starting by a valid prefix.
	pos := sort.SearchInts(prefixes, prefix)
	if pos < len(prefixes) && prefixes[pos] == prefix {
		return true
	}

	// Check if a key we're interested in.
	pos = sort.SearchStrings(fields, name)
	if pos < len(fields) && fields[pos] == name {
		return true
	}

	return false
}

func vpdReadInt(buf []byte, length int) ([]byte, int) {
	if length > len(buf) {
		return []byte{}, 0
	}

	value := 0
	for i, n := range buf[:length] {
		value += int(n) << (i * 8)
	}

	return buf[length:], value
}

func vpdReadString(buf []byte, length int) ([]byte, string) {
	if length > len(buf) {
		length = len(buf)
	}

	return buf[length:], strings.Trim(string(buf[:length]), "\x00 ")
}

func vpdReadEntries(buf []byte, length int) ([]byte, map[string]string) {
	entries := map[string]string{}
	vpdBuf := buf[:length]

	for len(vpdBuf) > 0 {
		var key string
		var entryLen int
		var value string

		// Read 2-char key.
		vpdBuf, key = vpdReadString(vpdBuf, 2)

		// Read 1 byte for the entry length.
		vpdBuf, entryLen = vpdReadInt(vpdBuf, 1)
		if entryLen == 0 {
			continue
		}

		// Read the entry value.
		vpdBuf, value = vpdReadString(vpdBuf, entryLen)
		if vpdKnownKey(key) {
			entries[key] = value
		}
	}

	return buf[length:], entries
}

func parsePCIVPD(buf []byte) api.ResourcesPCIVPD {
	vpd := api.ResourcesPCIVPD{Entries: map[string]string{}}

	for len(buf) > 0 {
		var tag int
		var length int

		// Read the 1-byte entry type.
		buf, tag = vpdReadInt(buf, 1)
		if (tag & 0x80) == 0x80 {
			// Large resource data, Read the 2-bytes entry length.

			if len(buf) < 2 {
				break
			}

			buf, length = vpdReadInt(buf, 2)
		} else {
			// Small resource data, size is in the tag itself.
			length = tag & 0x07
		}

		switch tag {
		case 0x82:
			// Product name.
			buf, vpd.ProductName = vpdReadString(buf, length)
		case 0x90:
			// Read/only VPD entries.
			fallthrough
		case 0x91:
			// Read/write VPD entries.
			var entries map[string]string
			buf, entries = vpdReadEntries(buf, length)

			// Append values since there might be multiple sections.
			for key, value := range entries {
				vpd.Entries[key] = value
			}

		default:
			// Check that we aren't past the buffer (invalid length).
			if len(buf) < length {
				break
			}

			// For other tags, just skip the value.
			buf = buf[length:]
		}
	}

	return vpd
}
