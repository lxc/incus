package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/internal/rsync"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/node"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	storageDrivers "github.com/lxc/incus/internal/server/storage/drivers"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
)

func daemonStorageVolumesUnmount(s *state.State) error {
	var storageBackups string
	var storageImages string

	err := s.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		storageBackups = nodeConfig.StorageBackupsVolume()
		storageImages = nodeConfig.StorageImagesVolume()

		return nil
	})
	if err != nil {
		return err
	}

	unmount := func(storageType string, source string) error {
		// Parse the source.
		poolName, volumeName, err := daemonStorageSplitVolume(source)
		if err != nil {
			return err
		}

		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return err
		}

		// Mount volume.
		_, err = pool.UnmountCustomVolume(api.ProjectDefaultName, volumeName, nil)
		if err != nil {
			return fmt.Errorf("Failed to unmount storage volume %q: %w", source, err)
		}

		return nil
	}

	if storageBackups != "" {
		err := unmount("backups", storageBackups)
		if err != nil {
			return fmt.Errorf("Failed to unmount backups storage: %w", err)
		}
	}

	if storageImages != "" {
		err := unmount("images", storageImages)
		if err != nil {
			return fmt.Errorf("Failed to unmount images storage: %w", err)
		}
	}

	return nil
}

func daemonStorageMount(s *state.State) error {
	var storageBackups string
	var storageImages string
	err := s.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		storageBackups = nodeConfig.StorageBackupsVolume()
		storageImages = nodeConfig.StorageImagesVolume()

		return nil
	})
	if err != nil {
		return err
	}

	mount := func(storageType string, source string) error {
		// Parse the source.
		poolName, volumeName, err := daemonStorageSplitVolume(source)
		if err != nil {
			return err
		}

		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return err
		}

		// Mount volume.
		_, err = pool.MountCustomVolume(api.ProjectDefaultName, volumeName, nil)
		if err != nil {
			return fmt.Errorf("Failed to mount storage volume %q: %w", source, err)
		}

		return nil
	}

	if storageBackups != "" {
		err := mount("backups", storageBackups)
		if err != nil {
			return fmt.Errorf("Failed to mount backups storage: %w", err)
		}
	}

	if storageImages != "" {
		err := mount("images", storageImages)
		if err != nil {
			return fmt.Errorf("Failed to mount images storage: %w", err)
		}
	}

	return nil
}

func daemonStorageSplitVolume(volume string) (string, string, error) {
	fields := strings.Split(volume, "/")
	if len(fields) != 2 {
		return "", "", fmt.Errorf("Invalid syntax for volume, must be <pool>/<volume>")
	}

	poolName := fields[0]
	volumeName := fields[1]

	return poolName, volumeName, nil
}

func daemonStorageValidate(s *state.State, target string) error {
	// Check syntax.
	if target == "" {
		return nil
	}

	poolName, volumeName, err := daemonStorageSplitVolume(target)
	if err != nil {
		return err
	}

	// Validate pool exists.
	poolID, _, _, err := s.DB.Cluster.GetStoragePool(poolName)
	if err != nil {
		return fmt.Errorf("Unable to load storage pool %q: %w", poolName, err)
	}

	// Confirm volume exists.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVol, err := tx.GetStoragePoolVolume(ctx, poolID, api.ProjectDefaultName, db.StoragePoolVolumeTypeCustom, volumeName, true)
		if err != nil {
			return fmt.Errorf("Failed loading storage volume %q in %q project: %w", target, api.ProjectDefaultName, err)
		}

		if dbVol.ContentType != db.StoragePoolVolumeContentTypeNameFS {
			return fmt.Errorf("Storage volume %q in %q project is not filesystem content type", target, api.ProjectDefaultName)
		}

		return nil
	})
	if err != nil {
		return err
	}

	snapshots, err := s.DB.Cluster.GetLocalStoragePoolVolumeSnapshotsWithType(api.ProjectDefaultName, volumeName, db.StoragePoolVolumeTypeCustom, poolID)
	if err != nil {
		return fmt.Errorf("Unable to load storage volume snapshots %q in %q project: %w", target, api.ProjectDefaultName, err)
	}

	if len(snapshots) != 0 {
		return fmt.Errorf("Storage volumes for use by Incus itself cannot have snapshots")
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return err
	}

	// Mount volume.
	_, err = pool.MountCustomVolume(api.ProjectDefaultName, volumeName, nil)
	if err != nil {
		return fmt.Errorf("Failed to mount storage volume %q: %w", target, err)
	}

	defer func() { _, _ = pool.UnmountCustomVolume(api.ProjectDefaultName, volumeName, nil) }()

	// Validate volume is empty (ignore lost+found).
	volStorageName := project.StorageVolume(api.ProjectDefaultName, volumeName)
	mountpoint := storageDrivers.GetVolumeMountPath(poolName, storageDrivers.VolumeTypeCustom, volStorageName)

	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		return fmt.Errorf("Failed to list %q: %w", mountpoint, err)
	}

	for _, entry := range entries {
		entryName := entry.Name()

		// Don't fail on clean ext4 volumes.
		if entryName == "lost+found" {
			continue
		}

		// Don't fail on systems with snapdir=visible.
		if entryName == ".zfs" {
			continue
		}

		return fmt.Errorf("Storage volume %q isn't empty", target)
	}

	return nil
}

