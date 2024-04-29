//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper incus-generate db mapper -t profiles.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e profile objects
//go:generate mapper stmt -e profile objects-by-ID
//go:generate mapper stmt -e profile objects-by-Name
//go:generate mapper stmt -e profile objects-by-Project
//go:generate mapper stmt -e profile objects-by-Project-and-Name
//go:generate mapper stmt -e profile id
//go:generate mapper stmt -e profile create
//go:generate mapper stmt -e profile rename
//go:generate mapper stmt -e profile update
//go:generate mapper stmt -e profile delete-by-Project-and-Name
//
//go:generate mapper method -i -e profile ID
//go:generate mapper method -i -e profile Exists
//go:generate mapper method -i -e profile GetMany references=Config,Device
//go:generate mapper method -i -e profile GetOne
//go:generate mapper method -i -e profile Create references=Config,Device
//go:generate mapper method -i -e profile Rename
//go:generate mapper method -i -e profile Update references=Config,Device
//go:generate mapper method -i -e profile DeleteOne-by-Project-and-Name

// Profile is a value object holding db-related details about a profile.
type Profile struct {
	ID          int
	ProjectID   int    `db:"omit=create,update"`
	Project     string `db:"primary=yes&join=projects.name"`
	Name        string `db:"primary=yes"`
	Description string `db:"coalesce=''"`
}

// ProfileFilter specifies potential query parameter fields.
type ProfileFilter struct {
	ID      *int
	Project *string
	Name    *string
}

// ToAPI returns a cluster Profile as an API struct.
func (p *Profile) ToAPI(ctx context.Context, tx *sql.Tx) (*api.Profile, error) {
	config, err := GetProfileConfig(ctx, tx, p.ID)
	if err != nil {
		return nil, err
	}

	devices, err := GetProfileDevices(ctx, tx, p.ID)
	if err != nil {
		return nil, err
	}

	profile := &api.Profile{
		Name: p.Name,
		ProfilePut: api.ProfilePut{
			Description: p.Description,
			Config:      config,
			Devices:     DevicesToAPI(devices),
		},
		Project: p.Project,
	}

	return profile, nil
}

// GetProfilesIfEnabled returns the profiles from the given project, or the
// default project if "features.profiles" is not set.
func GetProfilesIfEnabled(ctx context.Context, tx *sql.Tx, projectName string, names []string) ([]Profile, error) {
	enabled, err := ProjectHasProfiles(ctx, tx, projectName)
	if err != nil {
		return nil, err
	}

	if !enabled {
		projectName = "default"
	}

	profiles := make([]Profile, 0, len(names))
	for _, name := range names {
		profile, err := GetProfile(ctx, tx, projectName, name)
		if err != nil {
			return nil, err
		}

		profiles = append(profiles, *profile)
	}

	return profiles, nil
}

// ExpandInstanceConfig expands the given instance config with the config
// values of the given profiles.
func ExpandInstanceConfig(config map[string]string, profiles []api.Profile) map[string]string {
	expandedConfig := map[string]string{}

	// Apply all the profiles
	profileConfigs := make([]map[string]string, len(profiles))
	for i, profile := range profiles {
		profileConfigs[i] = profile.Config
	}

	for i := range profileConfigs {
		for k, v := range profileConfigs[i] {
			expandedConfig[k] = v
		}
	}

	// Stick the given config on top
	for k, v := range config {
		expandedConfig[k] = v
	}

	return expandedConfig
}

// ExpandInstanceDevices expands the given instance devices with the devices
// defined in the given profiles.
func ExpandInstanceDevices(devices config.Devices, profiles []api.Profile) config.Devices {
	expandedDevices := config.Devices{}

	// Apply all the profiles
	profileDevices := make([]config.Devices, len(profiles))
	for i, profile := range profiles {
		profileDevices[i] = config.NewDevices(profile.Devices)
	}

	for i := range profileDevices {
		for k, v := range profileDevices[i] {
			expandedDevices[k] = v
		}
	}

	// Stick the given devices on top
	for k, v := range devices {
		expandedDevices[k] = v
	}

	return expandedDevices
}
