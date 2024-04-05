//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"time"

	"github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/osarch"
)

// Code generation directives.
//
//go:generate -command mapper incus-generate db mapper -t instances.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e instance objects
//go:generate mapper stmt -e instance objects-by-ID
//go:generate mapper stmt -e instance objects-by-Project
//go:generate mapper stmt -e instance objects-by-Project-and-Type
//go:generate mapper stmt -e instance objects-by-Project-and-Type-and-Node
//go:generate mapper stmt -e instance objects-by-Project-and-Type-and-Node-and-Name
//go:generate mapper stmt -e instance objects-by-Project-and-Type-and-Name
//go:generate mapper stmt -e instance objects-by-Project-and-Name
//go:generate mapper stmt -e instance objects-by-Project-and-Name-and-Node
//go:generate mapper stmt -e instance objects-by-Project-and-Node
//go:generate mapper stmt -e instance objects-by-Type
//go:generate mapper stmt -e instance objects-by-Type-and-Name
//go:generate mapper stmt -e instance objects-by-Type-and-Name-and-Node
//go:generate mapper stmt -e instance objects-by-Type-and-Node
//go:generate mapper stmt -e instance objects-by-Node
//go:generate mapper stmt -e instance objects-by-Node-and-Name
//go:generate mapper stmt -e instance objects-by-Name
//go:generate mapper stmt -e instance id
//go:generate mapper stmt -e instance create
//go:generate mapper stmt -e instance rename
//go:generate mapper stmt -e instance delete-by-Project-and-Name
//go:generate mapper stmt -e instance update
//
//go:generate mapper method -i -e instance GetMany references=Config,Device
//go:generate mapper method -i -e instance GetOne
//go:generate mapper method -i -e instance ID
//go:generate mapper method -i -e instance Exists
//go:generate mapper method -i -e instance Create references=Config,Device
//go:generate mapper method -i -e instance Rename
//go:generate mapper method -i -e instance DeleteOne-by-Project-and-Name
//go:generate mapper method -i -e instance Update references=Config,Device

// Instance is a value object holding db-related details about an instance.
type Instance struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name"`
	Name         string `db:"primary=yes"`
	Node         string `db:"join=nodes.name"`
	Type         instancetype.Type
	Snapshot     bool `db:"ignore"`
	Architecture int
	Ephemeral    bool
	CreationDate time.Time
	Stateful     bool
	LastUseDate  sql.NullTime
	Description  string `db:"coalesce=''"`
	ExpiryDate   sql.NullTime
}

// InstanceFilter specifies potential query parameter fields.
type InstanceFilter struct {
	ID      *int
	Project *string
	Name    *string
	Node    *string
	Type    *instancetype.Type
}

// ToAPI converts the database Instance to API type.
func (i *Instance) ToAPI(ctx context.Context, tx *sql.Tx) (*api.Instance, error) {
	profiles, err := GetInstanceProfiles(ctx, tx, i.ID)
	if err != nil {
		return nil, err
	}

	apiProfiles := make([]api.Profile, 0, len(profiles))
	profileNames := make([]string, 0, len(profiles))
	for _, p := range profiles {
		apiProfile, err := p.ToAPI(ctx, tx)
		if err != nil {
			return nil, err
		}

		apiProfiles = append(apiProfiles, *apiProfile)
		profileNames = append(profileNames, p.Name)
	}

	devices, err := GetInstanceDevices(ctx, tx, i.ID)
	if err != nil {
		return nil, err
	}

	apiDevices := DevicesToAPI(devices)
	expandedDevices := ExpandInstanceDevices(config.NewDevices(apiDevices), apiProfiles)

	config, err := GetInstanceConfig(ctx, tx, i.ID)
	if err != nil {
		return nil, err
	}

	expandedConfig := ExpandInstanceConfig(config, apiProfiles)

	archName, err := osarch.ArchitectureName(i.Architecture)
	if err != nil {
		return nil, err
	}

	return &api.Instance{
		InstancePut: api.InstancePut{
			Architecture: archName,
			Config:       config,
			Devices:      apiDevices,
			Ephemeral:    i.Ephemeral,
			Profiles:     profileNames,
			Stateful:     i.Stateful,
			Description:  i.Description,
		},
		CreatedAt:       i.CreationDate,
		ExpandedConfig:  expandedConfig,
		ExpandedDevices: expandedDevices.CloneNative(),
		Name:            i.Name,
		LastUsedAt:      i.LastUseDate.Time,
		Location:        i.Node,
		Type:            i.Type.String(),
		Project:         i.Project,
	}, nil
}
