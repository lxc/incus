//go:build linux && cgo && !agent

package cluster

import (
	"time"

	"github.com/lxc/incus/v6/internal/server/db/warningtype"
	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper generate-database db mapper -t warnings.mapper.go
//go:generate mapper generate -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt warning objects
//generate-database:mapper stmt warning objects-by-UUID
//generate-database:mapper stmt warning objects-by-Project
//generate-database:mapper stmt warning objects-by-Status
//generate-database:mapper stmt warning objects-by-Node-and-TypeCode
//generate-database:mapper stmt warning objects-by-Node-and-TypeCode-and-Project
//generate-database:mapper stmt warning objects-by-Node-and-TypeCode-and-Project-and-EntityTypeCode-and-EntityID
//generate-database:mapper stmt warning delete-by-UUID
//generate-database:mapper stmt warning delete-by-EntityTypeCode-and-EntityID
//generate-database:mapper stmt warning id
//
//generate-database:mapper method warning GetMany
//generate-database:mapper method warning GetOne-by-UUID
//generate-database:mapper method warning DeleteOne-by-UUID
//generate-database:mapper method warning DeleteMany-by-EntityTypeCode-and-EntityID
//generate-database:mapper method warning ID
//generate-database:mapper method warning Exists struct=Warning

// Warning is a value object holding db-related details about a warning.
type Warning struct {
	ID             int
	Node           string `db:"coalesce=''&leftjoin=nodes.name"`
	Project        string `db:"coalesce=''&leftjoin=projects.name"`
	EntityTypeCode int    `db:"coalesce=-1"`
	EntityID       int    `db:"coalesce=-1"`
	UUID           string `db:"primary=yes"`
	TypeCode       warningtype.Type
	Status         warningtype.Status
	FirstSeenDate  time.Time
	LastSeenDate   time.Time
	UpdatedDate    time.Time
	LastMessage    string
	Count          int
}

// WarningFilter specifies potential query parameter fields.
type WarningFilter struct {
	ID             *int
	UUID           *string
	Project        *string
	Node           *string
	TypeCode       *warningtype.Type
	EntityTypeCode *int
	EntityID       *int
	Status         *warningtype.Status
}

// ToAPI returns an API entry.
func (w Warning) ToAPI() api.Warning {
	typeCode := warningtype.Type(w.TypeCode)

	return api.Warning{
		WarningPut: api.WarningPut{
			Status: warningtype.Statuses[warningtype.Status(w.Status)],
		},
		UUID:        w.UUID,
		Location:    w.Node,
		Project:     w.Project,
		Type:        warningtype.TypeNames[typeCode],
		Count:       w.Count,
		FirstSeenAt: w.FirstSeenDate,
		LastSeenAt:  w.LastSeenDate,
		LastMessage: w.LastMessage,
		Severity:    warningtype.Severities[typeCode.Severity()],
	}
}
