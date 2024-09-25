package util

import (
	"slices"
	"strings"
)

// IsTrue returns true if value is "true", "1", "yes" or "on" (case insensitive).
func IsTrue(value string) bool {
	return slices.Contains([]string{"true", "1", "yes", "on"}, strings.ToLower(value))
}

// IsTrueOrEmpty returns true if value is empty or if IsTrue() returns true.
func IsTrueOrEmpty(value string) bool {
	return value == "" || IsTrue(value)
}

// IsFalse returns true if value is "false", "0", "no" or "off" (case insensitive).
func IsFalse(value string) bool {
	return slices.Contains([]string{"false", "0", "no", "off"}, strings.ToLower(value))
}

// IsFalseOrEmpty returns true if value is empty or if IsFalse() returns true.
func IsFalseOrEmpty(value string) bool {
	return value == "" || IsFalse(value)
}
