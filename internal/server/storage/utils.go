package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/migration"
	"github.com/lxc/incus/internal/rsync"
	"github.com/lxc/incus/internal/server/apparmor"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/node"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/internal/server/storage/drivers"
	"github.com/lxc/incus/internal/server/sys"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/archive"
	"github.com/lxc/incus/shared/ioprogress"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

// ConfigDiff returns a diff of the provided configs. Additionally, it returns whether or not
// only user properties have been changed.
func ConfigDiff(oldConfig map[string]string, newConfig map[string]string) ([]string, bool) {
	changedConfig := []string{}
	userOnly := true
	for key := range oldConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !util.ValueInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key := range newConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !util.ValueInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Skip on no change
	if len(changedConfig) == 0 {
		return nil, false
	}

	return changedConfig, userOnly
}

// VolumeTypeNameToDBType converts a volume type string to internal volume type DB code.
func VolumeTypeNameToDBType(volumeTypeName string) (int, error) {
	switch volumeTypeName {
	case db.StoragePoolVolumeTypeNameContainer:
		return db.StoragePoolVolumeTypeContainer, nil
	case db.StoragePoolVolumeTypeNameVM:
		return db.StoragePoolVolumeTypeVM, nil
	case db.StoragePoolVolumeTypeNameImage:
		return db.StoragePoolVolumeTypeImage, nil
	case db.StoragePoolVolumeTypeNameCustom:
		return db.StoragePoolVolumeTypeCustom, nil
	}

	return -1, fmt.Errorf("Invalid storage volume type name")
}

// VolumeTypeToDBType converts volume type to internal volume type DB code.
func VolumeTypeToDBType(volType drivers.VolumeType) (int, error) {
	switch volType {
	case drivers.VolumeTypeContainer:
		return db.StoragePoolVolumeTypeContainer, nil
	case drivers.VolumeTypeVM:
		return db.StoragePoolVolumeTypeVM, nil
	case drivers.VolumeTypeImage:
		return db.StoragePoolVolumeTypeImage, nil
	case drivers.VolumeTypeCustom:
		return db.StoragePoolVolumeTypeCustom, nil
	}

	return -1, fmt.Errorf("Invalid storage volume type: %q", volType)
}

// VolumeDBTypeToType converts internal volume type DB code to storage driver volume type.
func VolumeDBTypeToType(volDBType int) (drivers.VolumeType, error) {
	switch volDBType {
	case db.StoragePoolVolumeTypeContainer:
		return drivers.VolumeTypeContainer, nil
	case db.StoragePoolVolumeTypeVM:
		return drivers.VolumeTypeVM, nil
	case db.StoragePoolVolumeTypeImage:
		return drivers.VolumeTypeImage, nil
	case db.StoragePoolVolumeTypeCustom:
		return drivers.VolumeTypeCustom, nil
	}

	return "", fmt.Errorf("Invalid storage volume DB type: %d", volDBType)
}

// InstanceTypeToVolumeType converts instance type to storage driver volume type.
func InstanceTypeToVolumeType(instType instancetype.Type) (drivers.VolumeType, error) {
	switch instType {
	case instancetype.Container:
		return drivers.VolumeTypeContainer, nil
	case instancetype.VM:
		return drivers.VolumeTypeVM, nil
	}

	return "", fmt.Errorf("Invalid instance type")
}

// VolumeTypeToAPIInstanceType converts storage driver volume type to API instance type type.
func VolumeTypeToAPIInstanceType(volType drivers.VolumeType) (api.InstanceType, error) {
	switch volType {
	case drivers.VolumeTypeContainer:
		return api.InstanceTypeContainer, nil
	case drivers.VolumeTypeVM:
		return api.InstanceTypeVM, nil
	}

	return api.InstanceTypeAny, fmt.Errorf("Volume type doesn't have equivalent instance type")
}

// VolumeContentTypeToDBContentType converts volume type to internal code.
func VolumeContentTypeToDBContentType(contentType drivers.ContentType) (int, error) {
	switch contentType {
	case drivers.ContentTypeBlock:
		return db.StoragePoolVolumeContentTypeBlock, nil
	case drivers.ContentTypeFS:
		return db.StoragePoolVolumeContentTypeFS, nil
	case drivers.ContentTypeISO:
		return db.StoragePoolVolumeContentTypeISO, nil
	}

	return -1, fmt.Errorf("Invalid volume content type")
}

// VolumeDBContentTypeToContentType converts internal content type DB code to driver representation.
func VolumeDBContentTypeToContentType(volDBType int) (drivers.ContentType, error) {
	switch volDBType {
	case db.StoragePoolVolumeContentTypeBlock:
		return drivers.ContentTypeBlock, nil
	case db.StoragePoolVolumeContentTypeFS:
		return drivers.ContentTypeFS, nil
	case db.StoragePoolVolumeContentTypeISO:
		return drivers.ContentTypeISO, nil
	}

	return "", fmt.Errorf("Invalid volume content type")
}

