//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"time"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
)

// Code generation directives.
//
//generate-database:mapper target snapshots.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e instance_snapshot objects
//generate-database:mapper stmt -e instance_snapshot objects-by-ID
//generate-database:mapper stmt -e instance_snapshot objects-by-Project-and-Instance
//generate-database:mapper stmt -e instance_snapshot objects-by-Project-and-Instance-and-Name
//generate-database:mapper stmt -e instance_snapshot id
//generate-database:mapper stmt -e instance_snapshot create references=Config,Devices
//generate-database:mapper stmt -e instance_snapshot rename
//generate-database:mapper stmt -e instance_snapshot delete-by-Project-and-Instance-and-Name
//
//generate-database:mapper method -i -e instance_snapshot GetMany references=Config,Device
//generate-database:mapper method -i -e instance_snapshot GetOne
//generate-database:mapper method -i -e instance_snapshot ID
//generate-database:mapper method -i -e instance_snapshot Exists
//generate-database:mapper method -i -e instance_snapshot Create references=Config,Device
//generate-database:mapper method -i -e instance_snapshot Rename
//generate-database:mapper method -i -e instance_snapshot DeleteOne-by-Project-and-Instance-and-Name

// InstanceSnapshot is a value object holding db-related details about a snapshot.
type InstanceSnapshot struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name&joinon=instances.project_id"`
	Instance     string `db:"primary=yes&join=instances.name"`
	Name         string `db:"primary=yes"`
	CreationDate time.Time
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
func (s *InstanceSnapshot) ToInstance(ctx context.Context, tx *sql.Tx, parentName string, parentNode string, parentType instancetype.Type, parentArch int) (Instance, error) {
	p, err := GetInstanceSnapshotProperty(ctx, tx, s.ID)
	if err != nil {
		return Instance{}, err
	}

	return Instance{
		ID:           s.ID,
		Project:      s.Project,
		Name:         parentName + internalInstance.SnapshotDelimiter + s.Name,
		Node:         parentNode,
		Type:         parentType,
		Snapshot:     true,
		Architecture: parentArch,
		Ephemeral:    p.Ephemeral,
		CreationDate: s.CreationDate,
		Stateful:     p.Stateful,
		LastUseDate:  sql.NullTime{},
		Description:  p.Description,
	}, nil
}
