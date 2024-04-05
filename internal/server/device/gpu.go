package device

import (
	"fmt"

	"github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/validate"
)

func gpuValidationRules(requiredFields []string, optionalFields []string) map[string]func(value string) error {
	// Define a set of default validators for each field name.
	defaultValidators := map[string]func(value string) error{
		"vendorid":  validate.Optional(validate.IsDeviceID),
		"productid": validate.Optional(validate.IsDeviceID),
		"id":        validate.IsAny,
		"pci":       validate.IsPCIAddress,
		"uid":       unixValidUserID,
		"gid":       unixValidUserID,
		"mode":      unixValidOctalFileMode,
		"mig.gi":    validate.IsUint8,
		"mig.ci":    validate.IsUint8,
		"mig.uuid":  gpuValidMigUUID,
		"mdev":      validate.IsAny,
	}

	validators := map[string]func(value string) error{}

	for _, k := range optionalFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in an empty check as field is optional.
		validators[k] = func(value string) error {
			if value == "" {
				return nil
			}

			return defaultValidator(value)
		}
	}

	// Add required fields last, that way if they are specified in both required and optional
	// field sets, the required one will overwrite the optional validators.
	for _, k := range requiredFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in a not empty check as field is required.
		validators[k] = func(value string) error {
			err := validate.IsNotEmpty(value)
			if err != nil {
				return err
			}

			return defaultValidator(value)
		}
	}

	return validators
}

// Check if the device matches the given GPU card.
// It matches based on vendorid, pci, productid or id setting of the device.
func gpuSelected(device config.Device, gpu api.ResourcesGPUCard) bool {
	return !((device["vendorid"] != "" && gpu.VendorID != device["vendorid"]) ||
		(device["pci"] != "" && gpu.PCIAddress != device["pci"]) ||
		(device["productid"] != "" && gpu.ProductID != device["productid"]) ||
		(device["id"] != "" && (gpu.DRM == nil || fmt.Sprintf("%d", gpu.DRM.ID) != device["id"])))
}