// VolumeContentTypeNameToContentType converts volume content type string internal code.
func VolumeContentTypeNameToContentType(contentTypeName string) (int, error) {
	switch contentTypeName {
	case db.StoragePoolVolumeContentTypeNameFS:
		return db.StoragePoolVolumeContentTypeFS, nil
	case db.StoragePoolVolumeContentTypeNameBlock:
		return db.StoragePoolVolumeContentTypeBlock, nil
	case db.StoragePoolVolumeContentTypeNameISO:
		return db.StoragePoolVolumeContentTypeISO, nil
	}

	return -1, fmt.Errorf("Invalid volume content type name")
}

// VolumeDBGet loads a volume from the database.
func VolumeDBGet(pool Pool, projectName string, volumeName string, volumeType drivers.VolumeType) (*db.StorageVolume, error) {
	p, ok := pool.(*backend)
	if !ok {
		return nil, fmt.Errorf("Pool is not a backend")
	}

	volDBType, err := VolumeTypeToDBType(volumeType)
	if err != nil {
		return nil, err
	}

	// Get the storage volume.
	var dbVolume *db.StorageVolume
	err = p.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, pool.ID(), projectName, volDBType, volumeName, true)
		if err != nil {
			if response.IsNotFoundError(err) {
				return fmt.Errorf("Storage volume %q in project %q of type %q does not exist on pool %q: %w", volumeName, projectName, volumeType, pool.Name(), err)
			}

			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return dbVolume, nil
}

// VolumeDBCreate creates a volume in the database.
// If volumeConfig is supplied, it is modified with any driver level default config options (if not set).
// If removeUnknownKeys is true, any unknown config keys are removed from volumeConfig rather than failing.
func VolumeDBCreate(pool Pool, projectName string, volumeName string, volumeDescription string, volumeType drivers.VolumeType, snapshot bool, volumeConfig map[string]string, creationDate time.Time, expiryDate time.Time, contentType drivers.ContentType, removeUnknownKeys bool, hasSource bool) error {
	p, ok := pool.(*backend)
	if !ok {
		return fmt.Errorf("Pool is not a backend")
	}

	// Prevent using this function to create storage volume bucket records.
	if volumeType == drivers.VolumeTypeBucket {
		return fmt.Errorf("Cannot store volume using bucket type")
	}

	// If the volumeType represents an instance type then check that the volumeConfig doesn't contain any of
	// the instance disk effective override fields (which should not be stored in the database).
	if volumeType.IsInstance() {
		for _, k := range instanceDiskVolumeEffectiveFields {
			_, found := volumeConfig[k]
			if found {
				return fmt.Errorf("Instance disk effective override field %q should not be stored in volume config", k)
			}
		}
	}

	// Convert the volume type to our internal integer representation.
	volDBType, err := VolumeTypeToDBType(volumeType)
	if err != nil {
		return err
	}

	volDBContentType, err := VolumeContentTypeToDBContentType(contentType)
	if err != nil {
		return err
	}

	// Make sure that we don't pass a nil to the next function.
	if volumeConfig == nil {
		volumeConfig = map[string]string{}
	}

	volType, err := VolumeDBTypeToType(volDBType)
	if err != nil {
		return err
	}

	vol := drivers.NewVolume(pool.Driver(), pool.Name(), volType, contentType, volumeName, volumeConfig, pool.Driver().Config())

	// Set source indicator.
	vol.SetHasSource(hasSource)

	// Fill default config.
	err = pool.Driver().FillVolumeConfig(vol)
	if err != nil {
		return err
	}

	// Validate config.
	err = pool.Driver().ValidateVolume(vol, removeUnknownKeys)
	if err != nil {
		return err
	}

	err = p.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the database entry for the storage volume.
		if snapshot {
			_, err = tx.CreateStorageVolumeSnapshot(ctx, projectName, volumeName, volumeDescription, volDBType, pool.ID(), vol.Config(), creationDate, expiryDate)
		} else {
			_, err = tx.CreateStoragePoolVolume(ctx, projectName, volumeName, volumeDescription, volDBType, pool.ID(), vol.Config(), volDBContentType, creationDate)
		}

		return err
	})
	if err != nil {
		return fmt.Errorf("Error inserting volume %q for project %q in pool %q of type %q into database %q", volumeName, projectName, pool.Name(), volumeType, err)
	}

	return nil
}

