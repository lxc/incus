package util

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	internalIO "github.com/lxc/incus/v6/internal/io"
	"github.com/lxc/incus/v6/shared/util"
)

// FileMove tries to move a file by using os.Rename,
// if that fails it tries to copy the file and remove the source.
func FileMove(oldPath string, newPath string) error {
	err := os.Rename(oldPath, newPath)
	if err == nil {
		return nil
	}

	err = FileCopy(oldPath, newPath)
	if err != nil {
		return err
	}

	_ = os.Remove(oldPath)

	return nil
}

// FileCopy copies a file, overwriting the target if it exists.
func FileCopy(source string, dest string) error {
	fi, err := os.Lstat(source)
	if err != nil {
		return err
	}

	_, uid, gid := internalIO.GetOwnerMode(fi)

	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}

		if util.PathExists(dest) {
			err = os.Remove(dest)
			if err != nil {
				return err
			}
		}

		err = os.Symlink(target, dest)
		if err != nil {
			return err
		}

		if runtime.GOOS != "windows" {
			return os.Lchown(dest, uid, gid)
		}

		return nil
	}

	s, err := os.Open(source)
	if err != nil {
		return err
	}

	defer func() { _ = s.Close() }()

	d, err := os.Create(dest)
	if err != nil {
		if os.IsExist(err) {
			d, err = os.OpenFile(dest, os.O_WRONLY, fi.Mode())
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	_, err = io.Copy(d, s)
	if err != nil {
		return err
	}

	/* chown not supported on windows */
	if runtime.GOOS != "windows" {
		err = d.Chown(uid, gid)
		if err != nil {
			return err
		}
	}

	return d.Close()
}

// DirCopy copies a directory recursively, overwriting the target if it exists.
func DirCopy(source string, dest string) error {
	// Get info about source.
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("failed to get source directory info: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("source is not a directory")
	}

	// Remove dest if it already exists.
	if util.PathExists(dest) {
		err := os.RemoveAll(dest)
		if err != nil {
			return fmt.Errorf("failed to remove destination directory %s: %w", dest, err)
		}
	}

	// Create dest.
	err = os.MkdirAll(dest, info.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", dest, err)
	}

	// Copy all files.
	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("failed to read source directory %s: %w", source, err)
	}

	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		destPath := filepath.Join(dest, entry.Name())

		if entry.IsDir() {
			err := DirCopy(sourcePath, destPath)
			if err != nil {
				return fmt.Errorf("failed to copy sub-directory from %s to %s: %w", sourcePath, destPath, err)
			}
		} else {
			err := FileCopy(sourcePath, destPath)
			if err != nil {
				return fmt.Errorf("failed to copy file from %s to %s: %w", sourcePath, destPath, err)
			}
		}
	}

	return nil
}

// AddSlash adds a slash to the end of paths if they don't already have one.
// This can be useful for rsyncing things, since rsync has behavior present on
// the presence or absence of a trailing slash.
func AddSlash(path string) string {
	if path[len(path)-1] != '/' {
		return path + "/"
	}

	return path
}

// PathIsEmpty checks if the given path is empty.
func PathIsEmpty(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}

	defer func() { _ = f.Close() }()

	// read in ONLY one file
	_, err = f.ReadDir(1)

	// and if the file is EOF... well, the dir is empty.
	if err == io.EOF {
		return true, nil
	}

	return false, err
}

func MkdirAllOwner(path string, perm os.FileMode, uid int, gid int) error {
	// This function is a slightly modified version of MkdirAll from the Go standard library.
	// https://golang.org/src/os/path.go?s=488:535#L9

	// Fast path: if we can tell whether path is a directory or file, stop with success or error.
	dir, err := os.Stat(path)
	if err == nil {
		if dir.IsDir() {
			return nil
		}

		return fmt.Errorf("path exists but isn't a directory")
	}

	// Slow path: make sure parent exists and then call Mkdir for path.
	i := len(path)
	for i > 0 && os.IsPathSeparator(path[i-1]) { // Skip trailing path separator.
		i--
	}

	j := i
	for j > 0 && !os.IsPathSeparator(path[j-1]) { // Scan backward over element.
		j--
	}

	if j > 1 {
		// Create parent
		err = MkdirAllOwner(path[0:j-1], perm, uid, gid)
		if err != nil {
			return err
		}
	}

	// Parent now exists; invoke Mkdir and use its result.
	err = os.Mkdir(path, perm)

	err_chown := os.Chown(path, uid, gid)
	if err_chown != nil {
		return err_chown
	}

	if err != nil {
		// Handle arguments like "foo/." by
		// double-checking that directory doesn't exist.
		dir, err1 := os.Lstat(path)
		if err1 == nil && dir.IsDir() {
			return nil
		}

		return err
	}

	return nil
}
