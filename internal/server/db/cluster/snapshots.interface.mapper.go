//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// InstanceSnapshotGenerated is an interface of generated methods for InstanceSnapshot.
type InstanceSnapshotGenerated interface {
	// GetInstanceSnapshotConfig returns all available InstanceSnapshot Config
	// generator: instance_snapshot GetMany
	GetInstanceSnapshotConfig(ctx context.Context, tx *sql.Tx, instanceSnapshotID int, filters ...ConfigFilter) (map[string]string, error)

	// GetInstanceSnapshotDevices returns all available InstanceSnapshot Devices
	// generator: instance_snapshot GetMany
	GetInstanceSnapshotDevices(ctx context.Context, tx *sql.Tx, instanceSnapshotID int, filters ...DeviceFilter) (map[string]Device, error)

	// GetInstanceSnapshots returns all available instance_snapshots.
	// generator: instance_snapshot GetMany
	GetInstanceSnapshots(ctx context.Context, tx *sql.Tx, filters ...InstanceSnapshotFilter) ([]InstanceSnapshot, error)

	// GetInstanceSnapshot returns the instance_snapshot with the given key.
	// generator: instance_snapshot GetOne
	GetInstanceSnapshot(ctx context.Context, tx *sql.Tx, project string, instance string, name string) (*InstanceSnapshot, error)

	// GetInstanceSnapshotID return the ID of the instance_snapshot with the given key.
	// generator: instance_snapshot ID
	GetInstanceSnapshotID(ctx context.Context, tx *sql.Tx, project string, instance string, name string) (int64, error)

	// InstanceSnapshotExists checks if a instance_snapshot with the given key exists.
	// generator: instance_snapshot Exists
	InstanceSnapshotExists(ctx context.Context, tx *sql.Tx, project string, instance string, name string) (bool, error)

	// CreateInstanceSnapshotConfig adds new instance_snapshot Config to the database.
	// generator: instance_snapshot Create
	CreateInstanceSnapshotConfig(ctx context.Context, tx *sql.Tx, instanceSnapshotID int64, config map[string]string) error

	// CreateInstanceSnapshotDevices adds new instance_snapshot Devices to the database.
	// generator: instance_snapshot Create
	CreateInstanceSnapshotDevices(ctx context.Context, tx *sql.Tx, instanceSnapshotID int64, devices map[string]Device) error

	// CreateInstanceSnapshot adds a new instance_snapshot to the database.
	// generator: instance_snapshot Create
	CreateInstanceSnapshot(ctx context.Context, tx *sql.Tx, object InstanceSnapshot) (int64, error)

	// RenameInstanceSnapshot renames the instance_snapshot matching the given key parameters.
	// generator: instance_snapshot Rename
	RenameInstanceSnapshot(ctx context.Context, tx *sql.Tx, project string, instance string, name string, to string) error

	// DeleteInstanceSnapshot deletes the instance_snapshot matching the given key parameters.
	// generator: instance_snapshot DeleteOne-by-Project-and-Instance-and-Name
	DeleteInstanceSnapshot(ctx context.Context, tx *sql.Tx, project string, instance string, name string) error
}