// VolumeDBDelete deletes a volume from the database.
func VolumeDBDelete(pool Pool, projectName string, volumeName string, volumeType drivers.VolumeType) error {
	p, ok := pool.(*backend)
	if !ok {
		return fmt.Errorf("Pool is not a backend")
	}

	// Convert the volume type to our internal integer representation.
	volDBType, err := VolumeTypeToDBType(volumeType)
	if err != nil {
		return err
	}

	err = p.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RemoveStoragePoolVolume(ctx, projectName, volumeName, volDBType, pool.ID())
	})
	if err != nil && !response.IsNotFoundError(err) {
		return fmt.Errorf("Error deleting storage volume from database: %w", err)
	}

	return nil
}

// VolumeDBSnapshotsGet loads a list of snapshots volumes from the database.
func VolumeDBSnapshotsGet(pool Pool, projectName string, volume string, volumeType drivers.VolumeType) ([]db.StorageVolumeArgs, error) {
	p, ok := pool.(*backend)
	if !ok {
		return nil, fmt.Errorf("Pool is not a backend")
	}

	volDBType, err := VolumeTypeToDBType(volumeType)
	if err != nil {
		return nil, err
	}

	var snapshots []db.StorageVolumeArgs

	err = p.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		snapshots, err = tx.GetLocalStoragePoolVolumeSnapshotsWithType(ctx, projectName, volume, volDBType, pool.ID())

		return err
	})
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}

// BucketDBGet loads a bucket from the database.
func BucketDBGet(pool Pool, projectName string, bucketName string, memberSpecific bool) (*db.StorageBucket, error) {
	p, ok := pool.(*backend)
	if !ok {
		return nil, fmt.Errorf("Pool is not a backend")
	}

	var err error
	var bucket *db.StorageBucket

	// Get the storage bucket.
	err = p.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err = tx.GetStoragePoolBucket(ctx, pool.ID(), projectName, memberSpecific, bucketName)
		if err != nil {
			if response.IsNotFoundError(err) {
				return fmt.Errorf("Storage bucket %q in project %q does not exist on pool %q: %w", bucketName, projectName, pool.Name(), err)
			}

			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return bucket, nil
}

// BucketDBCreate creates a bucket in the database.
// The supplied bucket's config may be modified with defaults for the storage pool being used.
// Returns bucket DB record ID.
func BucketDBCreate(ctx context.Context, pool Pool, projectName string, memberSpecific bool, bucket *api.StorageBucketsPost) (int64, error) {
	p, ok := pool.(*backend)
	if !ok {
		return -1, fmt.Errorf("Pool is not a backend")
	}

	// Make sure that we don't pass a nil to the next function.
	if bucket.Config == nil {
		bucket.Config = map[string]string{}
	}

	bucketVolName := project.StorageVolume(projectName, bucket.Name)
	bucketVol := drivers.NewVolume(pool.Driver(), pool.Name(), drivers.VolumeTypeBucket, drivers.ContentTypeFS, bucketVolName, bucket.Config, pool.Driver().Config())

	// Fill default config.
	err := pool.Driver().FillVolumeConfig(bucketVol)
	if err != nil {
		return -1, err
	}

	// Validate bucket name.
	err = pool.Driver().ValidateBucket(bucketVol)
	if err != nil {
		return -1, err
	}

	// Validate bucket volume config.
	err = pool.Driver().ValidateVolume(bucketVol, false)
	if err != nil {
		return -1, err
	}

	var bucketID int64

	err = p.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the database entry for the storage bucket.
		bucketID, err = tx.CreateStoragePoolBucket(ctx, p.ID(), projectName, memberSpecific, *bucket)

		return err
	})
	if err != nil {
		return -1, fmt.Errorf("Failed inserting storage bucket %q for project %q in pool %q into database: %w", bucket.Name, projectName, pool.Name(), err)
	}

	return bucketID, nil
}

// BucketDBDelete deletes a bucket from the database.
func BucketDBDelete(ctx context.Context, pool Pool, bucketID int64) error {
	p, ok := pool.(*backend)
	if !ok {
		return fmt.Errorf("Pool is not a backend")
	}

	err := p.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.DeleteStoragePoolBucket(ctx, p.ID(), bucketID)
	})
	if err != nil && !response.IsNotFoundError(err) {
		return fmt.Errorf("Failed deleting storage bucket from database: %w", err)
	}

	return nil
}

// poolAndVolumeCommonRules returns a map of pool and volume config common rules common to all drivers.
// When vol argument is nil function returns pool specific rules.
func poolAndVolumeCommonRules(vol *drivers.Volume) map[string]func(string) error {
	rules := map[string]func(string) error{
		// Note: size should not be modifiable for non-custom volumes and should be checked
		// in the relevant volume update functions.
		"size": validate.Optional(validate.IsSize),
		"snapshots.expiry": func(value string) error {
			// Validate expression
			_, err := internalInstance.GetExpiry(time.Time{}, value)
			return err
		},
		"snapshots.schedule": validate.Optional(validate.IsCron([]string{"@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@annually", "@yearly"})),
		"snapshots.pattern":  validate.IsAny,
	}

	// security.shifted and security.unmapped are only relevant for custom filesystem volumes.
	if (vol == nil) || (vol != nil && vol.Type() == drivers.VolumeTypeCustom && vol.ContentType() == drivers.ContentTypeFS) {
		rules["security.shifted"] = validate.Optional(validate.IsBool)
		rules["security.unmapped"] = validate.Optional(validate.IsBool)
	}

	return rules
}

