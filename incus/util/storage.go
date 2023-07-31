package util

import (
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// PoolType represents a type of storage pool (local, remote or any).
type PoolType string

// PoolTypeAny represents any storage pool (local or remote).
const PoolTypeAny PoolType = ""

// PoolTypeLocal represents local storage pools.
const PoolTypeLocal PoolType = "local"

// PoolTypeRemote represents remote storage pools.
const PoolTypeRemote PoolType = "remote"

// AvailableStorageDrivers returns a list of storage drivers that are available.
func AvailableStorageDrivers(supportedDrivers []api.ServerStorageDriverInfo, poolType PoolType) []string {
	backingFs, err := filesystem.Detect(shared.VarPath())
	if err != nil {
		backingFs = "dir"
	}

	drivers := make([]string, 0, len(supportedDrivers))

	// Check available backends.
	for _, driver := range supportedDrivers {
		if poolType == PoolTypeRemote && !driver.Remote {
			continue
		}

		if poolType == PoolTypeLocal && driver.Remote {
			continue
		}

		if poolType == PoolTypeAny && (driver.Name == "cephfs" || driver.Name == "cephobject") {
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
