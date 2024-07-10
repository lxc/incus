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

		// Instances are 0711 so the runtime can traverse to the data.
		{filepath.Join(s.VarDir, "containers"), 0711},
		{filepath.Join(s.VarDir, "virtual-machines"), 0711},

		// Snapshots are kept 0700 as the runtime doesn't need access.
		{filepath.Join(s.VarDir, "containers-snapshots"), 0700},
		{filepath.Join(s.VarDir, "virtual-machines-snapshots"), 0700},

		{filepath.Join(s.VarDir, "backups"), 0700},
		{s.CacheDir, 0700},
		{filepath.Join(s.CacheDir, "resources"), 0700},
		{filepath.Join(s.VarDir, "database"), 0700},
		{filepath.Join(s.VarDir, "devices"), 0711},
		{filepath.Join(s.VarDir, "disks"), 0700},
		{filepath.Join(s.VarDir, "guestapi"), 0755},
		{filepath.Join(s.VarDir, "images"), 0700},
		{s.LogDir, 0700},
		{filepath.Join(s.VarDir, "networks"), 0711},
		{s.RunDir, 0700},
		{filepath.Join(s.VarDir, "security"), 0700},
		{filepath.Join(s.VarDir, "security", "apparmor"), 0700},
		{filepath.Join(s.VarDir, "security", "apparmor", "cache"), 0700},
		{filepath.Join(s.VarDir, "security", "apparmor", "profiles"), 0700},
		{filepath.Join(s.VarDir, "security", "seccomp"), 0700},
		{filepath.Join(s.VarDir, "shmounts"), 0711},
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