// validatePoolCommonRules returns a map of pool config rules common to all drivers.
func validatePoolCommonRules() map[string]func(string) error {
	rules := map[string]func(string) error{
		"source":                  validate.IsAny,
		"source.wipe":             validate.Optional(validate.IsBool),
		"volatile.initial_source": validate.IsAny,
		"rsync.bwlimit":           validate.Optional(validate.IsSize),
		"rsync.compression":       validate.Optional(validate.IsBool),
	}

	// Add to pool config rules (prefixed with volume.*) which are common for pool and volume.
	for volRule, volValidator := range poolAndVolumeCommonRules(nil) {
		rules[fmt.Sprintf("volume.%s", volRule)] = volValidator
	}

	return rules
}

// validateVolumeCommonRules returns a map of volume config rules common to all drivers.
func validateVolumeCommonRules(vol drivers.Volume) map[string]func(string) error {
	rules := poolAndVolumeCommonRules(&vol)

	// volatile.idmap settings only make sense for filesystem volumes.
	if vol.ContentType() == drivers.ContentTypeFS {
		rules["volatile.idmap.last"] = validate.IsAny
		rules["volatile.idmap.next"] = validate.IsAny
	}

	// block.mount_options and block.filesystem settings are only relevant for drivers that are block backed
	// and when there is a filesystem to actually mount. This includes filesystem volumes and VM Block volumes,
	// as they have an associated config filesystem volume that shares the config.
	if vol.IsBlockBacked() && (vol.ContentType() == drivers.ContentTypeFS || vol.IsVMBlock()) {
		rules["block.mount_options"] = validate.IsAny

		// Note: block.filesystem should not be modifiable after volume created.
		// This should be checked in the relevant volume update functions.
		rules["block.filesystem"] = validate.IsAny
	}

	// volatile.rootfs.size is only used for image volumes.
	if vol.Type() == drivers.VolumeTypeImage {
		rules["volatile.rootfs.size"] = validate.Optional(validate.IsInt64)
	}

	return rules
}

