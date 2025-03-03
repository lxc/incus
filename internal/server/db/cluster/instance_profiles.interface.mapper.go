//go:build linux && cgo && !agent

package cluster

import "context"

// InstanceProfileGenerated is an interface of generated methods for InstanceProfile.
type InstanceProfileGenerated interface {
	// GetProfileInstances returns all available Instances for the Profile.
	// generator: instance_profile GetMany
	GetProfileInstances(ctx context.Context, db dbtx, profileID int) ([]Instance, error)

	// GetInstanceProfiles returns all available Profiles for the Instance.
	// generator: instance_profile GetMany
	GetInstanceProfiles(ctx context.Context, db dbtx, instanceID int) ([]Profile, error)

	// CreateInstanceProfiles adds a new instance_profile to the database.
	// generator: instance_profile Create
	CreateInstanceProfiles(ctx context.Context, db dbtx, objects []InstanceProfile) error

	// DeleteInstanceProfiles deletes the instance_profile matching the given key parameters.
	// generator: instance_profile DeleteMany
	DeleteInstanceProfiles(ctx context.Context, db dbtx, instanceID int) error
}
