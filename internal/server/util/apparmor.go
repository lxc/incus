package util

import (
	"os"
	"strings"
)

// AppArmorProfile returns the current apparmor profile.
func AppArmorProfile() string {
	contents, err := os.ReadFile("/proc/self/attr/current")
	if err == nil {
		return strings.TrimSpace(string(contents))
	}

	return ""
}
