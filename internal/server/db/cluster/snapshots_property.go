//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//
//generate-database:mapper target snapshots_property.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e instance_snapshot_property objects
//generate-database:mapper stmt -e instance_snapshot_property objects-by-ID
//generate-database:mapper stmt -e instance_snapshot_property objects-by-InstanceSnapshotID
//generate-database:mapper stmt -e instance_snapshot_property create struct=InstanceSnapshotProperty
//generate-database:mapper stmt -e instance_snapshot_property delete-by-InstanceSnapshotID
//
//generate-database:mapper method -i -e instance_snapshot_property GetMany
//generate-database:mapper method -i -e instance_snapshot_property GetOne
//generate-database:mapper method -i -e instance_snapshot_property Create struct=InstanceSnapshotProperty
//generate-database:mapper method -i -e instance_snapshot_property DeleteOne-by-InstanceSnapshotID

// InstanceSnapshotProperty is a value object holding db-related details about a instances_snapshots_property
type InstanceSnapshotProperty struct {
	ID                 int
	InstanceSnapshotID int `db:"primary=yes&column=instance_snapshot_id"`
	Ephemeral          bool
	Stateful           bool
	Description        string `db:"coalesce=''"`
}

// InstanceSnapshotPropertyFilter specifies potential query parameter fields.
type InstanceSnapshotPropertyFilter struct {
	ID                 *int
	InstanceSnapshotID *int
}
