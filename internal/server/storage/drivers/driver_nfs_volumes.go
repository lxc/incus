package drivers

import (
	"io"

	"github.com/lxc/incus/v6/internal/server/migration"
	"github.com/lxc/incus/v6/internal/server/operations"
)

// MigrateVolume sends a volume for migration.
func (n *nfs) MigrateVolume(vol Volume, conn io.ReadWriteCloser, volSrcArgs *migration.VolumeSourceArgs, op *operations.Operation) error {
	if volSrcArgs.ClusterMove && !volSrcArgs.StorageMove {
		return nil
	}

	return genericVFSMigrateVolume(n, n.state, vol, conn, volSrcArgs, op)
}

// CreateVolumeFromMigration creates a new volume (with or without snapshots) from a migration data stream.
func (n *nfs) CreateVolumeFromMigration(vol Volume, conn io.ReadWriteCloser, volTargetArgs migration.VolumeTargetArgs, preFiller *VolumeFiller, op *operations.Operation) error {
	if volTargetArgs.ClusterMoveSourceName != "" && volTargetArgs.StoragePool == "" {
		return nil
	}

	return genericVFSCreateVolumeFromMigration(n, n.setupInitialQuota, vol, conn, volTargetArgs, preFiller, op)
}
