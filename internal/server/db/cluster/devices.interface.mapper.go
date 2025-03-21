//go:build linux && cgo && !agent

package cluster

import "context"

// DeviceGenerated is an interface of generated methods for Device.
type DeviceGenerated interface {
	// GetDevices returns all available devices for the parent entity.
	// generator: device GetMany
	GetDevices(ctx context.Context, db tx, parentTablePrefix string, parentColumnPrefix string, filters ...DeviceFilter) (map[int][]Device, error)

	// CreateDevices adds a new device to the database.
	// generator: device Create
	CreateDevices(ctx context.Context, db tx, parentTablePrefix string, parentColumnPrefix string, objects map[string]Device) error

	// UpdateDevices updates the device matching the given key parameters.
	// generator: device Update
	UpdateDevices(ctx context.Context, db tx, parentTablePrefix string, parentColumnPrefix string, referenceID int, devices map[string]Device) error

	// DeleteDevices deletes the device matching the given key parameters.
	// generator: device DeleteMany
	DeleteDevices(ctx context.Context, db tx, parentTablePrefix string, parentColumnPrefix string, referenceID int) error
}
