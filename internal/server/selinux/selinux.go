//go:build linux && cgo && !agent

package selinux

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	goselinux "github.com/opencontainers/selinux/go-selinux"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/shared/logger"
)

// LabelTree recursively labels all entries under path with the given SELinux label without crossing filesystem boundaries.
func LabelTree(path string, label string, skipPath string) error {
	var rootStat unix.Stat_t

	if path == "" {
		return fmt.Errorf("Path is empty: %q", path)
	}

	if label == "" {
		return fmt.Errorf("Label is empty: %q", label)
	}

	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("Failed to resolve rootfs symlink for SELinux labeling: %q: %w", path, err)
	}

	err = unix.Lstat(target, &rootStat)
	if err != nil {
		return fmt.Errorf("Failed to stat SELinux label root %q: %w", target, err)
	}

	rootDev := rootStat.Dev

	logger.Debug("SELinux: Labeling instance path", logger.Ctx{"path": target, "skipPath": skipPath})

	return filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("SELinux label tree walk error: %q: %w", p, err)
		}

		if skipPath != "" {
			rel, _ := filepath.Rel(skipPath, p)
			if rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
				if d.IsDir() {
					return fs.SkipDir
				}

				return nil
			}
		}

		// Don't cross filesystem boundaries.
		var dirStat unix.Stat_t
		statErr := unix.Lstat(p, &dirStat)
		if statErr != nil {
			return fmt.Errorf("Failed to stat %q: %w", p, statErr)
		}

		if dirStat.Dev != rootDev {
			if d.IsDir() {
				return fs.SkipDir
			}

			return nil
		}

		labelErr := goselinux.LsetFileLabel(p, label)
		if labelErr != nil {
			return fmt.Errorf("Failed to set SELinux label: %w", labelErr)
		}

		return nil
	})
}
