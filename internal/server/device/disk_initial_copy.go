package device

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	internalInstance "github.com/lxc/incus/v7/internal/instance"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/locking"
	"github.com/lxc/incus/v7/internal/server/project"
	storageDrivers "github.com/lxc/incus/v7/internal/server/storage/drivers"
	"github.com/lxc/incus/v7/shared/idmap"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/util"
)

// idMapper translates on-disk ownership from the instance rootfs into on-disk ownership for the volume.
type idMapper func(uid int64, gid int64) (int64, int64, error)

// initialCopy populates an empty custom volume with the instance's existing content at the device path, once.
func (d *disk) initialCopy(volMountPath string) error {
	c, ok := d.inst.(instance.Container)
	if !ok {
		return errors.New(`The "initial.copy" property is only supported on containers`)
	}

	volName, volPath := internalInstance.SplitVolumeSource(d.config["source"])

	storageProjectName, err := project.StorageVolumeProject(d.state.DB.Cluster, d.inst.Project().Name, db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return err
	}

	// Serialize with other instances attaching the same volume.
	unlock, err := locking.Lock(context.TODO(), storageDrivers.OperationLockName("InitialCopy", d.pool.Name(), storageDrivers.VolumeTypeCustom, storageDrivers.ContentTypeFS, project.StorageVolume(storageProjectName, volName)))
	if err != nil {
		return err
	}

	defer unlock()

	var dbVolume *db.StorageVolume
	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, d.pool.ID(), storageProjectName, db.StoragePoolVolumeTypeCustom, volName, true)
		return err
	})
	if err != nil {
		return err
	}

	if util.IsTrue(dbVolume.Config["volatile.initial.copied"]) {
		return nil
	}

	// Device updates restart the device, so only reject a running instance when there's something to copy.
	if c.IsRunning() {
		return errors.New(`The "initial.copy" property can't be applied to a running instance`)
	}

	// Open the target, confined to the volume.
	volRoot, err := os.OpenRoot(volMountPath)
	if err != nil {
		return fmt.Errorf("Failed opening volume path %q: %w", volMountPath, err)
	}

	defer logger.WarnOnError(volRoot.Close, "Failed to close volume path")

	dstRoot := volRoot
	if volPath != "" {
		dstRoot, err = volRoot.OpenRoot(volPath)
		if err != nil {
			return fmt.Errorf("Failed opening volume sub-path %q: %w", volPath, err)
		}

		defer logger.WarnOnError(dstRoot.Close, "Failed to close volume sub-path")
	}

	// Only ever copy into an empty target.
	entries, err := fs.ReadDir(dstRoot.FS(), ".")
	if err != nil {
		return fmt.Errorf("Failed listing volume content: %w", err)
	}

	empty := true
	for _, entry := range entries {
		if entry.Name() != "lost+found" {
			empty = false
			break
		}
	}

	if empty {
		mapID, err := d.initialCopyIDMapper(c, dbVolume.Config)
		if err != nil {
			return err
		}

		err = d.copyRootfsPath(c, dstRoot, mapID)
		if err != nil {
			return err
		}
	} else {
		d.logger.Debug("Skipping initial copy into non-empty volume", logger.Ctx{"volume": volName})
	}

	// Record the copy so it never happens again for this volume.
	poolVolumePut := dbVolume.Writable()
	poolVolumePut.Config["volatile.initial.copied"] = "true"

	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateStoragePoolVolume(ctx, storageProjectName, volName, db.StoragePoolVolumeTypeCustom, d.pool.ID(), poolVolumePut.Description, poolVolumePut.Config)
	})
	if err != nil {
		return fmt.Errorf("Failed recording initial copy on volume %q: %w", volName, err)
	}

	return nil
}

