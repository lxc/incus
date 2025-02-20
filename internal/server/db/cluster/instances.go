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
//generate-database:mapper target instances.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e instance objects
//generate-database:mapper stmt -e instance objects-by-ID
//generate-database:mapper stmt -e instance objects-by-Project
//generate-database:mapper stmt -e instance objects-by-Project-and-Type
//generate-database:mapper stmt -e instance objects-by-Project-and-Type-and-Node
//generate-database:mapper stmt -e instance objects-by-Project-and-Type-and-Node-and-Name
//generate-database:mapper stmt -e instance objects-by-Project-and-Type-and-Name
//generate-database:mapper stmt -e instance objects-by-Project-and-Name
//generate-database:mapper stmt -e instance objects-by-Project-and-Name-and-Node
//generate-database:mapper stmt -e instance objects-by-Project-and-Node
//generate-database:mapper stmt -e instance objects-by-Type
//generate-database:mapper stmt -e instance objects-by-Type-and-Name
//generate-database:mapper stmt -e instance objects-by-Type-and-Name-and-Node
//generate-database:mapper stmt -e instance objects-by-Type-and-Node
//generate-database:mapper stmt -e instance objects-by-Node
//generate-database:mapper stmt -e instance objects-by-Node-and-Name
//generate-database:mapper stmt -e instance objects-by-Name
//generate-database:mapper stmt -e instance id
//generate-database:mapper stmt -e instance create
//generate-database:mapper stmt -e instance rename
//generate-database:mapper stmt -e instance delete-by-Project-and-Name
//generate-database:mapper stmt -e instance update
//
//generate-database:mapper method -i -e instance GetMany references=Config,Device
//generate-database:mapper method -i -e instance GetOne
//generate-database:mapper method -i -e instance ID
//generate-database:mapper method -i -e instance Exists
//generate-database:mapper method -i -e instance Create references=Config,Device
//generate-database:mapper method -i -e instance Rename
//generate-database:mapper method -i -e instance DeleteOne-by-Project-and-Name
//generate-database:mapper method -i -e instance Update references=Config,Device

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
func (i *Instance) ToAPI(ctx context.Context, tx *sql.Tx, instanceDevices map[int][]Device, profileConfigs map[int]map[string]string, profileDevices map[int][]Device) (*api.Instance, error) {
	profiles, err := GetInstanceProfiles(ctx, tx, i.ID)
	if err != nil {
		return nil, err
	}

	if profileConfigs == nil {
		profileConfigs, err = GetConfig(ctx, tx, "profile")
		if err != nil {
			return nil, err
		}
	}

	if profileDevices == nil {
		profileDevices, err = GetDevices(ctx, tx, "profile")
		if err != nil {
			return nil, err
		}
	}

	apiProfiles := make([]api.Profile, 0, len(profiles))
	profileNames := make([]string, 0, len(profiles))
	for _, p := range profiles {
		apiProfile, err := p.ToAPI(ctx, tx, profileConfigs, profileDevices)
		if err != nil {
			return nil, err
		}

		apiProfiles = append(apiProfiles, *apiProfile)
		profileNames = append(profileNames, p.Name)
	}

	var devices map[string]Device
	if instanceDevices != nil {
		devices = map[string]Device{}

		for _, dev := range instanceDevices[i.ID] {
			devices[dev.Name] = dev
		}
	} else {
		devices, err = GetInstanceDevices(ctx, tx, i.ID)
		if err != nil {
			return nil, err
		}
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
