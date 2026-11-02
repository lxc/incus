//go:build linux

package linux

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// MountWriters lists the processes holding files under path open for writing.
func MountWriters(path string) []string {
	writers := []string{}
	seen := map[string]bool{}

	// The kernel reports the resolved path of open files.
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = resolved
	}

	fds, _ := filepath.Glob("/proc/[0-9]*/fd/*")
	for _, fd := range fds {
		target, err := os.Readlink(fd)
		if err != nil || (target != path && !strings.HasPrefix(target, path+"/")) {
			continue
		}

		pidDir := filepath.Dir(filepath.Dir(fd))
		info, err := os.ReadFile(filepath.Join(pidDir, "fdinfo", filepath.Base(fd)))
		if err != nil {
			continue
		}

		// The flags line holds the open(2) flags in octal.
		writable := false
		for line := range strings.SplitSeq(string(info), "\n") {
			value, found := strings.CutPrefix(line, "flags:")
			if !found {
				continue
			}

			flags, err := strconv.ParseUint(strings.TrimSpace(value), 8, 64)
			writable = err == nil && flags&unix.O_ACCMODE != unix.O_RDONLY
		}

		if !writable {
			continue
		}

		comm, _ := os.ReadFile(filepath.Join(pidDir, "comm"))
		entry := fmt.Sprintf("%s[%s] %s", strings.TrimSpace(string(comm)), filepath.Base(pidDir), target)
		if !seen[entry] {
			seen[entry] = true
			writers = append(writers, entry)
		}
	}

	return writers
}