func daemonStorageMove(s *state.State, storageType string, target string) error {
	destPath := internalUtil.VarPath(storageType)

	// Track down the current storage.
	var sourcePool string
	var sourceVolume string

	sourcePath, err := os.Readlink(destPath)
	if err != nil {
		sourcePath = destPath
	} else {
		fields := strings.Split(sourcePath, "/")
		sourcePool = fields[len(fields)-3]
		sourceVolume = fields[len(fields)-1]
	}

	moveContent := func(source string, target string) error {
		// Copy the content.
		_, err := rsync.LocalCopy(source, target, "", false)
		if err != nil {
			return err
		}

		// Remove the source content.
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}

		for _, entry := range entries {
			err := os.RemoveAll(filepath.Join(source, entry.Name()))
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Deal with unsetting.
	if target == "" {
		// Things already look correct.
		if sourcePath == destPath {
			return nil
		}

		// Remove the symlink.
		err = os.Remove(destPath)
		if err != nil {
			return fmt.Errorf("Failed to delete storage symlink at %q: %w", destPath, err)
		}

		// Re-create as a directory.
		err = os.MkdirAll(destPath, 0700)
		if err != nil {
			return fmt.Errorf("Failed to create directory %q: %w", destPath, err)
		}

		// Move the data across.
		err = moveContent(sourcePath, destPath)
		if err != nil {
			return fmt.Errorf("Failed to move data over to directory %q: %w", destPath, err)
		}

		pool, err := storagePools.LoadByName(s, sourcePool)
		if err != nil {
			return err
		}

		// Unmount old volume.
		projectName, sourceVolumeName := project.StorageVolumeParts(sourceVolume)
		_, err = pool.UnmountCustomVolume(projectName, sourceVolumeName, nil)
		if err != nil {
			return fmt.Errorf(`Failed to umount storage volume "%s/%s": %w`, sourcePool, sourceVolumeName, err)
		}

		return nil
	}

	// Parse the target.
	poolName, volumeName, err := daemonStorageSplitVolume(target)
	if err != nil {
		return err
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return err
	}

	// Mount volume.
	_, err = pool.MountCustomVolume(api.ProjectDefaultName, volumeName, nil)
	if err != nil {
		return fmt.Errorf("Failed to mount storage volume %q: %w", target, err)
	}

	// Set ownership & mode.
	volStorageName := project.StorageVolume(api.ProjectDefaultName, volumeName)
	mountpoint := storageDrivers.GetVolumeMountPath(poolName, storageDrivers.VolumeTypeCustom, volStorageName)
	destPath = mountpoint

	err = os.Chmod(mountpoint, 0700)
	if err != nil {
		return fmt.Errorf("Failed to set permissions on %q: %w", mountpoint, err)
	}

	err = os.Chown(mountpoint, 0, 0)
	if err != nil {
		return fmt.Errorf("Failed to set ownership on %q: %w", mountpoint, err)
	}

	// Handle changes.
	if sourcePath != internalUtil.VarPath(storageType) {
		// Remove the symlink.
		err := os.Remove(internalUtil.VarPath(storageType))
		if err != nil {
			return fmt.Errorf("Failed to remove the new symlink at %q: %w", internalUtil.VarPath(storageType), err)
		}

		// Create the new symlink.
		err = os.Symlink(destPath, internalUtil.VarPath(storageType))
		if err != nil {
			return fmt.Errorf("Failed to create the new symlink at %q: %w", internalUtil.VarPath(storageType), err)
		}

		// Move the data across.
		err = moveContent(sourcePath, destPath)
		if err != nil {
			return fmt.Errorf("Failed to move data over to directory %q: %w", destPath, err)
		}

		pool, err := storagePools.LoadByName(s, sourcePool)
		if err != nil {
			return err
		}

		// Unmount old volume.
		projectName, sourceVolumeName := project.StorageVolumeParts(sourceVolume)
		_, err = pool.UnmountCustomVolume(projectName, sourceVolumeName, nil)
		if err != nil {
			return fmt.Errorf(`Failed to umount storage volume "%s/%s": %w`, sourcePool, sourceVolumeName, err)
		}

		return nil
	}

	sourcePath = internalUtil.VarPath(storageType) + ".temp"

	// Rename the existing storage.
	err = os.Rename(internalUtil.VarPath(storageType), sourcePath)
	if err != nil {
		return fmt.Errorf("Failed to rename existing storage %q: %w", internalUtil.VarPath(storageType), err)
	}

	// Create the new symlink.
	err = os.Symlink(destPath, internalUtil.VarPath(storageType))
	if err != nil {
		return fmt.Errorf("Failed to create the new symlink at %q: %w", internalUtil.VarPath(storageType), err)
	}

	// Move the data across.
	err = moveContent(sourcePath, destPath)
	if err != nil {
		return fmt.Errorf("Failed to move data over to directory %q: %w", destPath, err)
	}

	// Remove the old data.
	err = os.RemoveAll(sourcePath)
	if err != nil {
		return fmt.Errorf("Failed to cleanup old directory %q: %w", sourcePath, err)
	}

	return nil
}
