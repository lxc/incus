package drivers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/project"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/subprocess"
)

// Type of the block volume.
const (
	BlockVolumeTypeRaw   = "raw"
	BlockVolumeTypeQcow2 = "qcow2"
)

// ImageInfo contains information about a qcow2 image.
type ImageInfo struct {
	BackingFilename string `json:"backing-filename"`
	Format          string `json:"format"`
	VirtualSize     int    `json:"virtual-size"`
}

// Qcow2Create creates a qcow2-formatted image.
func Qcow2Create(path string, backingPath string, size int64) error {
	args := []string{
		"create",
		"-f",
		"qcow2",
	}

	if backingPath != "" {
		args = append(args, "-b", backingPath)
		args = append(args, "-F", "qcow2")
	}

	args = append(args, path)

	if size > 0 {
		args = append(args, fmt.Sprintf("%db", size))
	}

	_, err := subprocess.RunCommand("qemu-img", args...)
	if err != nil {
		return err
	}

	return nil
}

// Qcow2Rebase changes the backing file of a qcow2 image.
func Qcow2Rebase(path string, backingPath string) error {
	_, err := subprocess.RunCommand("qemu-img", "rebase", "-u", "-b", backingPath, "-F", "qcow2", path)
	if err != nil {
		return err
	}

	return nil
}

// Qcow2Commit commits changes from a qcow2 image to its immediate backing file.
func Qcow2Commit(path string) error {
	_, err := subprocess.RunCommand("qemu-img", "commit", "-f", "qcow2", path)
	if err != nil {
		return err
	}

	return nil
}

// Qcow2Info returns information about a qcow2 image.
func Qcow2Info(path string) (*ImageInfo, error) {
	imgJSON, err := subprocess.RunCommand("qemu-img", "info", "--output=json", path)
	if err != nil {
		return nil, err
	}

	imgInfo := ImageInfo{}

	err = json.Unmarshal([]byte(imgJSON), &imgInfo)
	if err != nil {
		return nil, fmt.Errorf("Failed unmarshalling image info %q: %w (%q)", path, err, imgJSON)
	}

	return &imgInfo, nil
}

// Qcow2BackingChain returns information about the backing chain of a qcow2 image.
func Qcow2BackingChain(path string) ([]string, error) {
	result := []string{}
	imgJSON, err := subprocess.RunCommand("qemu-img", "info", "--backing-chain", "--output=json", path)
	if err != nil {
		return nil, err
	}

	imgInfo := []struct {
		BackingFilename string `json:"backing-filename"`
	}{}

	err = json.Unmarshal([]byte(imgJSON), &imgInfo)
	if err != nil {
		return nil, fmt.Errorf("Failed unmarshalling image info %q: %w (%q)", path, err, imgJSON)
	}

	for _, info := range imgInfo {
		if info.BackingFilename == "" {
			break
		}

		result = append(result, info.BackingFilename)
	}

	return result, nil
}

// Qcow2MountConfigTask mounts the config filesystem volume with its snapshots and performs the task specified by the parameter.
func Qcow2MountConfigTask(vol Volume, op *operations.Operation, task func(mountPath string) error) error {
	mountPath := fmt.Sprintf("%s%s", vol.MountPath(), tmpVolSuffix)
	mountVol := NewVolume(vol.driver, vol.driver.Name(), vol.volType, vol.contentType, vol.name, vol.config, vol.poolConfig)
	mountVol.mountFullFilesystem = true
	mountVol.mountCustomPath = mountPath
	wasMounted := linux.IsMountPoint(mountPath)

	err := mountVol.MountTask(func(mountPath string, op *operations.Operation) error {
		taskErr := task(mountVol.MountPath())

		// Return task error if failed.
		if taskErr != nil {
			return taskErr
		}

		return nil
	}, op)
	if err != nil {
		return err
	}

	// MountTask delegates unmounting to UnmountVolume(), which calculates the
	// refCount based on the volume name and type. Since a volume can be mounted
	// at multiple paths, it is only unmounted when the refCount drops to zero.
	// In this case, we unmount from customPath if the mount is no longer needed.
	if !wasMounted && linux.IsMountPoint(mountPath) {
		err = TryUnmount(mountPath, 0)
		if err != nil {
			return fmt.Errorf("Failed to unmount logical volume: %w", err)
		}
	}

	// Remove temporary mount path.
	isEmpty, err := internalUtil.PathIsEmpty(mountPath)
	if err != nil {
		return err
	}

	if isEmpty {
		err := os.Remove(mountPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("Failed to remove '%s': %w", mountPath, err)
		}
	}

	return nil
}