// initialCopyIDMapper returns the ownership translation from the rootfs on-disk IDs to the volume on-disk IDs.
func (d *disk) initialCopyIDMapper(c instance.Container, volConfig map[string]string) (idMapper, error) {
	// On-disk idmap of the rootfs (empty when unshifted or using idmapped mounts).
	srcIdmap, err := c.DiskIdmap()
	if err != nil {
		return nil, err
	}

	// On-disk idmap of the volume (empty when unshifted or using idmapped mounts).
	var dstIdmap *idmap.Set
	if util.IsFalseOrEmpty(volConfig["security.unmapped"]) && util.IsFalseOrEmpty(volConfig["security.shifted"]) {
		dstIdmap, err = c.NextIdmap()
		if err != nil {
			return nil, err
		}
	}

	return func(uid int64, gid int64) (int64, int64, error) {
		// Translate to container IDs, host-owned entries show up as nobody in the container.
		if srcIdmap != nil && srcIdmap.Len() > 0 {
			uid, gid = srcIdmap.ShiftFromNS(uid, gid)
			if uid == -1 {
				uid = 65534
			}

			if gid == -1 {
				gid = 65534
			}
		}

		// Translate to the volume's on-disk IDs.
		if dstIdmap != nil && dstIdmap.Len() > 0 {
			uid, gid = dstIdmap.ShiftIntoNS(uid, gid)
			if uid == -1 || gid == -1 {
				return -1, -1, errors.New("Ownership isn't covered by the instance idmap")
			}
		}

		return uid, gid, nil
	}, nil
}

// copyRootfsPath copies the directory at the device path inside the rootfs into the empty target.
func (d *disk) copyRootfsPath(c instance.Container, dstRoot *os.Root, mapID idMapper) error {
	rootfs, err := os.OpenRoot(c.RootfsPath())
	if err != nil {
		return fmt.Errorf("Failed opening instance rootfs: %w", err)
	}

	defer logger.WarnOnError(rootfs.Close, "Failed to close instance rootfs")

	relPath := strings.TrimPrefix(filepath.Clean(d.config["path"]), "/")
	if relPath == "" || relPath == "." {
		return errors.New(`The "initial.copy" property can't be used with the root path`)
	}

	// Symlinks are resolved within the rootfs only.
	info, err := rootfs.Stat(relPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			d.logger.Debug("Nothing to copy, path doesn't exist in the instance", logger.Ctx{"path": d.config["path"]})
			return nil
		}

		return fmt.Errorf("Failed accessing %q in the instance: %w", d.config["path"], err)
	}

	if !info.IsDir() {
		return fmt.Errorf("Path %q in the instance isn't a directory", d.config["path"])
	}

	srcRoot, err := rootfs.OpenRoot(relPath)
	if err != nil {
		return fmt.Errorf("Failed opening %q in the instance: %w", d.config["path"], err)
	}

	defer logger.WarnOnError(srcRoot.Close, "Failed to close instance path")

	d.logger.Debug("Copying instance content into volume", logger.Ctx{"path": d.config["path"]})

	err = d.copyTree(srcRoot, dstRoot, mapID)
	if err != nil {
		// The target was empty, so drop the partial copy to keep the retry path clean.
		entries, _ := fs.ReadDir(dstRoot.FS(), ".")
		for _, entry := range entries {
			if entry.Name() == "lost+found" {
				continue
			}

			_ = dstRoot.RemoveAll(entry.Name())
		}

		return fmt.Errorf("Failed copying %q into the volume: %w", d.config["path"], err)
	}

	return nil
}

