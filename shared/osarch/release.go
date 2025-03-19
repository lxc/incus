package osarch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/lxc/incus/v6/shared/util"
)

// GetOSRelease returns a map with Linux distribution information.
func GetOSRelease() (map[string]string, error) {
	// Handle TrueNAS.
	if util.PathExists("/usr/share/truenas") {
		content, err := os.ReadFile("/etc/version")
		if err == nil {
			return map[string]string{
				"NAME":       "TrueNAS Scale",
				"VERSION_ID": strings.TrimSpace(string(content)),
			}, nil
		}
	}

	// Add chromebook info
	if util.PathExists("/run/cros_milestone") {
		content, err := os.ReadFile("/run/cros_milestone")
		if err == nil {
			return map[string]string{
				"NAME":       "Chrome OS",
				"VERSION_ID": strings.TrimSpace(string(content)),
			}, nil
		}
	}

	// Parse OS release files.
	for _, osPath := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		osRelease, err := getOSRelease(osPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}

			return nil, err
		}

		// Check if we have what we need.
		if osRelease["IMAGE_ID"] != "" {
			return map[string]string{
				"NAME":       osRelease["IMAGE_ID"],
				"VERSION_ID": osRelease["IMAGE_VERSION"],
			}, nil
		}

		if osRelease["NAME"] != "" {
			return map[string]string{
				"NAME":       osRelease["NAME"],
				"VERSION_ID": osRelease["VERSION_ID"],
			}, nil
		}
	}

	return map[string]string{}, nil
}

func getOSRelease(filename string) (map[string]string, error) {
	osRelease := make(map[string]string)

	data, err := os.ReadFile(filename)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return osRelease, nil
		}

		return osRelease, err
	}

	for i, line := range strings.Split(string(data), "\n") {
		if len(line) == 0 {
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		tokens := strings.SplitN(line, "=", 2)
		if len(tokens) != 2 {
			return osRelease, fmt.Errorf("%s: invalid format on line %d", filename, i+1)
		}

		osRelease[tokens[0]] = strings.Trim(tokens[1], `'"`)
	}

	return osRelease, nil
}
