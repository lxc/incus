//go:build linux && cgo

package idmap

import (
	"errors"
	"fmt"
	"os"

	"github.com/pkg/xattr"
	"golang.org/x/sys/unix"
)

// The functions below are verbatim copies of the functions found in the internal package.
// They have been copied here due to there not being a good place for
// them in the shared package and because we have a policy not to import
// anything from internal in one of our shared packages.

// getAllXattr retrieves all extended attributes associated with a file, directory or symbolic link.
func getAllXattr(path string) (map[string]string, error) {
	xattrNames, err := xattr.LList(path)
	if err != nil {
		// Some filesystems don't support llistxattr() for various reasons.
		// Interpret this as a set of no xattrs, instead of an error.
		if errors.Is(err, unix.EOPNOTSUPP) {
			return nil, nil
		}

		return nil, fmt.Errorf("Failed getting extended attributes from %q: %w", path, err)
	}

	var xattrs = make(map[string]string, len(xattrNames))
	for _, xattrName := range xattrNames {
		value, err := xattr.LGet(path, xattrName)
		if err != nil {
			return nil, fmt.Errorf("Failed getting %q extended attribute from %q: %w", xattrName, path, err)
		}

		xattrs[xattrName] = string(value)
	}

	return xattrs, nil
}

// getErrno checks if the Go error is a kernel errno.
func getErrno(err error) (errno error, iserrno bool) {
	sysErr, ok := err.(*os.SyscallError)
	if ok {
		return sysErr.Err, true
	}

	pathErr, ok := err.(*os.PathError)
	if ok {
		return pathErr.Err, true
	}

	tmpErrno, ok := err.(unix.Errno)
	if ok {
		return tmpErrno, true
	}

	return nil, false
}
