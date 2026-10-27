package util

import (
	"os"
	"strings"
)

// AppArmorProfile returns the current apparmor profile.
func AppArmorProfile() string {
	contents, err := os.ReadFile("/proc/self/attr/current")
	if err == nil {
		profile := strings.TrimSpace(string(contents))
		if strings.HasSuffix(profile, " (unconfined)") {
			return "unconfined"
		}

		return profile
	}

	return ""
}