// ImageUnpack unpacks a filesystem image into the destination path.
// There are several formats that images can come in:
// Container Format A: Separate metadata tarball and root squashfs file.
//   - Unpack metadata tarball into mountPath.
//   - Unpack root squashfs file into mountPath/rootfs.
//
// Container Format B: Combined tarball containing metadata files and root squashfs.
//   - Unpack combined tarball into mountPath.
//
// VM Format A: Separate metadata tarball and root qcow2 file.
//   - Unpack metadata tarball into mountPath.
//   - Check rootBlockPath is a file and convert qcow2 file into raw format in rootBlockPath.
func ImageUnpack(imageFile string, vol drivers.Volume, destBlockFile string, sysOS *sys.OS, allowUnsafeResize bool, tracker *ioprogress.ProgressTracker) (int64, error) {
	l := logger.Log.AddContext(logger.Ctx{"imageFile": imageFile, "volName": vol.Name()})
	l.Info("Image unpack started")
	defer l.Info("Image unpack stopped")

	// Check available memory.
	maxMemory, err := linux.DeviceTotalMemory()
	if err == nil {
		// Cap the memory to 10%.
		maxMemory = maxMemory / 10
	} else {
		maxMemory = 0
	}

	// For all formats, first unpack the metadata (or combined) tarball into destPath.
	imageRootfsFile := imageFile + ".rootfs"
	destPath := vol.MountPath()

	// If no destBlockFile supplied then this is a container image unpack.
	if destBlockFile == "" {
		rootfsPath := filepath.Join(destPath, "rootfs")

		// Unpack the main image file.
		err := archive.Unpack(imageFile, destPath, vol.IsBlockBacked(), maxMemory, tracker)
		if err != nil {
			return -1, err
		}

		// Check for separate root file.
		if util.PathExists(imageRootfsFile) {
			err = os.MkdirAll(rootfsPath, 0755)
			if err != nil {
				return -1, fmt.Errorf("Error creating rootfs directory")
			}

			err = archive.Unpack(imageRootfsFile, rootfsPath, vol.IsBlockBacked(), maxMemory, tracker)
			if err != nil {
				return -1, err
			}
		}

		// Check that the container image unpack has resulted in a rootfs dir.
		if !util.PathExists(rootfsPath) {
			return -1, fmt.Errorf("Image is missing a rootfs: %s", imageFile)
		}

		// Done with this.
		return 0, nil
	}

	// If a rootBlockPath is supplied then this is a VM image unpack.

	// Validate the target.
	fileInfo, err := os.Stat(destBlockFile)
	if err != nil && !os.IsNotExist(err) {
		return -1, err
	}

	if fileInfo != nil && fileInfo.IsDir() {
		// If the dest block file exists, and it is a directory, fail.
		return -1, fmt.Errorf("Root block path isn't a file: %s", destBlockFile)
	}

	// convertBlockImage converts the qcow2 block image file into a raw block device. If needed it will attempt
	// to enlarge the destination volume to accommodate the unpacked qcow2 image file.
	convertBlockImage := func(v drivers.Volume, imgPath string, dstPath string) (int64, error) {
		// Get info about qcow2 file. Force input format to qcow2 so we don't rely on qemu-img's detection
		// logic as that has been known to have vulnerabilities and we only support qcow2 images anyway.
		// Use prlimit because qemu-img can consume considerable RAM & CPU time if fed a maliciously
		// crafted disk image. Since cloud tenants are not to be trusted, ensure QEMU is limits to 1 GiB
		// address space and 2 seconds CPU time, which ought to be more than enough for real world images.
		cmd := []string{"prlimit", "--cpu=2", "--as=1073741824", "qemu-img", "info", "-f", "qcow2", "--output=json", imgPath}
		imgJSON, err := apparmor.QemuImg(sysOS, cmd, imgPath, dstPath)
		if err != nil {
			return -1, fmt.Errorf("Failed reading image info %q: %w", imgPath, err)
		}

		imgInfo := struct {
			Format      string `json:"format"`
			VirtualSize int64  `json:"virtual-size"`
		}{}

		err = json.Unmarshal([]byte(imgJSON), &imgInfo)
		if err != nil {
			return -1, fmt.Errorf("Failed unmarshalling image info %q: %w (%q)", imgPath, err, imgJSON)
		}

		// Belt and braces qcow2 check.
		if imgInfo.Format != "qcow2" {
			return -1, fmt.Errorf("Unexpected image format %q", imgInfo.Format)
		}

		// Check whether image is allowed to be unpacked into pool volume. Create a partial image volume
		// struct and then use it to check that target volume size can be set as needed.
		imgVolConfig := map[string]string{
			"volatile.rootfs.size": fmt.Sprintf("%d", imgInfo.VirtualSize),
		}

		imgVol := drivers.NewVolume(nil, "", drivers.VolumeTypeImage, drivers.ContentTypeBlock, "", imgVolConfig, nil)

		l.Debug("Checking image unpack size")
		newVolSize, err := vol.ConfigSizeFromSource(imgVol)
		if err != nil {
			return -1, err
		}

		if util.PathExists(dstPath) {
			volSizeBytes, err := drivers.BlockDiskSizeBytes(dstPath)
			if err != nil {
				return -1, fmt.Errorf("Error getting current size of %q: %w", dstPath, err)
			}

			// If the target volume's size is smaller than the image unpack size, then we need to
			// increase the target volume's size.
			if volSizeBytes < imgInfo.VirtualSize {
				l.Debug("Increasing volume size", logger.Ctx{"imgPath": imgPath, "dstPath": dstPath, "oldSize": volSizeBytes, "newSize": newVolSize, "allowUnsafeResize": allowUnsafeResize})
				err = vol.SetQuota(newVolSize, allowUnsafeResize, nil)
				if err != nil {
					return -1, fmt.Errorf("Error increasing volume size: %w", err)
				}
			}
		}

		// Convert the qcow2 format to a raw block device.
		l.Debug("Converting qcow2 image to raw disk", logger.Ctx{"imgPath": imgPath, "dstPath": dstPath})

		cmd = []string{
			"nice", "-n19", // Run with low priority to reduce CPU impact on other processes.
			"qemu-img", "convert", "-f", "qcow2", "-O", "raw",
		}

		// Check for Direct I/O support.
		from, err := os.OpenFile(imgPath, unix.O_DIRECT|unix.O_RDONLY, 0)
		if err == nil {
			cmd = append(cmd, "-T", "none")
			_ = from.Close()
		}

		to, err := os.OpenFile(dstPath, unix.O_DIRECT|unix.O_RDONLY, 0)
		if err == nil {
			cmd = append(cmd, "-t", "none")
			_ = to.Close()
		}

		// Check if we should do parallel unpacking.
		if linux.IsBlockdevPath(dstPath) {
			cmd = append(cmd, "-W")
		}

		cmd = append(cmd, imgPath, dstPath)

		_, err = apparmor.QemuImg(sysOS, cmd, imgPath, dstPath)

		if err != nil {
			return -1, fmt.Errorf("Failed converting image to raw at %q: %w", dstPath, err)
		}

		return imgInfo.VirtualSize, nil
	}

	var imgSize int64

	if util.PathExists(imageRootfsFile) {
		// Unpack the main image file.
		err := archive.Unpack(imageFile, destPath, vol.IsBlockBacked(), maxMemory, tracker)
		if err != nil {
			return -1, err
		}

		// Convert the qcow2 format to a raw block device.
		imgSize, err = convertBlockImage(vol, imageRootfsFile, destBlockFile)
		if err != nil {
			return -1, err
		}
	} else {
		// Dealing with unified tarballs require an initial unpack to a temporary directory.
		tempDir, err := os.MkdirTemp(internalUtil.VarPath("images"), "incus_image_unpack_")
		if err != nil {
			return -1, err
		}

		defer func() { _ = os.RemoveAll(tempDir) }()

		// Unpack the whole image.
		err = archive.Unpack(imageFile, tempDir, vol.IsBlockBacked(), maxMemory, tracker)
		if err != nil {
			return -1, err
		}

		imgPath := filepath.Join(tempDir, "rootfs.img")

		// Convert the qcow2 format to a raw block device.
		imgSize, err = convertBlockImage(vol, imgPath, destBlockFile)
		if err != nil {
			return -1, err
		}

		// Delete the qcow2.
		err = os.Remove(imgPath)
		if err != nil {
			return -1, fmt.Errorf("Failed to remove %q: %w", imgPath, err)
		}

		// Transfer the content excluding the destBlockFile name so that we don't delete the block file
		// created above if the storage driver stores image files in the same directory as destPath.
		_, err = rsync.LocalCopy(tempDir, destPath, "", true, "--exclude", filepath.Base(destBlockFile))
		if err != nil {
			return -1, err
		}
	}

	return imgSize, nil
}