// Qcow2CreateConfig creates the btrfs config filesystem associated with the QCOW2 block volume.
func Qcow2CreateConfig(vol Volume, op *operations.Operation) error {
	err := Qcow2MountConfigTask(vol, op, func(mountPath string) error {
		_, volName := project.StorageVolumeParts(vol.Name())
		volPath := filepath.Join(mountPath, volName)
		// Create the volume itself.
		_, err := subprocess.RunCommand("btrfs", "subvolume", "create", volPath)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// Qcow2CreateConfigSnapshot creates the btrfs snapshot of the config filesystem associated with the QCOW2 block volume.
func Qcow2CreateConfigSnapshot(vol Volume, snapVol Volume, op *operations.Operation) error {
	err := Qcow2MountConfigTask(vol, op, func(mountPath string) error {
		fullParent, snapName, _ := api.GetParentAndSnapshotName(snapVol.Name())
		_, parent := project.StorageVolumeParts(fullParent)
		dstPath := filepath.Join(mountPath, fmt.Sprintf("%s-%s", parent, snapName))
		srcPath := filepath.Join(mountPath, parent)

		_, err := subprocess.RunCommand("btrfs", "subvolume", "snapshot", srcPath, dstPath)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// Qcow2RestoreConfigSnapshot restores the btrfs snapshot of the config filesystem associated with the QCOW2 block volume.
func Qcow2RestoreConfigSnapshot(vol Volume, snapVol Volume, op *operations.Operation) error {
	err := Qcow2MountConfigTask(vol, op, func(mountPath string) error {
		fullParent, snapName, _ := api.GetParentAndSnapshotName(snapVol.Name())
		_, parent := project.StorageVolumeParts(fullParent)
		snapPath := fmt.Sprintf("%s-%s", parent, snapName)

		// Delete the subvolume itself.
		_, err := subprocess.RunCommand("btrfs", "subvolume", "delete", filepath.Join(mountPath, parent))
		if err != nil {
			return err
		}

		_, err = subprocess.RunCommand("btrfs", "subvolume", "snapshot", filepath.Join(mountPath, snapPath), filepath.Join(mountPath, parent))
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// Qcow2RenameConfigSnapshot renames the btrfs snapshot of the config filesystem associated with the QCOW2 block volume.
func Qcow2RenameConfigSnapshot(vol Volume, snapVol Volume, newName string, op *operations.Operation) error {
	err := Qcow2MountConfigTask(vol, op, func(mountPath string) error {
		fullParent, snapName, _ := api.GetParentAndSnapshotName(snapVol.Name())
		_, parent := project.StorageVolumeParts(fullParent)
		oldPath := filepath.Join(mountPath, fmt.Sprintf("%s-%s", parent, snapName))
		newPath := filepath.Join(mountPath, fmt.Sprintf("%s-%s", parent, newName))

		err := os.Rename(oldPath, newPath)
		if err != nil {
			return fmt.Errorf("Failed to rename %q to %q: %w", oldPath, newPath, err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// Qcow2DeleteConfigSnapshot deletes the btrfs snapshot of the config filesystem associated with the QCOW2 block volume.
func Qcow2DeleteConfigSnapshot(vol Volume, snapVol Volume, op *operations.Operation) error {
	err := Qcow2MountConfigTask(vol, op, func(mountPath string) error {
		fullParent, snapName, _ := api.GetParentAndSnapshotName(snapVol.Name())
		_, parent := project.StorageVolumeParts(fullParent)
		path := filepath.Join(mountPath, fmt.Sprintf("%s-%s", parent, snapName))

		// Delete the subvolume itself.
		_, err := subprocess.RunCommand("btrfs", "subvolume", "delete", path)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// isQcow2Block checks whether a volume is a QCOW2 block device.
func isQcow2Block(vol Volume) bool {
	return vol.Config()["block.type"] == BlockVolumeTypeQcow2 && vol.ContentType() == ContentTypeBlock
}
