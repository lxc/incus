package api

// DevIncusPut represents the modifiable data.
type DevIncusPut struct {
	// Instance state
	// Example: Started
	State string `json:"state" yaml:"state"`
}

// DevIncusGet represents the server data which is returned as the root of the /dev/incus API.
type DevIncusGet struct {
	DevIncusPut

	// API version number
	// Example: 1.0
	APIVersion string `json:"api_version" yaml:"api_version"`

	// Type (container or virtual-machine)
	// Example: container
	InstanceType string `json:"instance_type" yaml:"instance_type"`

	// What cluster member this instance is located on
	// Example: server01
	Location string `json:"location" yaml:"location"`
}
