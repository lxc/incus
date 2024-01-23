package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pierrec/lz4/v4"

	internalIO "github.com/lxc/incus/internal/io"
	internalUtil "github.com/lxc/incus/internal/util"
)

// Uncompress the raft snapshot files in the given database directory.
//
// A backup will be created and kept around in case of errors.
func migrateDatabase(dir string) error {
	global := filepath.Join(dir, "global")

	err := internalUtil.DirCopy(global, global+".bak")
	if err != nil {
		return fmt.Errorf("Failed to backup database directory %q: %w", global, err)
	}

	files, err := os.ReadDir(global)
	if err != nil {
		return fmt.Errorf("Failed to list database directory %q: %w", global, err)
	}

	for _, file := range files {
		var timestamp uint64
		var first uint64
		var last uint64

		if !file.Type().IsRegular() {
			continue
		}

		n, err := fmt.Sscanf(file.Name(), "snapshot-%d-%d-%d\n", &timestamp, &first, &last)
		if err != nil || n != 3 {
			continue
		}

		filename := filepath.Join(global, file.Name())
		err = lz4Uncompress(filename)
		if err != nil {
			return fmt.Errorf("Failed to uncompress snapshot %q: %w", filename, err)
		}
	}

	return nil
}

// Uncompress the given file, preserving its mode and ownership.
//
// If the file is not lz4-compressed, this is a no-op.
func lz4Uncompress(zfilename string) error {
	zr := lz4.NewReader(nil)

	zfile, err := os.Open(zfilename)
	if err != nil {
		return fmt.Errorf("Failed to open file %q: %w", zfilename, err)
	}

	buf := make([]byte, 4)
	n, err := zfile.Read(buf)
	if err != nil {
		return fmt.Errorf("Failed to read header file %q: %w", zfilename, err)
	}

	if n != 4 {
		return fmt.Errorf("Read only %d bytes from %q", n, zfilename)
	}

	// Check the file magic, and return now if it's not an lz4 file.
	magic := binary.LittleEndian.Uint32(buf)
	if magic != 0x184D2204 {
		zfile.Close()
		return nil
	}

	off, err := zfile.Seek(0, 0)
	if err != nil {
		return fmt.Errorf("Failed to seek %q: %w", zfilename, err)
	}

	if off != 0 {
		return fmt.Errorf("Seek %q to offset: %d", zfilename, off)
	}

	zinfo, err := zfile.Stat()
	if err != nil {
		return fmt.Errorf("Fialed to get file info for %q: %w", zfilename, err)
	}

	// use the same mode for the output file
	mode := zinfo.Mode()

	_, uid, gid := internalIO.GetOwnerMode(zinfo)

	filename := zfilename + ".uncompressed"
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("Failed to open file %q: %w", filename, err)
	}

	zr.Reset(zfile)

	_, err = io.Copy(file, zr)
	if err != nil {
		return fmt.Errorf("Failed to uncompress %q into %q: %w", zfilename, filename, err)
	}

	for _, c := range []io.Closer{zfile, file} {
		err := c.Close()
		if err != nil {
			return fmt.Errorf("Failed to close file: %w", err)
		}
	}

	err = os.Chown(filename, uid, gid)
	if err != nil {
		return fmt.Errorf("Failed to set ownership of %q: %w", filename, err)
	}

	err = os.Rename(filename, zfilename)
	if err != nil {
		return fmt.Errorf("Failed to rename %q to %q: %w", filename, zfilename, err)
	}

	return nil
}