// copyTree recursively copies directories, regular files and symlinks from src into dst with their metadata.
func (d *disk) copyTree(src *os.Root, dst *os.Root, mapID idMapper) error {
	type dirEntry struct {
		path string
		info fs.FileInfo
		uid  int64
		gid  int64
	}

	dirs := []dirEntry{}

	err := fs.WalkDir(src.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("Failed getting ownership of %q", path)
		}

		uid, gid, err := mapID(int64(st.Uid), int64(st.Gid))
		if err != nil {
			return fmt.Errorf("Failed mapping ownership of %q: %w", path, err)
		}

		mode := info.Mode()
		switch {
		case mode.IsDir():
			if path != "." {
				err = dst.Mkdir(path, 0o700)
				if err != nil {
					return err
				}
			}

			// Directory metadata is applied once its content has been copied.
			dirs = append(dirs, dirEntry{path: path, info: info, uid: uid, gid: gid})
			return nil

		case mode.IsRegular():
			return copyRegularFile(src, dst, path, info, uid, gid)

		case mode&fs.ModeSymlink != 0:
			target, err := src.Readlink(path)
			if err != nil {
				return err
			}

			err = dst.Symlink(target, path)
			if err != nil {
				return err
			}

			return dst.Lchown(path, int(uid), int(gid))

		default:
			d.logger.Debug("Skipping special file during initial copy", logger.Ctx{"path": path, "mode": mode.String()})
			return nil
		}
	})
	if err != nil {
		return err
	}

	// Deepest directories first so restrictive modes don't get in the way.
	for i := len(dirs) - 1; i >= 0; i-- {
		dir := dirs[i]

		srcDir, err := src.Open(dir.path)
		if err != nil {
			return err
		}

		dstDir, err := dst.Open(dir.path)
		if err != nil {
			_ = srcDir.Close()
			return err
		}

		err = copyMetadata(srcDir, dstDir, dir.info, dir.uid, dir.gid)
		_ = srcDir.Close()
		_ = dstDir.Close()
		if err != nil {
			return fmt.Errorf("Failed setting metadata on %q: %w", dir.path, err)
		}
	}

	return nil
}

// copyRegularFile copies a single regular file along with its metadata.
func copyRegularFile(src *os.Root, dst *os.Root, path string, info fs.FileInfo, uid int64, gid int64) error {
	srcFile, err := src.Open(path)
	if err != nil {
		return err
	}

	defer logger.WarnOnError(srcFile.Close, "Failed to close source file")

	dstFile, err := dst.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}

	defer logger.WarnOnError(dstFile.Close, "Failed to close destination file")

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	err = copyMetadata(srcFile, dstFile, info, uid, gid)
	if err != nil {
		return fmt.Errorf("Failed setting metadata on %q: %w", path, err)
	}

	return nil
}

// copyMetadata applies ownership, mode, xattrs and times from src onto the open dst file.
func copyMetadata(src *os.File, dst *os.File, info fs.FileInfo, uid int64, gid int64) error {
	err := dst.Chown(int(uid), int(gid))
	if err != nil {
		return err
	}

	// Set the mode after the ownership as chown clears the setuid and setgid bits.
	err = dst.Chmod(info.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky))
	if err != nil {
		return err
	}

	err = copyXattrs(int(src.Fd()), int(dst.Fd()))
	if err != nil {
		return err
	}

	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}

	atime := time.Unix(st.Atim.Unix())
	mtime := time.Unix(st.Mtim.Unix())

	return unix.Futimes(int(dst.Fd()), []unix.Timeval{unix.NsecToTimeval(atime.UnixNano()), unix.NsecToTimeval(mtime.UnixNano())})
}

// copyXattrs copies all extended attributes between two open files.
func copyXattrs(srcFd int, dstFd int) error {
	size, err := unix.Flistxattr(srcFd, nil)
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			return nil
		}

		return err
	}

	if size == 0 {
		return nil
	}

	buf := make([]byte, size)
	size, err = unix.Flistxattr(srcFd, buf)
	if err != nil {
		return err
	}

	for _, name := range strings.Split(strings.TrimRight(string(buf[:size]), "\x00"), "\x00") {
		if name == "" {
			continue
		}

		valueSize, err := unix.Fgetxattr(srcFd, name, nil)
		if err != nil {
			return err
		}

		value := make([]byte, valueSize)
		valueSize, err = unix.Fgetxattr(srcFd, name, value)
		if err != nil {
			return err
		}

		err = unix.Fsetxattr(dstFd, name, value[:valueSize], 0)
		if err != nil {
			if errors.Is(err, unix.ENOTSUP) {
				continue
			}

			return fmt.Errorf("Failed setting xattr %q: %w", name, err)
		}
	}

	return nil
}
