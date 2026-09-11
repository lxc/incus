package util

import (
	"os"
	"strings"
)

// AppArmorProfile returns the current apparmor profile.
func AppArmorProfile() string {
	contents, err := os.ReadFile("/proc/self/attr/current")
	if err == nil {
		return appArmorProfileName(string(contents))
	}

	return ""
}

func appArmorProfileName(profile string) string {
	profile = strings.TrimSpace(profile)
	if strings.HasSuffix(profile, " (unconfined)") {
		return "unconfined"
	}

	return profile
}