// InstanceContentType returns the instance's content type.
func InstanceContentType(inst instance.Instance) drivers.ContentType {
	contentType := drivers.ContentTypeFS
	if inst.Type() == instancetype.VM {
		contentType = drivers.ContentTypeBlock
	}

	return contentType
}

// VolumeUsedByProfileDevices finds profiles using a volume and passes them to profileFunc for evaluation.
// The profileFunc is provided with a profile config, project config and a list of device names that are using
// the volume.
func VolumeUsedByProfileDevices(s *state.State, poolName string, projectName string, vol *api.StorageVolume, profileFunc func(profileID int64, profile api.Profile, project api.Project, usedByDevices []string) error) error {
	// Convert the volume type name to our internal integer representation.
	volumeType, err := VolumeTypeNameToDBType(vol.Type)
	if err != nil {
		return err
	}

	var profiles []api.Profile
	var profileIDs []int64
	var profileProjects []*api.Project
	// Retrieve required info from the database in single transaction for performance.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err := cluster.GetProjects(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading projects: %w", err)
		}

		// Index of all projects by name.
		projectMap := make(map[string]*api.Project, len(projects))
		for _, project := range projects {
			projectMap[project.Name], err = project.ToAPI(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading config for projec %q: %w", project.Name, err)
			}
		}

		dbProfiles, err := cluster.GetProfiles(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading profiles: %w", err)
		}

		for _, profile := range dbProfiles {
			apiProfile, err := profile.ToAPI(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed getting API Profile %q: %w", profile.Name, err)
			}

			profileIDs = append(profileIDs, int64(profile.ID))
			profiles = append(profiles, *apiProfile)
		}

		profileProjects = make([]*api.Project, len(dbProfiles))
		for i, p := range dbProfiles {
			profileProjects[i] = projectMap[p.Project]
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Iterate all profiles, consider only those which belong to a project that has the same effective
	// storage project as volume.
	for i, profile := range profiles {
		profileStorageProject := project.StorageVolumeProjectFromRecord(profileProjects[i], volumeType)
		if err != nil {
			return err
		}

		// Check profile's storage project is the same as the volume's project.
		// If not then the volume names mentioned in the profile's config cannot be referring to volumes
		// in the volume's project we are trying to match, and this profile cannot possibly be using it.
		if projectName != profileStorageProject {
			continue
		}

		var usedByDevices []string

		// Iterate through each of the profiles's devices, looking for disks in the same pool as volume.
		// Then try and match the volume name against the profile device's "source" property.
		for name, dev := range profile.Devices {
			if dev["type"] != cluster.TypeDisk.String() {
				continue
			}

			if dev["pool"] != poolName {
				continue
			}

			if dev["source"] == vol.Name {
				usedByDevices = append(usedByDevices, name)
			}
		}

		if len(usedByDevices) > 0 {
			err = profileFunc(profileIDs[i], profile, *profileProjects[i], usedByDevices)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// VolumeUsedByInstanceDevices finds instances using a volume (either directly or via their expanded profiles if
// expandDevices is true) and passes them to instanceFunc for evaluation. If instanceFunc returns an error then it
// is returned immediately. The instanceFunc is executed during a DB transaction, so DB queries are not permitted.
// The instanceFunc is provided with a instance config, project config, instance's profiles and a list of device
// names that are using the volume.
func VolumeUsedByInstanceDevices(s *state.State, poolName string, projectName string, vol *api.StorageVolume, expandDevices bool, instanceFunc func(inst db.InstanceArgs, project api.Project, usedByDevices []string) error) error {
	// Convert the volume type name to our internal integer representation.
	volumeType, err := VolumeTypeNameToDBType(vol.Type)
	if err != nil {
		return err
	}

	return s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
			// If the volume has a specific cluster member which is different than the instance then skip as
			// instance cannot be using this volume.
			if vol.Location != "" && inst.Node != vol.Location {
				return nil
			}

			instStorageProject := project.StorageVolumeProjectFromRecord(&p, volumeType)
			if err != nil {
				return err
			}

			// Check instance's storage project is the same as the volume's project.
			// If not then the volume names mentioned in the instance's config cannot be referring to volumes
			// in the volume's project we are trying to match, and this instance cannot possibly be using it.
			if projectName != instStorageProject {
				return nil
			}

			// Use local devices for usage check by if expandDevices is false (but don't modify instance).
			devices := inst.Devices

			// Expand devices for usage check if expandDevices is true.
			if expandDevices {
				devices = db.ExpandInstanceDevices(devices.Clone(), inst.Profiles)
			}

			var usedByDevices []string

			// Iterate through each of the instance's devices, looking for disks in the same pool as volume.
			// Then try and match the volume name against the instance device's "source" property.
			for devName, dev := range devices {
				if dev["type"] != "disk" {
					continue
				}

				if dev["pool"] != poolName {
					continue
				}

				if dev["source"] == vol.Name {
					usedByDevices = append(usedByDevices, devName)
				}
			}

			if len(usedByDevices) > 0 {
				err = instanceFunc(inst, p, usedByDevices)
				if err != nil {
					return err
				}
			}

			return nil
		})
	})
}

