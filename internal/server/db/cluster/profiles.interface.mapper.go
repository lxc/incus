//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// ProfileGenerated is an interface of generated methods for Profile.
type ProfileGenerated interface {
	// GetProfileID return the ID of the profile with the given key.
	// generator: profile ID
	GetProfileID(ctx context.Context, tx *sql.Tx, project string, name string) (int64, error)

	// ProfileExists checks if a profile with the given key exists.
	// generator: profile Exists
	ProfileExists(ctx context.Context, tx *sql.Tx, project string, name string) (bool, error)

	// GetProfileConfig returns all available Profile Config
	// generator: profile GetMany
	GetProfileConfig(ctx context.Context, tx *sql.Tx, profileID int, filters ...ConfigFilter) (map[string]string, error)

	// GetProfileDevices returns all available Profile Devices
	// generator: profile GetMany
	GetProfileDevices(ctx context.Context, tx *sql.Tx, profileID int, filters ...DeviceFilter) (map[string]Device, error)

	// GetProfiles returns all available profiles.
	// generator: profile GetMany
	GetProfiles(ctx context.Context, tx *sql.Tx, filters ...ProfileFilter) ([]Profile, error)

	// GetProfile returns the profile with the given key.
	// generator: profile GetOne
	GetProfile(ctx context.Context, tx *sql.Tx, project string, name string) (*Profile, error)

	// CreateProfileConfig adds new profile Config to the database.
	// generator: profile Create
	CreateProfileConfig(ctx context.Context, tx *sql.Tx, profileID int64, config map[string]string) error

	// CreateProfileDevices adds new profile Devices to the database.
	// generator: profile Create
	CreateProfileDevices(ctx context.Context, tx *sql.Tx, profileID int64, devices map[string]Device) error

	// CreateProfile adds a new profile to the database.
	// generator: profile Create
	CreateProfile(ctx context.Context, tx *sql.Tx, object Profile) (int64, error)

	// RenameProfile renames the profile matching the given key parameters.
	// generator: profile Rename
	RenameProfile(ctx context.Context, tx *sql.Tx, project string, name string, to string) error

	// UpdateProfileConfig updates the profile Config matching the given key parameters.
	// generator: profile Update
	UpdateProfileConfig(ctx context.Context, tx *sql.Tx, profileID int64, config map[string]string) error

	// UpdateProfileDevices updates the profile Device matching the given key parameters.
	// generator: profile Update
	UpdateProfileDevices(ctx context.Context, tx *sql.Tx, profileID int64, devices map[string]Device) error

	// UpdateProfile updates the profile matching the given key parameters.
	// generator: profile Update
	UpdateProfile(ctx context.Context, tx *sql.Tx, project string, name string, object Profile) error

	// DeleteProfile deletes the profile matching the given key parameters.
	// generator: profile DeleteOne-by-Project-and-Name
	DeleteProfile(ctx context.Context, tx *sql.Tx, project string, name string) error
}
