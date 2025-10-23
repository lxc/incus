package drivers

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/migration"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	localMigration "github.com/lxc/incus/v6/internal/server/migration"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
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
		Buckets:                      false,
		SameSource:                   true,
		IgnoreCleanup:                true,
	}
}

// isRemote returns true indicating this driver uses remote storage.
func (n *nfs) isRemote() bool {
	return true
}

func (n *nfs) getMountOptions() string {
	// Allow overriding the default options.
	if n.config["nfs.mount_options"] != "" {
		return n.config["nfs.mount_options"]
	}
	// We only really support vers=4.2
	return fmt.Sprintf("vers=4.2,addr=%s", n.config["nfs.host"])
}

// Create is called during pool creation and is effectively using an empty driver struct.
// WARNING: The Create() function cannot rely on any of the struct attributes being set.
func (n *nfs) Create() error {
	if n.config["source"] == "" {
		return fmt.Errorf(`The "source" property must be defined`)
	}

	sourceStr := n.config["source"]

	// Taken from the truenas driver
	var host, path string
	if strings.HasPrefix(sourceStr, "[") {
		// IPv6 with brackets
		endBracket := strings.Index(sourceStr, "]")
		if endBracket == -1 || endBracket+1 >= len(sourceStr) || sourceStr[endBracket+1] != ':' {
			// Malformed, treat whole string as path
			host = ""
			path = sourceStr
		} else {
			host = sourceStr[:endBracket+1]
			path = sourceStr[endBracket+2:] // skip over "]:"
		}
	} else {
		// Try normal IPv4/hostname
		h, p, ok := strings.Cut(sourceStr, ":")
		if ok {
			host = h
			path = p
		} else {
			// No colon: whole thing is path
			host = ""
			path = sourceStr
		}
	}

	if path == "" {
		fmt.Println(filepath.IsAbs(path))
		return errors.New(`NFS driver requires "source" to be specified using the format: [<remote host>:]<remote path>`)
	}

	if host == "" {
		if n.config["nfs.host"] == "" {
			return errors.New(`NFS driver requires "nfs.host" to be specified or included in "source": [<remote host>:]<remote path>`)
		}

		host = n.config["nfs.host"]
	} else {
		n.config["nfs.host"] = host
	}

	n.config["source"] = fmt.Sprintf("%s:%s", host, path)
	n.config["nfs.path"] = path

	// Mount the nfs driver.
	mntFlags, mntOptions := linux.ResolveMountOptions(strings.Split(n.getMountOptions(), ","))
	err := TryMount(n.config["source"], GetPoolMountPath(n.name), "nfs4", mntFlags, mntOptions)
	if err != nil {
		return err
	}

	defer func() { _, _ = forceUnmount(GetPoolMountPath(n.name)) }()

	return nil
}

// Mount mounts the storage pool.
func (n *nfs) Mount() (bool, error) {
	path := GetPoolMountPath(n.name)

	// Check if already mounted.
	if linux.IsMountPoint(path) {
		return false, nil
	}

	sourcePath := n.config["source"]

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

// MigrationTypes returns the type of transfer methods to be used when doing migrations between pools in preference order.
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

// Validate checks that all provide keys are supported and that no conflicting or missing configuration is present.
func (n *nfs) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		// gendoc:generate(entity=storage_nfs, group=common, key=source)
		//
		// ---
		//  type: string
		//  scope: local
		//  default: -
		//  shortdesc: NFS remote storage path. Format: `[<host>:]<remote path>`. If `host` is omitted here, it must be set via `nfs.host`.
		"source": validate.IsAny, // can be used as a shortcut to specify dataset and optionally host.

		// gendoc:generate(entity=storage_nfs, group=common, key=nfs.host)
		//
		// ---
		//  type: string
		//  scope: global
		//  default: -
		//  shortdesc: Hostname or IP address of the remote NFS server. Optional if included in `source`, or a configuration is used.
		"nfs.host": validate.IsAny,

		// gendoc:generate(entity=storage_nfs, group=common, key=nfs.path)
		//
		// ---
		//  type: string
		//  scope: local
		//  default: -
		//  shortdesc: Remote NFS path. Typically inferred from `source`, but can be overridden.
		"nfs.path": validate.IsAny,

		// gendoc:generate(entity=storage_nfs, group=common, key=nfs.mount_options)
		//
		// ---
		//  type: string
		//  scope: local
		//  default: -
		//  shortdesc: Additional mount options for the NFS mount.
		"nfs.mount_options": validate.IsAny,
	}

	return n.validatePool(config, rules, map[string]func(value string) error{})
}
