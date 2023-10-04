package util

import (
	"strings"
)

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
