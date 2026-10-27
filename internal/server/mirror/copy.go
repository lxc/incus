package mirror

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/internal/linux"
)

// CopyFile copies src to dst atomically, leaving holes where src contains zeros.
// The parent directories are trusted but the file names themselves are never followed.
func CopyFile(src string, dst string) error {
	srcRoot, err := os.OpenRoot(filepath.Dir(src))
	if err != nil {
		return err
	}

	defer func() { _ = srcRoot.Close() }()

	dstRoot, err := os.OpenRoot(filepath.Dir(dst))
	if err != nil {
		return err
	}

	defer func() { _ = dstRoot.Close() }()

	return copyFile(srcRoot, filepath.Base(src), dstRoot, filepath.Base(dst))
}

// copyFile copies srcName in srcRoot to dstName in dstRoot atomically, refusing symlinks and special files.
func copyFile(srcRoot *os.Root, srcName string, dstRoot *os.Root, dstName string) error {
	in, err := srcRoot.OpenFile(srcName, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}

	defer func() { _ = in.Close() }()

	fi, err := in.Stat()
	if err != nil {
		return err
	}

	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", srcName)
	}

	// Never write through a pre-existing entry, whatever it may be.
	tmp := fmt.Sprintf(".%s.mirror", dstName)
	err = dstRoot.Remove(tmp)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	out, err := dstRoot.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}

	defer func() {
		_ = out.Close()
		_ = dstRoot.Remove(tmp)
	}()

	err = copySparse(in, out, fi.Size())
	if err != nil {
		return fmt.Errorf("Failed copying %q: %w", srcName, err)
	}

	err = out.Truncate(fi.Size())
	if err != nil {
		return err
	}

	err = out.Sync()
	if err != nil {
		return err
	}

	err = out.Close()
	if err != nil {
		return err
	}

	return dstRoot.Rename(tmp, dstName)
}

// copySparse writes the data regions of in to out, skipping holes and zero-filled blocks.
func copySparse(in *os.File, out *os.File, size int64) error {
	buf := make([]byte, 128*1024)
	writer := linux.NewSparseFileWrapper(out)

	var offset int64
	for offset < size {
		// Find the next data region, falling back to a full copy when unsupported.
		data, err := unix.Seek(int(in.Fd()), offset, unix.SEEK_DATA)
		if errors.Is(err, unix.ENXIO) {
			break
		}

		hole := size
		if err == nil {
			hole, err = unix.Seek(int(in.Fd()), data, unix.SEEK_HOLE)
			if err != nil {
				return err
			}
		} else {
			data = offset
		}

		_, err = out.Seek(data, io.SeekStart)
		if err != nil {
			return err
		}

		_, err = io.CopyBuffer(writer, io.NewSectionReader(in, data, hole-data), buf)
		if err != nil {
			return err
		}

		offset = hole
	}

	return nil
}

// listRegular returns the regular files of root accepted by filter.
func listRegular(root *os.Root, filter func(name string) bool) ([]string, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("Failed reading %q: %w", root.Name(), err)
	}

	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return nil, fmt.Errorf("Failed reading %q: %w", root.Name(), err)
	}

	names := []string{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !filter(entry.Name()) {
			continue
		}

		names = append(names, entry.Name())
	}

	return names, nil
}

// Seed copies the regular files of src accepted by filter into dst.
func Seed(src string, dst string, filter func(name string) bool) error {
	srcRoot, err := os.OpenRoot(src)
	if err != nil {
		return err
	}

	defer func() { _ = srcRoot.Close() }()

	dstRoot, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}

	defer func() { _ = dstRoot.Close() }()

	names, err := listRegular(srcRoot, filter)
	if err != nil {
		return err
	}

	for _, name := range names {
		err := copyFile(srcRoot, name, dstRoot, name)
		if err != nil {
			return err
		}
	}

	return nil
}
