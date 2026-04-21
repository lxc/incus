package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pkg/sftp"

	"github.com/lxc/incus/v6/internal/i18n"
)

// pullable abstracts commands that pull files.
type pullable struct {
	flagRecursive     bool
	flagNoDereference bool
	flagFollow        bool
	flagDereference   bool
}

// preCheck performs flag validation.
func (p *pullable) preCheck(target string) error {
	// --no-dereference/-P, --follow/-H, and --dereference/-L are mutually exclusive.
	found := 0
	if p.flagNoDereference {
		found++
	}

	if p.flagFollow {
		found++
	}

	if p.flagDereference {
		found++
	}

	if found > 1 {
		return errors.New(i18n.G("--no-dereference/-P, --follow/-H, and --dereference/-L are mutually exclusive"))
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
	if isSymlink {
		srcStat, err = sftpConn.Stat(normalizedPath)
		if err != nil {
			return nil, "", err
		}
	}

	// Let’s be extra careful and check that explicit requests for directories actually point to
	// directories.
	directoryRequested := strings.HasSuffix(path, "/")
	if directoryRequested && !srcStat.IsDir() {
		return nil, "", fmt.Errorf(i18n.G("%s is not a directory"), normalizedPath)
	}

	// Here, we perform a special handling if -P is used on a directory symlink.
	if srcStat.IsDir() && !p.flagRecursive && (!isSymlink || !p.flagNoDereference) {
		return nil, "", errors.New(i18n.G("--recursive/-r is required when pulling directories"))
	}

	// Under a few conditions, return the file the link points to and not the link itself.
	if p.flagDereference || !p.flagRecursive && !p.flagNoDereference || isSymlink && p.flagFollow || directoryRequested {
		return srcStat, normalizedPath, nil
	}

	return srcLstat, normalizedPath, nil
}
