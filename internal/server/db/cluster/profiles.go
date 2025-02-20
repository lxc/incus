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
//generate-database:mapper target profiles.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e profile objects
//generate-database:mapper stmt -e profile objects-by-ID
//generate-database:mapper stmt -e profile objects-by-Name
//generate-database:mapper stmt -e profile objects-by-Project
//generate-database:mapper stmt -e profile objects-by-Project-and-Name
//generate-database:mapper stmt -e profile id
//generate-database:mapper stmt -e profile create
//generate-database:mapper stmt -e profile rename
//generate-database:mapper stmt -e profile update
//generate-database:mapper stmt -e profile delete-by-Project-and-Name
//
//generate-database:mapper method -i -e profile ID
//generate-database:mapper method -i -e profile Exists
//generate-database:mapper method -i -e profile GetMany references=Config,Device
//generate-database:mapper method -i -e profile GetOne
//generate-database:mapper method -i -e profile Create references=Config,Device
//generate-database:mapper method -i -e profile Rename
//generate-database:mapper method -i -e profile Update references=Config,Device
//generate-database:mapper method -i -e profile DeleteOne-by-Project-and-Name

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
func (p *Profile) ToAPI(ctx context.Context, tx *sql.Tx, profileConfigs map[int]map[string]string, profileDevices map[int][]Device) (*api.Profile, error) {
	var err error

	var dbConfig map[string]string
	if profileConfigs != nil {
		dbConfig = profileConfigs[p.ID]
		if dbConfig == nil {
			dbConfig = map[string]string{}
		}
	} else {
		dbConfig, err = GetProfileConfig(ctx, tx, p.ID)
		if err != nil {
			return nil, err
		}
	}

	var dbDevices map[string]Device
	if profileDevices != nil {
		dbDevices = map[string]Device{}

		for _, dev := range profileDevices[p.ID] {
			dbDevices[dev.Name] = dev
		}
	} else {
		dbDevices, err = GetProfileDevices(ctx, tx, p.ID)
		if err != nil {
			return nil, err
		}
	}

	profile := &api.Profile{
		Name: p.Name,
		ProfilePut: api.ProfilePut{
			Description: p.Description,
			Config:      dbConfig,
			Devices:     DevicesToAPI(dbDevices),
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
