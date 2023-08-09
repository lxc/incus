package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxc/incus/incus/db/cluster"
	"github.com/lxc/incus/incus/db/warningtype"
	"github.com/lxc/incus/incus/instance"
	"github.com/lxc/incus/incus/instance/instancetype"
	"github.com/lxc/incus/incus/response"
	"github.com/lxc/incus/incus/state"
	storagePools "github.com/lxc/incus/incus/storage"
	storageDrivers "github.com/lxc/incus/incus/storage/drivers"
	"github.com/lxc/incus/incus/warnings"
	"github.com/lxc/incus/shared"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/version"
)

// Simply cache used to storage the activated drivers on this LXD instance. This
// allows us to avoid querying the database everytime and API call is made.
var storagePoolUsedDriversCacheVal atomic.Value
var storagePoolSupportedDriversCacheVal atomic.Value
var storagePoolDriversCacheLock sync.Mutex

// readStoragePoolDriversCache returns supported and used storage driver info.
func readStoragePoolDriversCache() ([]api.ServerStorageDriverInfo, map[string]string) {
	usedDrivers := storagePoolUsedDriversCacheVal.Load()
	if usedDrivers == nil {
		usedDrivers = map[string]string{}
	}

	supportedDrivers := storagePoolSupportedDriversCacheVal.Load()
	if supportedDrivers == nil {
		supportedDrivers = []api.ServerStorageDriverInfo{}
	}

	return supportedDrivers.([]api.ServerStorageDriverInfo), usedDrivers.(map[string]string)
}

func storageStartup(s *state.State, forceCheck bool) error {
	// Update the storage drivers supported and used cache in api_1.0.go.
	storagePoolDriversCacheUpdate(s)

	poolNames, err := s.DB.Cluster.GetCreatedStoragePoolNames()
	if err != nil {
		if response.IsNotFoundError(err) {
			logger.Debug("No existing storage pools detected")
			return nil
		}

		return fmt.Errorf("Failed loading existing storage pools: %w", err)
	}

	initPools := make(map[string]struct{}, len(poolNames))
	for _, poolName := range poolNames {
		initPools[poolName] = struct{}{}
	}

	initPool := func(poolName string) bool {
		logger.Debug("Initializing storage pool", logger.Ctx{"pool": poolName})

		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			if response.IsNotFoundError(err) {
				return true // Nothing to activate as pool has been deleted.
			}

			logger.Error("Failed loading storage pool", logger.Ctx{"pool": poolName, "err": err})

			return false
		}

		_, err = pool.Mount()
		if err != nil {
			logger.Error("Failed mounting storage pool", logger.Ctx{"pool": poolName, "err": err})
			_ = s.DB.Cluster.UpsertWarningLocalNode("", cluster.TypeStoragePool, int(pool.ID()), warningtype.StoragePoolUnvailable, err.Error())

			return false
		}

		logger.Info("Initialized storage pool", logger.Ctx{"pool": poolName})
		_ = warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(s.DB.Cluster, "", warningtype.StoragePoolUnvailable, cluster.TypeStoragePool, int(pool.ID()))

		return true
	}

	// Try initializing storage pools in random order.
	for poolName := range initPools {
		if initPool(poolName) {
			// Storage pool initialized successfully so remove it from the list so its not retried.
			delete(initPools, poolName)
		}
	}

	// For any remaining storage pools that were not successfully initialised, we now start a go routine to
	// periodically try to initialize them again in the background.
	if len(initPools) > 0 {
		go func() {
			for {
				t := time.NewTimer(time.Duration(time.Minute))

				select {
				case <-s.ShutdownCtx.Done():
					t.Stop()
					return
				case <-t.C:
					t.Stop()

					// Try initializing remaining storage pools in random order.
					tryInstancesStart := false
					for poolName := range initPools {
						if initPool(poolName) {
							// Storage pool initialized successfully or deleted so
							// remove it from the list so its not retried.
							delete(initPools, poolName)
							tryInstancesStart = true
						}
					}

					if len(initPools) <= 0 {
						logger.Info("All storage pools initialized")
					}

					// At least one remaining storage pool was initialized, check if any
					// instances can now start.
					if tryInstancesStart {
						instances, err := instance.LoadNodeAll(s, instancetype.Any)
						if err != nil {
							logger.Error("Failed loading instances to start", logger.Ctx{"err": err})
						} else {
							instancesStart(s, instances)
						}
					}

					if len(initPools) <= 0 {
						return // Our job here is done.
					}
				}
			}
		}()
	} else {
		logger.Info("All storage pools initialized")
	}

	return nil
}

func storagePoolDriversCacheUpdate(s *state.State) {
	// Get a list of all storage drivers currently in use
	// on this LXD instance. Only do this when we do not already have done
	// this once to avoid unnecessarily querying the db. All subsequent
	// updates of the cache will be done when we create or delete storage
	// pools in the db. Since this is a rare event, this cache
	// implementation is a classic frequent-read, rare-update case so
	// copy-on-write semantics without locking in the read case seems
	// appropriate. (Should be cheaper then querying the db all the time,
	// especially if we keep adding more storage drivers.)
	drivers, err := s.DB.Cluster.GetStoragePoolDrivers()
	if err != nil && !response.IsNotFoundError(err) {
		return
	}

	usedDrivers := map[string]string{}

	// Get the driver info.
	info := storageDrivers.SupportedDrivers(s)
	supportedDrivers := make([]api.ServerStorageDriverInfo, 0, len(info))

	for _, entry := range info {
		supportedDrivers = append(supportedDrivers, api.ServerStorageDriverInfo{
			Name:    entry.Name,
			Version: entry.Version,
			Remote:  entry.Remote,
		})

		if shared.StringInSlice(entry.Name, drivers) {
			usedDrivers[entry.Name] = entry.Version
		}
	}

	// Prepare the cache entries.
	backends := []string{}
	for k, v := range usedDrivers {
		backends = append(backends, fmt.Sprintf("%s %s", k, v))
	}

	// Update the user agent.
	version.UserAgentStorageBackends(backends)

	storagePoolDriversCacheLock.Lock()
	storagePoolUsedDriversCacheVal.Store(usedDrivers)
	storagePoolSupportedDriversCacheVal.Store(supportedDrivers)
	storagePoolDriversCacheLock.Unlock()
}
