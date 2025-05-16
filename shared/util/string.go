package util

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ParseUint32Range parses a uint32 range in the form "number" or "start-end".
// Returns the start number and the size of the range.
func ParseUint32Range(value string) (uint32, uint32, error) {
	rangeParts := strings.SplitN(value, "-", 2)
	rangeLen := len(rangeParts)
	if rangeLen != 1 && rangeLen != 2 {
		return 0, 0, errors.New("Range must contain a single number or start and end numbers")
	}

	startNum, err := strconv.ParseUint(rangeParts[0], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("Invalid number %q", value)
	}

	var rangeSize uint32 = 1

	if rangeLen == 2 {
		endNum, err := strconv.ParseUint(rangeParts[1], 10, 32)
		if err != nil {
			return 0, 0, fmt.Errorf("Invalid end number %q", value)
		}

		if startNum >= endNum {
			return 0, 0, fmt.Errorf("Start number %d must be lower than end number %d", startNum, endNum)
		}

		rangeSize += uint32(endNum) - uint32(startNum)
	}

	return uint32(startNum), rangeSize, nil
}

// SplitNTrimSpace returns result of strings.SplitN() and then strings.TrimSpace() on each element.
// Accepts nilIfEmpty argument which if true, will return nil slice if s is empty (after trimming space).
func SplitNTrimSpace(s string, sep string, n int, nilIfEmpty bool) []string {
	if nilIfEmpty && strings.TrimSpace(s) == "" {
		return nil
	}

	parts := strings.SplitN(s, sep, n)

	for i, v := range parts {
		parts[i] = strings.TrimSpace(v)
	}

	return parts
}

// StringHasPrefix returns true if value has one of the supplied prefixes.
func StringHasPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// StringPrefixInSlice returns true if any element in the list has the given prefix.
func StringPrefixInSlice(key string, list []string) bool {
	for _, entry := range list {
		if strings.HasPrefix(entry, key) {
			return true
		}
	}

	return false
}