// VolumeUsedByExclusiveRemoteInstancesWithProfiles checks if custom volume is exclusively attached to a remote
// instance. Returns the remote instance that has the volume exclusively attached. Returns nil if volume available.
func VolumeUsedByExclusiveRemoteInstancesWithProfiles(s *state.State, poolName string, projectName string, vol *api.StorageVolume) (*db.InstanceArgs, error) {
	pool, err := LoadByName(s, poolName)
	if err != nil {
		return nil, fmt.Errorf("Failed loading storage pool %q: %w", poolName, err)
	}

	info := pool.Driver().Info()

	// Always return nil if the storage driver supports mounting volumes on multiple nodes at once.
	if info.VolumeMultiNode {
		return nil, nil
	}

	// Find if volume is attached to a remote instance.
	var remoteInstance *db.InstanceArgs
	err = VolumeUsedByInstanceDevices(s, poolName, projectName, vol, true, func(dbInst db.InstanceArgs, project api.Project, usedByDevices []string) error {
		if dbInst.Node != s.ServerName {
			remoteInstance = &dbInst
			return db.ErrInstanceListStop // Stop the search, this volume is attached to a remote instance.
		}

		return nil
	})
	if err != nil && err != db.ErrInstanceListStop {
		return nil, err
	}

	return remoteInstance, nil
}

// VolumeUsedByDaemon indicates whether the volume is used by daemon storage.
func VolumeUsedByDaemon(s *state.State, poolName string, volumeName string) (bool, error) {
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
		return false, err
	}

	fullName := fmt.Sprintf("%s/%s", poolName, volumeName)
	if storageBackups == fullName || storageImages == fullName {
		return true, nil
	}

	return false, nil
}

// FallbackMigrationType returns the fallback migration transport to use based on volume content type.
func FallbackMigrationType(contentType drivers.ContentType) migration.MigrationFSType {
	if drivers.IsContentBlock(contentType) {
		return migration.MigrationFSType_BLOCK_AND_RSYNC
	}

	return migration.MigrationFSType_RSYNC
}

// RenderSnapshotUsage can be used as an optional argument to Instance.Render() to return snapshot usage.
// As this is a relatively expensive operation it is provided as an optional feature rather than on by default.
func RenderSnapshotUsage(s *state.State, snapInst instance.Instance) func(response any) error {
	return func(response any) error {
		apiRes, ok := response.(*api.InstanceSnapshot)
		if !ok {
			return nil
		}

		pool, err := LoadByInstance(s, snapInst)
		if err == nil {
			// It is important that the snapshot not be mounted here as mounting a snapshot can trigger a very
			// expensive filesystem UUID regeneration, so we rely on the driver implementation to get the info
			// we are requesting as cheaply as possible.
			volumeState, err := pool.GetInstanceUsage(snapInst)
			if err == nil {
				apiRes.Size = volumeState.Used
			}
		}

		return nil
	}
}

