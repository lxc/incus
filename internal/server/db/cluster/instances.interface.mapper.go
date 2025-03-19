//go:build linux && cgo && !agent

package cluster

import "context"

// InstanceGenerated is an interface of generated methods for Instance.
type InstanceGenerated interface {
	// GetInstanceConfig returns all available Instance Config
	// generator: instance GetMany
	GetInstanceConfig(ctx context.Context, db tx, instanceID int, filters ...ConfigFilter) (map[string]string, error)

	// GetInstanceDevices returns all available Instance Devices
	// generator: instance GetMany
	GetInstanceDevices(ctx context.Context, db tx, instanceID int, filters ...DeviceFilter) (map[string]Device, error)

	// GetInstances returns all available instances.
	// generator: instance GetMany
	GetInstances(ctx context.Context, db dbtx, filters ...InstanceFilter) ([]Instance, error)

	// GetInstance returns the instance with the given key.
	// generator: instance GetOne
	GetInstance(ctx context.Context, db dbtx, project string, name string) (*Instance, error)

	// GetInstanceID return the ID of the instance with the given key.
	// generator: instance ID
	GetInstanceID(ctx context.Context, db tx, project string, name string) (int64, error)

	// InstanceExists checks if a instance with the given key exists.
	// generator: instance Exists
	InstanceExists(ctx context.Context, db dbtx, project string, name string) (bool, error)

	// CreateInstanceConfig adds new instance Config to the database.
	// generator: instance Create
	CreateInstanceConfig(ctx context.Context, db dbtx, instanceID int64, config map[string]string) error

	// CreateInstanceDevices adds new instance Devices to the database.
	// generator: instance Create
	CreateInstanceDevices(ctx context.Context, db tx, instanceID int64, devices map[string]Device) error

	// CreateInstance adds a new instance to the database.
	// generator: instance Create
	CreateInstance(ctx context.Context, db dbtx, object Instance) (int64, error)

	// RenameInstance renames the instance matching the given key parameters.
	// generator: instance Rename
	RenameInstance(ctx context.Context, db dbtx, project string, name string, to string) error

	// DeleteInstance deletes the instance matching the given key parameters.
	// generator: instance DeleteOne-by-Project-and-Name
	DeleteInstance(ctx context.Context, db dbtx, project string, name string) error

	// UpdateInstanceConfig updates the instance Config matching the given key parameters.
	// generator: instance Update
	UpdateInstanceConfig(ctx context.Context, db tx, instanceID int64, config map[string]string) error

	// UpdateInstanceDevices updates the instance Device matching the given key parameters.
	// generator: instance Update
	UpdateInstanceDevices(ctx context.Context, db tx, instanceID int64, devices map[string]Device) error

	// UpdateInstance updates the instance matching the given key parameters.
	// generator: instance Update
	UpdateInstance(ctx context.Context, db tx, project string, name string, object Instance) error
}
