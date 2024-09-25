//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"fmt"
)

// Code generation directives.
//
//go:generate -command mapper incus-generate db mapper -t instance_profiles.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e instance_profile objects
//go:generate mapper stmt -e instance_profile objects-by-ProfileID
//go:generate mapper stmt -e instance_profile objects-by-InstanceID
//go:generate mapper stmt -e instance_profile create
//go:generate mapper stmt -e instance_profile delete-by-InstanceID
//
//go:generate mapper method -i -e instance_profile GetMany struct=Profile
//go:generate mapper method -i -e instance_profile GetMany struct=Instance
//go:generate mapper method -i -e instance_profile Create struct=Instance
//go:generate mapper method -i -e instance_profile DeleteMany struct=Instance

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
