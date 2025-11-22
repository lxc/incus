package resources

import (
	"github.com/lxc/incus/v6/shared/api"
)

// GetSerial returns the serial devices available on the system
func GetSerial() (*api.ResourcesSerial, error) {
	serial := api.ResourcesSerial{}
	// TODO: Get the serial devices from the system

	return &serial, nil
}
