//go:build linux && cgo && !agent

package cluster

import "context"

// InstanceSnapshotPropertyGenerated is an interface of generated methods for InstanceSnapshotProperty.
type InstanceSnapshotPropertyGenerated interface {
	// GetInstanceSnapshotProperties returns all available instance_snapshot_properties.
	// generator: instance_snapshot_property GetMany
	GetInstanceSnapshotProperties(ctx context.Context, db dbtx, filters ...InstanceSnapshotPropertyFilter) ([]InstanceSnapshotProperty, error)

	// GetInstanceSnapshotProperty returns the instance_snapshot_property with the given key.
	// generator: instance_snapshot_property GetOne
	GetInstanceSnapshotProperty(ctx context.Context, db dbtx, instanceSnapshotID int) (*InstanceSnapshotProperty, error)

	// CreateInstanceSnapshotProperty adds a new instance_snapshot_property to the database.
	// generator: instance_snapshot_property Create
	CreateInstanceSnapshotProperty(ctx context.Context, db dbtx, object InstanceSnapshotProperty) (int64, error)

	// DeleteInstanceSnapshotProperty deletes the instance_snapshot_property matching the given key parameters.
	// generator: instance_snapshot_property DeleteOne-by-InstanceSnapshotID
	DeleteInstanceSnapshotProperty(ctx context.Context, db dbtx, instanceSnapshotID int) error
}