// InstanceMount mounts an instance's storage volume (if not already mounted).
// Please call InstanceUnmount when finished.
func InstanceMount(pool Pool, inst instance.Instance, op *operations.Operation) (*MountInfo, error) {
	var err error
	var mountInfo *MountInfo

	if inst.IsSnapshot() {
		mountInfo, err = pool.MountInstanceSnapshot(inst, op)
		if err != nil {
			return nil, err
		}
	} else {
		mountInfo, err = pool.MountInstance(inst, op)
		if err != nil {
			return nil, err
		}
	}

	return mountInfo, nil
}

// InstanceUnmount unmounts an instance's storage volume (if not in use).
func InstanceUnmount(pool Pool, inst instance.Instance, op *operations.Operation) error {
	var err error

	if inst.IsSnapshot() {
		err = pool.UnmountInstanceSnapshot(inst, op)
	} else {
		err = pool.UnmountInstance(inst, op)
	}

	return err
}

// InstanceDiskBlockSize returns the block device size for the instance's disk.
// This will mount the instance if not already mounted and will unmount at the end if needed.
func InstanceDiskBlockSize(pool Pool, inst instance.Instance, op *operations.Operation) (int64, error) {
	mountInfo, err := InstanceMount(pool, inst, op)
	if err != nil {
		return -1, err
	}

	defer func() { _ = InstanceUnmount(pool, inst, op) }()

	if mountInfo.DiskPath == "" {
		return -1, fmt.Errorf("No disk path available from mount")
	}

	blockDiskSize, err := drivers.BlockDiskSizeBytes(mountInfo.DiskPath)
	if err != nil {
		return -1, fmt.Errorf("Error getting block disk size %q: %w", mountInfo.DiskPath, err)
	}

	return blockDiskSize, nil
}

// ComparableSnapshot is used when comparing snapshots on different pools to see whether they differ.
type ComparableSnapshot struct {
	// Name of the snapshot (without the parent name).
	Name string

	// Identifier of the snapshot (that remains the same when copied between pools).
	ID string

	// Creation date time of the snapshot.
	CreationDate time.Time
}

// CompareSnapshots returns a list of snapshot indexes (from the associated input slices) to sync from the source
// and to delete from the target respectively.
// A snapshot will be added to "to sync from source" slice if it either doesn't exist in the target or its ID or
// creation date is different to the source.
// A snapshot will be added to the "to delete from target" slice if it doesn't exist in the source or its ID or
// creation date is different to the source.
func CompareSnapshots(sourceSnapshots []ComparableSnapshot, targetSnapshots []ComparableSnapshot) ([]int, []int) {
	// Compare source and target.
	sourceSnapshotsByName := make(map[string]*ComparableSnapshot, len(sourceSnapshots))
	targetSnapshotsByName := make(map[string]*ComparableSnapshot, len(targetSnapshots))

	var syncFromSource, deleteFromTarget []int

	// Generate a list of source snapshots by name.
	for sourceSnapIndex := range sourceSnapshots {
		sourceSnapshotsByName[sourceSnapshots[sourceSnapIndex].Name] = &sourceSnapshots[sourceSnapIndex]
	}

	// If target snapshot doesn't exist in source, or its creation date or ID differ,
	// then mark it for deletion on target.
	for targetSnapIndex := range targetSnapshots {
		// Generate a list of target snapshots by name for later comparison.
		targetSnapshotsByName[targetSnapshots[targetSnapIndex].Name] = &targetSnapshots[targetSnapIndex]

		sourceSnap, sourceSnapExists := sourceSnapshotsByName[targetSnapshots[targetSnapIndex].Name]
		if !sourceSnapExists || !sourceSnap.CreationDate.Equal(targetSnapshots[targetSnapIndex].CreationDate) || sourceSnap.ID != targetSnapshots[targetSnapIndex].ID {
			deleteFromTarget = append(deleteFromTarget, targetSnapIndex)
		}
	}

	// If source snapshot doesn't exist in target, or its creation date or ID differ,
	// then mark it for syncing to target.
	for sourceSnapIndex := range sourceSnapshots {
		targetSnap, targetSnapExists := targetSnapshotsByName[sourceSnapshots[sourceSnapIndex].Name]
		if !targetSnapExists || !targetSnap.CreationDate.Equal(sourceSnapshots[sourceSnapIndex].CreationDate) || targetSnap.ID != sourceSnapshots[sourceSnapIndex].ID {
			syncFromSource = append(syncFromSource, sourceSnapIndex)
		}
	}

	return syncFromSource, deleteFromTarget
}
