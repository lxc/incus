//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"fmt"
)

// Code generation directives.
//
//generate-database:mapper target instance_profiles.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e instance_profile objects
//generate-database:mapper stmt -e instance_profile objects-by-ProfileID
//generate-database:mapper stmt -e instance_profile objects-by-InstanceID
//generate-database:mapper stmt -e instance_profile create
//generate-database:mapper stmt -e instance_profile delete-by-InstanceID
//
//generate-database:mapper method -i -e instance_profile GetMany struct=Profile
//generate-database:mapper method -i -e instance_profile GetMany struct=Instance
//generate-database:mapper method -i -e instance_profile Create struct=Instance
//generate-database:mapper method -i -e instance_profile DeleteMany struct=Instance

// InstanceProfile is an association table struct that associates Instances
// to Profiles.
type InstanceProfile struct {
	InstanceID int `db:"primary=yes&order=yes"`
	ProfileID  int
	ApplyOrder int `db:"order=yes"`
}

// InstanceProfileFilter specifies potential query parameter fields.
type InstanceProfileFilter struct {
	InstanceID *int
	ProfileID  *int
}

// UpdateInstanceProfiles updates the profiles of an instance in the order they are given.
func UpdateInstanceProfiles(ctx context.Context, tx *sql.Tx, instanceID int, projectName string, profiles []string) error {
	err := DeleteInstanceProfiles(ctx, tx, instanceID)
	if err != nil {
		return err
	}

	project := projectName
	enabled, err := ProjectHasProfiles(ctx, tx, project)
	if err != nil {
		return fmt.Errorf("Check if project has profiles: %w", err)
	}

	if !enabled {
		project = "default"
	}

	applyOrder := 1
	stmt, err := Stmt(tx, instanceProfileCreate)
	if err != nil {
		return fmt.Errorf("Failed to get \"instanceProfileCreate\" prepared statement: %w", err)
	}

	for _, name := range profiles {
		profileID, err := GetProfileID(ctx, tx, project, name)
		if err != nil {
			return err
		}

		_, err = stmt.Exec(instanceID, profileID, applyOrder)
		if err != nil {
			return err
		}

		applyOrder = applyOrder + 1
	}

	return nil
}

// GetAllInstanceProfiles returns the profiles of every instance in apply order, keyed by instance ID.
func GetAllInstanceProfiles(ctx context.Context, tx *sql.Tx) (map[int][]Profile, error) {
	stmt, err := StmtContext(ctx, tx, instanceProfileObjects)
	if err != nil {
		return nil, fmt.Errorf("Failed to get \"instanceProfileObjects\" prepared statement: %w", err)
	}

	links, err := getInstanceProfiles(ctx, stmt)
	if err != nil {
		return nil, err
	}

	profiles, err := GetProfiles(ctx, tx)
	if err != nil {
		return nil, err
	}

	profilesByID := make(map[int]Profile, len(profiles))
	for _, profile := range profiles {
		profilesByID[profile.ID] = profile
	}

	result := map[int][]Profile{}
	for _, link := range links {
		profile, ok := profilesByID[link.ProfileID]
		if !ok {
			return nil, fmt.Errorf("Instance %d references unknown profile %d", link.InstanceID, link.ProfileID)
		}

		result[link.InstanceID] = append(result[link.InstanceID], profile)
	}

	return result, nil
}
