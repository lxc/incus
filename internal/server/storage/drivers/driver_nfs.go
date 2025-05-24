package drivers

import (
	"strings"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/migration"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	localMigration "github.com/lxc/incus/v6/internal/server/migration"
	"github.com/lxc/incus/v6/shared/util"
)

type nfs struct {
	dir
}

// Info returns info about the driver and its environment.
func (n *nfs) Info() Info {
	return Info{
		Name:                         "nfs",
		Version:                      "1",
		DefaultVMBlockFilesystemSize: deviceConfig.DefaultVMBlockFilesystemSize,
		OptimizedImages:              false,
		PreservesInodes:              false,
		Remote:                       n.isRemote(),
		VolumeTypes:                  []VolumeType{VolumeTypeBucket, VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		VolumeMultiNode:              n.isRemote(),
		BlockBacking:                 false,
		RunningCopyFreeze:            false,
		DirectIO:                     true,
		MountedRoot:                  true,
		Buckets:                      true,
	}
}

// isRemote returns true indicating this driver uses remote storage.
func (n *nfs) isRemote() bool {
	return true
}

// FillConfig populates the storage pool's configuration file with the default values.
func (n *nfs) FillConfig() error {
	uri := strings.Split(n.config["source"], ":")

	// URI should be first part of IP:PORT
	n.config["nfs.addr"] = uri[0]

	return nil
}

func (n *nfs) getMountOptions() string {
	// Allow overriding the default options.
	if n.config["nfs.mount_options"] != "" {
		return n.config["nfs.mount_options"]
	}
	// We only really support vers=4.2
	return "vers=4.2,addr=" + n.config["nfs.addr"]
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (n *nfs) Create() error {
	err := n.FillConfig()
	if err != nil {
		return err
	}

	sourcePath := n.config["source"]

	// Mount the nfs driver.
	mntFlags, mntOptions := linux.ResolveMountOptions(strings.Split(n.getMountOptions(), ","))
	err = TryMount(sourcePath, GetPoolMountPath(n.name), "nfs4", mntFlags, mntOptions)
	if err != nil {
		return err
	}

	defer func() { _, _ = forceUnmount(GetPoolMountPath(n.name)) }()

	return nil
}

func (n *nfs) Mount() (bool, error) {
	path := GetPoolMountPath(n.name)

	// Check if already mounted.
	if linux.IsMountPoint(path) {
		return false, nil
	}

	sourcePath := n.config["source"]
	n.config["nfs.path"] = sourcePath

	// Check if we're dealing with an external mount.
	if sourcePath == path {
		return false, nil
	}

	// Mount the nfs driver.
	mntFlags, mntOptions := linux.ResolveMountOptions(strings.Split(n.getMountOptions(), ","))
	err := TryMount(sourcePath, GetPoolMountPath(n.name), "nfs4", mntFlags, mntOptions)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (n *nfs) MigrationTypes(contentType ContentType, refresh bool, copySnapshots bool, clusterMove bool, storageMove bool) []localMigration.Type {
	// NFS does not support xattr
	rsyncFeatures := []string{"delete", "bidirectional"}
	if util.IsTrue(n.Config()["rsync.compression"]) {
		rsyncFeatures = append(rsyncFeatures, "compress")
	}

	return []localMigration.Type{
		{
			FSType:   migration.MigrationFSType_BLOCK_AND_RSYNC,
			Features: rsyncFeatures,
		},
		{
			FSType:   migration.MigrationFSType_RSYNC,
			Features: rsyncFeatures,
		},
	}
}
