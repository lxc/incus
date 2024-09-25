package device

import (
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance"
)

type none struct {
	deviceCommon
}

// CanMigrate returns whether the device can be migrated to any other cluster member.
func (d *none) CanMigrate() bool {
	return true
}

// CanHotPlug returns whether the device can be managed whilst the instance is running.
func (d *none) CanHotPlug() bool {
	return true
}

// validateConfig checks the supplied config for correctness.
// validateConfig checks the supplied config for correctness.
func (d *none) validateConfig(instConf instance.ConfigReader) error {
	rules := map[string]func(string) error{} // No fields allowed.
	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// Start is run when the device is added to the container.
func (d *none) Start() (*deviceConfig.RunConfig, error) {
	return nil, nil
}

// Stop is run when the device is removed from the instance.
func (d *none) Stop() (*deviceConfig.RunConfig, error) {
	return nil, nil
}
