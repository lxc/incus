//go:build linux && cgo && !agent

package cluster

import (
	"database/sql"
	"time"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
)

// Code generation directives.
//
//go:generate -command mapper incus-generate db mapper -t snapshots.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"

//go:generate mapper stmt -e instance_snapshot objects
//go:generate mapper stmt -e instance_snapshot objects-by-ID
//go:generate mapper stmt -e instance_snapshot objects-by-Project-and-Instance
//go:generate mapper stmt -e instance_snapshot objects-by-Project-and-Instance-and-Name
//go:generate mapper stmt -e instance_snapshot id
//go:generate mapper stmt -e instance_snapshot create references=Config,Devices
//go:generate mapper stmt -e instance_snapshot rename
//go:generate mapper stmt -e instance_snapshot delete-by-Project-and-Instance-and-Name
//
//go:generate mapper method -i -e instance_snapshot GetMany references=Config,Device
//go:generate mapper method -i -e instance_snapshot GetOne
//go:generate mapper method -i -e instance_snapshot ID
//go:generate mapper method -i -e instance_snapshot Exists
//go:generate mapper method -i -e instance_snapshot Create references=Config,Device
//go:generate mapper method -i -e instance_snapshot Rename
//go:generate mapper method -i -e instance_snapshot DeleteOne-by-Project-and-Instance-and-Name

// InstanceSnapshot is a value object holding db-related details about a snapshot.
type InstanceSnapshot struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name&joinon=instances.project_id"`
	Instance     string `db:"primary=yes&join=instances.name"`
	Name         string `db:"primary=yes"`
	CreationDate time.Time
	Stateful     bool
	Description  string `db:"coalesce=''"`
	ExpiryDate   sql.NullTime
}

// InstanceSnapshotFilter specifies potential query parameter fields.
type InstanceSnapshotFilter struct {
	ID       *int
	Project  *string
	Instance *string
	Name     *string
}

// ToInstance converts an instance snapshot to a database Instance, filling in extra fields from the parent instance.
func (s *InstanceSnapshot) ToInstance(parentName string, parentNode string, parentType instancetype.Type, parentArch int) Instance {
	return Instance{
		ID:           s.ID,
		Project:      s.Project,
		Name:         parentName + internalInstance.SnapshotDelimiter + s.Name,
		Node:         parentNode,
		Type:         parentType,
		Snapshot:     true,
		Architecture: parentArch,
		Ephemeral:    false,
		CreationDate: s.CreationDate,
		Stateful:     s.Stateful,
		LastUseDate:  sql.NullTime{},
		Description:  s.Description,
		ExpiryDate:   s.ExpiryDate,
	}
}
