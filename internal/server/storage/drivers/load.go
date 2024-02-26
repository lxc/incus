package drivers

import (
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/logger"
)

var drivers = map[string]func() driver{
	"btrfs":      func() driver { return &btrfs{} },
	"ceph":       func() driver { return &ceph{} },
	"cephfs":     func() driver { return &cephfs{} },
	"cephobject": func() driver { return &cephobject{} },
	"dir":        func() driver { return &dir{} },
	"lvm":        func() driver { return &lvm{} },
	"lvmcluster": func() driver { return &lvm{clustered: true} },
	"zfs":        func() driver { return &zfs{} },
}

// Validators contains functions used for validating a drivers's config.
type Validators struct {
	PoolRules   func() map[string]func(string) error
	VolumeRules func(vol Volume) map[string]func(string) error
}

// Load returns a Driver for an existing low-level storage pool.
func Load(state *state.State, driverName string, name string, config map[string]string, logger logger.Logger, volIDFunc func(volType VolumeType, volName string) (int64, error), commonRules *Validators) (Driver, error) {
	var driverFunc func() driver

	// Locate the driver loader.
	if state.OS.MockMode {
		driverFunc = func() driver { return &mock{} }
	} else {
		df, ok := drivers[driverName]
		if !ok {
			return nil, ErrUnknownDriver
		}

		driverFunc = df
	}

	d := driverFunc()
	d.init(state, name, config, logger, volIDFunc, commonRules)

	err := d.load()
	if err != nil {
		return nil, err
	}

	return d, nil
}

// SupportedDrivers returns a list of supported storage drivers by loading each storage driver and running its
// compatibility inspection process. This can take a long time if a driver is not supported.
func SupportedDrivers(s *state.State) []Info {
	supportedDrivers := make([]Info, 0, len(drivers))

	for driverName := range drivers {
		driver, err := Load(s, driverName, "", nil, nil, nil, nil)
		if err != nil {
			continue
		}

		supportedDrivers = append(supportedDrivers, driver.Info())
	}

	return supportedDrivers
}

// AllDriverNames returns a list of all storage driver names.
func AllDriverNames() []string {
	driverNames := make([]string, 0, len(drivers))
	for driverName := range drivers {
		driverNames = append(driverNames, driverName)
	}

	return driverNames
}

// RemoteDriverNames returns a list of remote storage driver names.
func RemoteDriverNames() []string {
	driverNames := make([]string, 0, len(drivers))
	for driverName, driverFunc := range drivers {
		if !driverFunc().isRemote() {
			continue
		}

		driverNames = append(driverNames, driverName)
	}

	return driverNames
}
