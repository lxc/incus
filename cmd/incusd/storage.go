package main

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/warningtype"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/state"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	storageDrivers "github.com/lxc/incus/v6/internal/server/storage/drivers"
	"github.com/lxc/incus/v6/internal/server/warnings"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// Simple cache used to store the activated drivers on this server.
// This allows us to avoid querying the database everytime and API call is made.
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
	storagePoolDriversCacheUpdate(s.ShutdownCtx, s)

	var poolNames []string

	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		poolNames, err = tx.GetCreatedStoragePoolNames(ctx)

		return err
	})
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
			_ = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.UpsertWarningLocalNode(ctx, "", cluster.TypeStoragePool, int(pool.ID()), warningtype.StoragePoolUnvailable, err.Error())
			})

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

	// For any remaining storage pools that were not successfully initialized, we now start a go routine to
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

func storagePoolDriversCacheUpdate(ctx context.Context, s *state.State) {
	// Get a list of all storage drivers currently in use
	// on this server. Only do this when we do not already have done
	// this once to avoid unnecessarily querying the db. All subsequent
	// updates of the cache will be done when we create or delete storage
	// pools in the db. Since this is a rare event, this cache
	// implementation is a classic frequent-read, rare-update case so
	// copy-on-write semantics without locking in the read case seems
	// appropriate. (Should be cheaper then querying the db all the time,
	// especially if we keep adding more storage drivers.)

	var drivers []string

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		drivers, err = tx.GetStoragePoolDrivers(ctx)

		return err
	})
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

		if slices.Contains(drivers, entry.Name) {
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
