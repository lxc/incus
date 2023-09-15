package linux

import (
	"github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared"
	"github.com/lxc/incus/shared/api"
)

// AvailableStorageDrivers returns a list of storage drivers that are available.
func AvailableStorageDrivers(supportedDrivers []api.ServerStorageDriverInfo, poolType util.PoolType) []string {
	backingFs, err := DetectFilesystem(shared.VarPath())
	if err != nil {
		backingFs = "dir"
	}

	drivers := make([]string, 0, len(supportedDrivers))

	// Check available backends.
	for _, driver := range supportedDrivers {
		if poolType == util.PoolTypeRemote && !driver.Remote {
			continue
		}

		if poolType == util.PoolTypeLocal && driver.Remote {
			continue
		}

		if poolType == util.PoolTypeAny && (driver.Name == "cephfs" || driver.Name == "cephobject") {
			continue
		}

		if driver.Name == "dir" {
			drivers = append(drivers, driver.Name)
			continue
		}

		// btrfs can work in user namespaces too. (If source=/some/path/on/btrfs is used.)
		if shared.RunningInUserNS() && (backingFs != "btrfs" || driver.Name != "btrfs") {
			continue
		}

		drivers = append(drivers, driver.Name)
	}

	return drivers
}
