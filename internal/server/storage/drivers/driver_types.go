package drivers

// Info represents information about a storage driver.
type Info struct {
	Name                         string
	Version                      string
	VolumeTypes                  []VolumeType // Supported volume types.
	DefaultVMBlockFilesystemSize string       // Default volume size for VM block filesystems.
	Buckets                      bool         // Buckets supported.
	Remote                       bool         // Whether the driver uses a remote backing store.
	VolumeMultiNode              bool         // Whether volumes can be used on multiple nodes concurrently.
	OptimizedImages              bool         // Whether driver stores images as separate volume.
	OptimizedBackups             bool         // Whether driver supports optimized volume backups.
	OptimizedBackupHeader        bool         // Whether driver generates an optimised backup header file in backup.
	PreservesInodes              bool         // Whether driver preserves inodes when volumes are moved hosts.
	BlockBacking                 bool         // Whether driver uses block devices as backing store.
	RunningCopyFreeze            bool         // Whether instance should be frozen during snapshot if running.
	DirectIO                     bool         // Whether the driver supports direct I/O.
	IOUring                      bool         // Whether the driver supports io_uring.
	MountedRoot                  bool         // Whether the pool directory itself is a mount.
}

// VolumeFiller provides a struct for filling a volume.
type VolumeFiller struct {
	Fill func(vol Volume, rootBlockPath string, allowUnsafeResize bool) (int64, error) // Function to fill the volume.
	Size int64                                                                         // Size of the unpacked volume in bytes.

	Fingerprint string // If the Filler will unpack an image, it should be this fingerprint.
}
