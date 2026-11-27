package osarch

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/lxc/incus/v7/shared/util"
)

// releaseFileMaxSize bounds the release files, which may be an instance's when read from a helper.
const releaseFileMaxSize = 64 * 1024

// readReleaseFile reads at most releaseFileMaxSize bytes of a release file.
func readReleaseFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	return io.ReadAll(io.LimitReader(f, releaseFileMaxSize))
}

// GetOSRelease returns a map with Linux distribution information.
func GetOSRelease() (map[string]string, error) {
	// Handle TrueNAS.
	if util.PathExists("/usr/share/truenas") {
		content, err := readReleaseFile("/etc/version")
		if err == nil {
			return map[string]string{
				"NAME":       "TrueNAS Scale",
				"VERSION_ID": strings.TrimSpace(string(content)),
			}, nil
		}
	}

	// Add chromebook info
	if util.PathExists("/run/cros_milestone") {
		content, err := readReleaseFile("/run/cros_milestone")
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

	data, err := readReleaseFile(filename)
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
