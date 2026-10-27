package osinfo

import (
	"fmt"
	"slices"
	"strings"
)

// Supported windows versions, with matching virtio-win abbreviations.
var windowsVersions = map[string]string{
	"XP":             "xp",
	"7":              "w7",
	"8":              "w8",
	"8.1":            "w8.1",
	"10":             "w10",
	"11":             "w11",
	"Server 2003":    "2k3",
	"Server 2008":    "2k8",
	"Server 2008 R2": "2k8R2",
	"Server 2012":    "2k12",
	"Server 2012 R2": "2k12R2",
	"Server 2016":    "2k16",
	"Server 2019":    "2k19",
	"Server 2022":    "2k22",
	"Server 2025":    "2k25",
}

var windowsAliases = map[string][]string{
	"Server 2008 R2": {"Server R2 2008"},
	"Server 2012 R2": {"Server R2 2012"},
}

// ToWindowsVersion returns the windows version for the given OS description.
func ToWindowsVersion(imageRelease string) (string, error) {
	imageRelease = strings.ToLower(imageRelease)
	for v := range windowsVersions {
		compare := strings.ToLower(v)
		if strings.Contains(compare, " ") {
			if strings.Contains(imageRelease, " r2") && !strings.HasSuffix(compare, " r2") {
				continue
			}

			if strings.Contains(imageRelease, compare) {
				return v, nil
			}

			aliases, ok := windowsAliases[v]
			if ok {
				for _, alias := range aliases {
					if strings.Contains(imageRelease, strings.ToLower(alias)) {
						return v, nil
					}
				}
			}
		} else {
			if slices.Contains(strings.Split(imageRelease, " "), compare) {
				return v, nil
			}
		}
	}

	return "", fmt.Errorf("Windows version %q is unknown or unsupported", imageRelease)
}

// MapWindowsVersionToAbbrev takes a full version string and returns the abbreviation used by virtio-win drivers.
func MapWindowsVersionToAbbrev(version string) (string, error) {
	code, ok := windowsVersions[version]
	if !ok {
		return "", fmt.Errorf("Invalid Windows version %q", version)
	}

	return code, nil
}

// ValidateWindowsVersion checks if the given Windows version is valid.
func ValidateWindowsVersion(v string) error {
	_, ok := windowsVersions[v]
	if !ok {
		return fmt.Errorf("Unknown windows version %q", v)
	}

	return nil
}
