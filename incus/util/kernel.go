package util

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/lxc/incus/shared"
)

// LoadModule loads the kernel module with the given name, by invoking
// modprobe. This respects any modprobe configuration on the system.
func LoadModule(module string) error {
	if shared.PathExists(fmt.Sprintf("/sys/module/%s", module)) {
		return nil
	}

	_, err := shared.RunCommand("modprobe", "-b", module)
	return err
}

// SupportsFilesystem checks whether a given filesystem is already supported
// by the kernel. Note that if the filesystem is a module, you may need to
// load it first.
func SupportsFilesystem(filesystem string) bool {
	file, err := os.Open("/proc/filesystems")
	if err != nil {
		return false
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		entry := fields[len(fields)-1]

		if entry == filesystem {
			return true
		}
	}

	return false
}

// HugepagesPath attempts to locate the mount point of the hugepages filesystem.
func HugepagesPath() (string, error) {
	// Find the source mount of the path
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return "", err
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	matches := []string{}
	for scanner.Scan() {
		line := scanner.Text()
		cols := strings.Fields(line)

		if cols[2] == "hugetlbfs" {
			matches = append(matches, cols[1])
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("No hugetlbfs mount found, can't use hugepages")
	}

	if len(matches) > 1 {
		if shared.StringInSlice("/dev/hugepages", matches) {
			return "/dev/hugepages", nil
		}

		return "", fmt.Errorf("More than one hugetlbfs instance found and none at standard /dev/hugepages")
	}

	return matches[0], nil
}
