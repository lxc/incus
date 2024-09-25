//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/query"
)

// UpdateInstanceSnapshotConfig inserts/updates/deletes the provided config keys.
func (c *ClusterTx) UpdateInstanceSnapshotConfig(id int, values map[string]string) error {
	insertSQL := "INSERT OR REPLACE INTO instances_snapshots_config (instance_snapshot_id, key, value) VALUES"
	deleteSQL := "DELETE FROM instances_snapshots_config WHERE key IN %s AND instance_snapshot_id=?"
	return c.configUpdate(id, values, insertSQL, deleteSQL)
}

// UpdateInstanceSnapshot updates the description and expiry date of the
// instance snapshot with the given ID.
func (c *ClusterTx) UpdateInstanceSnapshot(id int, description string, expiryDate time.Time) error {
	str := "UPDATE instances_snapshots SET description=?, expiry_date=? WHERE id=?"
	var err error
	if expiryDate.IsZero() {
		_, err = c.tx.Exec(str, description, "", id)
	} else {
		_, err = c.tx.Exec(str, description, expiryDate, id)
	}

	if err != nil {
		return err
	}

	return nil
}

// GetLocalExpiredInstanceSnapshots returns a list of expired snapshots.
func (c *ClusterTx) GetLocalExpiredInstanceSnapshots(ctx context.Context) ([]cluster.InstanceSnapshot, error) {
	q := `
	SELECT
		instances_snapshots.id,
		instances_snapshots.expiry_date
	FROM instances_snapshots
	JOIN instances ON instances.id=instances_snapshots.instance_id
	WHERE instances.node_id=? AND instances_snapshots.expiry_date != '0001-01-01T00:00:00Z'
	`

	snapshotIDs := []int{}
	err := query.Scan(ctx, c.Tx(), q, func(scan func(dest ...any) error) error {
		var id int
		var expiry sql.NullTime

		// Read the row.
		err := scan(&id, &expiry)
		if err != nil {
			return err
		}

		// Skip if not expired.
		if !expiry.Valid || expiry.Time.Unix() <= 0 {
			return nil
		}

		if time.Now().Before(expiry.Time) {
			return nil
		}

		// Add the snapshot.
		snapshotIDs = append(snapshotIDs, id)

		return nil
	}, c.nodeID)
	if err != nil {
		return nil, err
	}

	// Fetch all the expired snapshot details.
	snapshots := make([]cluster.InstanceSnapshot, len(snapshotIDs))

	for i, id := range snapshotIDs {
		snap, err := cluster.GetInstanceSnapshots(ctx, c.tx, cluster.InstanceSnapshotFilter{ID: &id})
		if err != nil {
			return nil, err
		}

		snapshots[i] = snap[0]
	}

	return snapshots, nil
}

// GetInstanceSnapshotID returns the ID of the snapshot with the given name.
func (c *ClusterTx) GetInstanceSnapshotID(ctx context.Context, project, instance, name string) (int, error) {
	id, err := cluster.GetInstanceSnapshotID(ctx, c.tx, project, instance, name)
	return int(id), err
}
