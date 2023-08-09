//go:build linux && cgo && !agent

package sys

import (
	"fmt"
	"os"
	"path/filepath"
)

// LocalDatabasePath returns the path of the local database file.
func (s *OS) LocalDatabasePath() string {
	return filepath.Join(s.VarDir, "database", "local.db")
}

// GlobalDatabaseDir returns the path of the global database directory.
func (s *OS) GlobalDatabaseDir() string {
	return filepath.Join(s.VarDir, "database", "global")
}

// GlobalDatabasePath returns the path of the global database SQLite file
// managed by dqlite.
func (s *OS) GlobalDatabasePath() string {
	return filepath.Join(s.GlobalDatabaseDir(), "db.bin")
}

// initDirs Make sure all our directories are available.
func (s *OS) initDirs() error {
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{s.VarDir, 0711},
		{filepath.Join(s.VarDir, "backups"), 0700},
		{s.CacheDir, 0700},
		// containers is 0711 because liblxc needs to traverse dir to get to each container.
		{filepath.Join(s.VarDir, "containers"), 0711},
		{filepath.Join(s.VarDir, "virtual-machines"), 0711},
		{filepath.Join(s.VarDir, "database"), 0700},
		{filepath.Join(s.VarDir, "devices"), 0711},
		{filepath.Join(s.VarDir, "devlxd"), 0755},
		{filepath.Join(s.VarDir, "disks"), 0700},
		{filepath.Join(s.VarDir, "images"), 0700},
		{s.LogDir, 0700},
		{filepath.Join(s.VarDir, "networks"), 0711},
		{filepath.Join(s.VarDir, "security"), 0700},
		{filepath.Join(s.VarDir, "security", "apparmor"), 0700},
		{filepath.Join(s.VarDir, "security", "apparmor", "cache"), 0700},
		{filepath.Join(s.VarDir, "security", "apparmor", "profiles"), 0700},
		{filepath.Join(s.VarDir, "security", "seccomp"), 0700},
		{filepath.Join(s.VarDir, "shmounts"), 0711},
		// snapshots is 0700 as liblxc does not need to access this.
		{filepath.Join(s.VarDir, "snapshots"), 0700},
		{filepath.Join(s.VarDir, "virtual-machines-snapshots"), 0700},
		{filepath.Join(s.VarDir, "storage-pools"), 0711},
	}

	for _, dir := range dirs {
		err := os.Mkdir(dir.path, dir.mode)
		if err != nil {
			if !os.IsExist(err) {
				return fmt.Errorf("Failed to init dir %q: %w", dir.path, err)
			}

			err = os.Chmod(dir.path, dir.mode)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("Failed to chmod dir %q: %w", dir.path, err)
			}
		}
	}

	return nil
}

// initStorageDirs make sure all our directories are on the storage layer (after storage is mounted).
func (s *OS) initStorageDirs() error {
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(s.VarDir, "backups", "custom"), 0700},
		{filepath.Join(s.VarDir, "backups", "instances"), 0700},
	}

	for _, dir := range dirs {
		err := os.Mkdir(dir.path, dir.mode)
		if err != nil {
			if !os.IsExist(err) {
				return fmt.Errorf("Failed to init storage dir %q: %w", dir.path, err)
			}

			err = os.Chmod(dir.path, dir.mode)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("Failed to chmod storage dir %q: %w", dir.path, err)
			}
		}
	}

	return nil
}
