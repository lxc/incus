//go:build linux && cgo && !agent

package storage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/internal/server/storage/drivers"
	"github.com/lxc/incus/v7/shared/logger"
)

func TestPatchMissingSnapshotRecordsCommitsRepairs(t *testing.T) {
	s, cleanup := state.NewTestState(t)
	defer cleanup()

	previousRemoteDrivers := db.StorageRemoteDriverNames
	db.StorageRemoteDriverNames = func() []string { return nil }
	t.Cleanup(func() { db.StorageRemoteDriverNames = previousRemoteDrivers })

	var poolID int64
	created := time.Unix(1700000000, 0).UTC()
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		poolID, err = tx.CreateStoragePool(ctx, "repair", "", "dir", nil)
		if err != nil {
			return err
		}

		_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
			Project: "default", Name: "c1", Node: "none", Type: instancetype.Container, Architecture: 1,
		})
		if err != nil {
			return err
		}

		_, err = tx.CreateStoragePoolVolume(ctx, "default", "c1", "original", db.StoragePoolVolumeTypeContainer, poolID, map[string]string{"user.preserved": "original"}, db.StoragePoolVolumeContentTypeFS, created)
		if err != nil {
			return err
		}

		for _, name := range []string{"existing", "missing0", "missing1"} {
			_, err = cluster.CreateInstanceSnapshot(ctx, tx.Tx(), cluster.InstanceSnapshot{
				Project: "default", Instance: "c1", Name: name, CreationDate: created,
			})
			if err != nil {
				return err
			}
		}

		_, err = tx.CreateStorageVolumeSnapshot(ctx, "default", "c1/existing", "preserved", db.StoragePoolVolumeTypeContainer, poolID, map[string]string{"user.preserved": "existing"}, created, created.Add(time.Hour))
		return err
	})
	require.NoError(t, err)

	driver, err := drivers.Load(s, "dir", "repair", nil, logger.Log, nil, nil)
	require.NoError(t, err)
	pool := &backend{id: poolID, name: "repair", state: s, driver: driver, logger: logger.Log}

	for range 2 {
		require.NoError(t, patchMissingSnapshotRecords(pool))
		snapshots, err := VolumeDBSnapshotsGet(pool, "default", "c1", drivers.VolumeTypeContainer)
		require.NoError(t, err)
		require.Len(t, snapshots, 3)

		for _, snapshot := range snapshots {
			require.Equal(t, created, snapshot.CreationDate)
			if snapshot.Name == "c1/existing" {
				require.Equal(t, "preserved", snapshot.Description)
				require.Equal(t, "existing", snapshot.Config["user.preserved"])
				require.Equal(t, created.Add(time.Hour), snapshot.ExpiryDate)
				continue
			}

			require.Equal(t, "Auto repaired", snapshot.Description)
			require.Equal(t, "original", snapshot.Config["user.preserved"])
			require.True(t, snapshot.ExpiryDate.IsZero())
		}
	}
}
