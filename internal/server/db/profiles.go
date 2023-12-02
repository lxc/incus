//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"

	"github.com/lxc/incus/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/shared/api"
)

// GetProfileNames returns the names of all profiles in the given project.
func (c *Cluster) GetProfileNames(project string) ([]string, error) {
	q := `
SELECT profiles.name
 FROM profiles
 JOIN projects ON projects.id = profiles.project_id
WHERE projects.name = ?
`
	var result [][]any

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		enabled, err := cluster.ProjectHasProfiles(context.Background(), tx.tx, project)
		if err != nil {
			return fmt.Errorf("Check if project has profiles: %w", err)
		}

		if !enabled {
			project = "default"
		}

		inargs := []any{project}
		var name string
		outfmt := []any{name}

		result, err = queryScan(ctx, tx, q, inargs, outfmt)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

// GetProfile returns the profile with the given name.
func (c *Cluster) GetProfile(project, name string) (int64, *api.Profile, error) {
	var result *api.Profile
	var id int64

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		var err error
		profiles, err := cluster.GetProfilesIfEnabled(ctx, tx.Tx(), project, []string{name})
		if err != nil {
			return err
		}

		if len(profiles) != 1 {
			return fmt.Errorf("Expected one profile with name %q, got %d profiles", name, len(profiles))
		}

		profile := profiles[0]
		id = int64(profile.ID)
		result, err = profile.ToAPI(ctx, tx.Tx())

		return err
	})
	if err != nil {
		return -1, nil, err
	}

	return id, result, nil
}

// GetProfiles returns the profiles with the given names in the given project.
func (c *Cluster) GetProfiles(projectName string, profileNames []string) ([]api.Profile, error) {
	profiles := make([]api.Profile, len(profileNames))

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		dbProfiles, err := cluster.GetProfilesIfEnabled(ctx, tx.Tx(), projectName, profileNames)
		if err != nil {
			return err
		}

		for i, profile := range dbProfiles {
			apiProfile, err := profile.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			profiles[i] = *apiProfile
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return profiles, nil
}

// GetInstancesWithProfile gets the names of the instance associated with the
// profile with the given name in the given project.
func (c *Cluster) GetInstancesWithProfile(project, profile string) (map[string][]string, error) {
	q := `SELECT instances.name, projects.name FROM instances
		JOIN instances_profiles ON instances.id == instances_profiles.instance_id
		JOIN projects ON projects.id == instances.project_id
		WHERE instances_profiles.profile_id ==
		  (SELECT profiles.id FROM profiles
		   JOIN projects ON projects.id == profiles.project_id
		   WHERE profiles.name=? AND projects.name=?)`

	results := map[string][]string{}
	var output [][]any

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		enabled, err := cluster.ProjectHasProfiles(context.Background(), tx.tx, project)
		if err != nil {
			return fmt.Errorf("Check if project has profiles: %w", err)
		}

		if !enabled {
			project = "default"
		}

		inargs := []any{profile, project}
		var name string
		outfmt := []any{name, name}

		output, err = queryScan(ctx, tx, q, inargs, outfmt)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, r := range output {
		if results[r[1].(string)] == nil {
			results[r[1].(string)] = []string{}
		}

		results[r[1].(string)] = append(results[r[1].(string)], r[0].(string))
	}

	return results, nil
}

// RemoveUnreferencedProfiles removes unreferenced profiles.
func (c *Cluster) RemoveUnreferencedProfiles() error {
	stmt := `
DELETE FROM profiles_config WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices_config WHERE profile_device_id NOT IN (SELECT id FROM profiles_devices);
`

	return c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		_, err := tx.tx.ExecContext(ctx, stmt)
		return err
	})
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
func ExpandInstanceDevices(devices deviceConfig.Devices, profiles []api.Profile) deviceConfig.Devices {
	expandedDevices := deviceConfig.Devices{}

	// Apply all the profiles
	profileDevices := make([]deviceConfig.Devices, len(profiles))
	for i, profile := range profiles {
		profileDevices[i] = deviceConfig.NewDevices(profile.Devices)
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
