package logging

import (
	"slices"
	"strings"
)

// sliceFromString converts a comma-separated string into a slice of strings.
func sliceFromString(input string) []string {
	parts := strings.Split(input, ",")
	result := []string{}
	for _, v := range parts {
		part := strings.TrimSpace(v)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// contains checks if the target string is part of the slice.
func contains(slice []string, target string) bool {
	return slices.Contains(slice, target)
}

// hasAnyPrefix checks if any string in the prefixes slice is a prefix of s.
// It returns true as soon as a match is found, otherwise false.
func hasAnyPrefix(prefixes []string, s string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
