package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pierrec/lz4/v4"

	"github.com/lxc/incus/shared"
)

// Uncompress the raft snapshot files in the given database directory.
//
// A backup will be created and kept around in case of errors.
func migrateDatabase(dir string) error {
	global := filepath.Join(dir, "global")

	err := shared.DirCopy(global, global+".bak")
	if err != nil {
		return fmt.Errorf("Failed to backup database directory %q: %w", global, err)
	}

	files, err := ioutil.ReadDir(global)
	if err != nil {
		return fmt.Errorf("Failed to list database directory %q: %w", global, err)
	}

	for _, file := range files {
		var timestamp uint64
		var first uint64
		var last uint64

		if !file.Mode().IsRegular() {
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
func lz4Uncompress(zfilename string) error {
	zr := lz4.NewReader(nil)

	zfile, err := os.Open(zfilename)
	if err != nil {
		return fmt.Errorf("Failed to open file %q: %w", zfilename, err)
	}

	zinfo, err := zfile.Stat()
	if err != nil {
		return fmt.Errorf("Fialed to get file info for %q: %w", zfilename, err)
	}

	// use the same mode for the output file
	mode := zinfo.Mode()

	_, uid, gid := shared.GetOwnerMode(zinfo)

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
