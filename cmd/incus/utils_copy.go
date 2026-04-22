package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"

	"github.com/lxc/incus/v6/internal/i18n"
)

// fileCopiable abstracts commands that pull files.
type fileCopiable struct {
	flagRecursive     bool
	flagNoDereference bool
	flagFollow        bool
	flagDereference   bool
}

// preCheck performs flag validation.
func (f *fileCopiable) preCheck() error {
	// --no-dereference/-P, --follow/-H, and --dereference/-L are mutually exclusive.
	found := 0
	if f.flagNoDereference {
		found++
	}

	if f.flagFollow {
		found++
	}

	if f.flagDereference {
		found++
	}

	if found > 1 {
		return errors.New(i18n.G("--no-dereference/-P, --follow/-H, and --dereference/-L are mutually exclusive"))
	}

	return nil
}

// pullable abstracts commands that pull files.
type pullable struct {
	fileCopiable
}

// preCheck performs flag validation.
func (p *pullable) preCheck(target string) error {
	err := p.fileCopiable.preCheck()
	if err != nil {
		return err
	}

	// Using stdout as a target implicitly sets -H if -L is not set, but fails if -P is set.
	if isStdout(target) {
		if p.flagDereference {
			return nil
		}

		if p.flagNoDereference {
			return errors.New(i18n.G("--no-dereference/-P cannot be used together with stdout as a target"))
		}

		p.flagFollow = true
	}

	return nil
}

// statFile returns the proper stat struct for the given flags, along with the normalized file name.
func (p *pullable) statFile(sftpConn *sftp.Client, path string) (os.FileInfo, string, error) {
	normalizedPath, _ := normalizePath(path)
	srcLstat, err := sftpConn.Lstat(normalizedPath)
	if err != nil {
		return nil, "", err
	}

	isSymlink := srcLstat.Mode()&os.ModeSymlink != 0
	srcStat := srcLstat
	var errSymlink error
	if isSymlink {
		// We defer dereferencing error handling, as chances are we aren’t even interested in the
		// symlink target.
		srcStat, errSymlink = sftpConn.Stat(normalizedPath)
	}

	directoryRequested := strings.HasSuffix(path, "/")
	if errSymlink == nil {
		// Let’s be extra careful and check that explicit requests for directories actually point to
		// directories.
		if directoryRequested && !srcStat.IsDir() {
			return nil, "", fmt.Errorf(i18n.G("%s is not a directory"), normalizedPath)
		}

		// Here, we perform a special handling if -P is used on a directory symlink.
		if srcStat.IsDir() && !p.flagRecursive && (!isSymlink || !p.flagNoDereference) {
			return nil, "", errors.New(i18n.G("--recursive/-r is required when pulling directories"))
		}
	}

	// Under a few conditions, return the file the link points to and not the link itself.
	if p.flagDereference || !p.flagRecursive && !p.flagNoDereference || isSymlink && p.flagFollow || directoryRequested {
		if errSymlink != nil {
			return nil, "", err
		}

		return srcStat, normalizedPath, nil
	}

	return srcLstat, normalizedPath, nil
}

// pushable abstracts commands that push files.
type pushable struct {
	fileCopiable
}

// statFile returns the proper stat struct for the given flags, along with the walkable file name.
func (p *pushable) statFile(path string) (os.FileInfo, string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}

	srcLstat, err := os.Lstat(absPath)
	if err != nil {
		return nil, "", err
	}

	isSymlink := srcLstat.Mode()&os.ModeSymlink != 0
	srcStat := srcLstat
	var errSymlink error
	if isSymlink {
		// We defer dereferencing error handling, as chances are we aren’t even interested in the
		// symlink target.
		srcStat, errSymlink = os.Stat(absPath)
	}

	if errSymlink == nil {
		// Here, we perform a special handling if -P is used on a directory symlink.
		if srcStat.IsDir() && !p.flagRecursive && (!isSymlink || !p.flagNoDereference) {
			return nil, "", errors.New(i18n.G("--recursive/-r is required when pulling directories"))
		}
	}

	// Under a few conditions, return the file the link points to and not the link itself.
	if p.flagDereference || !p.flagRecursive && !p.flagNoDereference || isSymlink && p.flagFollow || strings.HasSuffix(path, "/") {
		if errSymlink != nil {
			return nil, "", err
		}

		// This is a bit of a hack, but as we are using `filepath.Walk`, we need to point to an actual
		// directory to be able to walk it, hence the early dereferencing.
		if isSymlink && srcStat.IsDir() {
			target, err := os.Readlink(absPath)
			if err != nil {
				return nil, "", err
			}

			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(absPath), target)
			}

			absPath = target
		}

		return srcStat, absPath, nil
	}

	return srcLstat, absPath, nil
}
