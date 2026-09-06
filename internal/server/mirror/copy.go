package mirror

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/internal/linux"
)

// CopyFile copies src to dst atomically, leaving holes where src contains zeros.
func CopyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}

	defer func() { _ = in.Close() }()

	fi, err := in.Stat()
	if err != nil {
		return err
	}

	tmp := filepath.Join(filepath.Dir(dst), fmt.Sprintf(".%s.mirror", filepath.Base(dst)))
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	defer func() {
		_ = out.Close()
		_ = os.Remove(tmp)
	}()

	err = copySparse(in, out, fi.Size())
	if err != nil {
		return fmt.Errorf("Failed copying %q: %w", src, err)
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

	return os.Rename(tmp, dst)
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

// Seed copies the regular files of src accepted by filter into dst.
func Seed(src string, dst string, filter func(name string) bool) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("Failed reading %q: %w", src, err)
	}

	for _, entry := range entries {
		if !entry.Type().IsRegular() || !filter(entry.Name()) {
			continue
		}

		err := CopyFile(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()))
		if err != nil {
			return err
		}
	}

	return nil
}
