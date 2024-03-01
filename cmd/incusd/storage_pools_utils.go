package main

import (
	"context"
	"fmt"

	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/cluster/request"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
)

// storagePoolDBCreate creates a storage pool DB entry and returns the created Pool ID.
func storagePoolDBCreate(ctx context.Context, s *state.State, poolName string, poolDescription string, driver string, config map[string]string) (int64, error) {
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the storage pool does not already exist.
		_, err := tx.GetStoragePoolID(ctx, poolName)

		return err
	})
	if err == nil {
		return -1, fmt.Errorf("The storage pool already exists: %w", db.ErrAlreadyDefined)
	}

	// Make sure that we don't pass a nil to the next function.
	if config == nil {
		config = map[string]string{}
	}

	err = storagePoolValidate(s, poolName, driver, config)
	if err != nil {
		return -1, err
	}

	// Create the database entry for the storage pool.
	id, err := dbStoragePoolCreateAndUpdateCache(ctx, s, poolName, poolDescription, driver, config)
	if err != nil {
		return -1, fmt.Errorf("Error inserting %s into database: %w", poolName, err)
	}

	return id, nil
}

func storagePoolValidate(s *state.State, poolName string, driverName string, config map[string]string) error {
	poolType, err := storagePools.LoadByType(s, driverName)
	if err != nil {
		return err
	}

	// Check if the storage pool name is valid.
	err = poolType.ValidateName(poolName)
	if err != nil {
		return err
	}

	// Validate the requested storage pool configuration.
	err = poolType.Validate(config)
	if err != nil {
		return err
	}

	return nil
}

func storagePoolCreateGlobal(ctx context.Context, s *state.State, req api.StoragePoolsPost, clientType request.ClientType) error {
	// Create the database entry.
	id, err := storagePoolDBCreate(ctx, s, req.Name, req.Description, req.Driver, req.Config)
	if err != nil {
		return err
	}

	// Define a function which reverts everything.  Defer this function
	// so that it doesn't need to be explicitly called in every failing
	// return path. Track whether or not we want to undo the changes
	// using a closure.
	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { _ = dbStoragePoolDeleteAndUpdateCache(context.Background(), s, req.Name) })

	_, err = storagePoolCreateLocal(ctx, s, id, req, clientType)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// This performs local pool setup and updates DB record if config was changed during pool setup.
// Returns resulting config.
func storagePoolCreateLocal(ctx context.Context, s *state.State, poolID int64, req api.StoragePoolsPost, clientType request.ClientType) (map[string]string, error) {
	// Setup revert.
	revert := revert.New()
	defer revert.Fail()

	// Load pool record.
	pool, err := storagePools.LoadByName(s, req.Name)
	if err != nil {
		return nil, err
	}

	if pool.LocalStatus() == api.NetworkStatusCreated {
		logger.Debug("Skipping local storage pool create as already created", logger.Ctx{"pool": pool.Name()})

		return pool.Driver().Config(), nil
	}

	// Create the pool.
	err = pool.Create(clientType, nil)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = pool.Delete(clientType, nil) })

	// Mount the pool.
	_, err = pool.Mount()
	if err != nil {
		return nil, err
	}

	// In case the storage pool config was changed during the pool creation, we need to update the database to
	// reflect this change. This can e.g. happen, when we create a loop file image. This means we append ".img"
	// to the path the user gave us and update the config in the storage callback. So diff the config here to
	// see if something like this has happened.
	configDiff, _ := storagePools.ConfigDiff(req.Config, pool.Driver().Config())
	if len(configDiff) > 0 {
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Update the database entry for the storage pool.
			return tx.UpdateStoragePool(ctx, req.Name, req.Description, pool.Driver().Config())
		})
		if err != nil {
			return nil, fmt.Errorf("Error updating storage pool config after local create for %q: %w", req.Name, err)
		}
	}

	// Set storage pool node to storagePoolCreated.
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.StoragePoolNodeCreated(poolID)
	})
	if err != nil {
		return nil, err
	}

	logger.Debug("Marked storage pool local status as created", logger.Ctx{"pool": req.Name})

	revert.Success()
	return pool.Driver().Config(), nil
}

// Helper around the low-level DB API, which also updates the driver names cache.
func dbStoragePoolCreateAndUpdateCache(ctx context.Context, s *state.State, poolName string, poolDescription string, poolDriver string, poolConfig map[string]string) (int64, error) {
	var id int64

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		id, err = tx.CreateStoragePool(ctx, poolName, poolDescription, poolDriver, poolConfig)

		return err
	})
	if err != nil {
		return id, err
	}

	// Update the storage drivers cache in api_1.0.go.
	storagePoolDriversCacheUpdate(ctx, s)

	return id, nil
}

// Helper around the low-level DB API, which also updates the driver names
// cache.
func dbStoragePoolDeleteAndUpdateCache(ctx context.Context, s *state.State, poolName string) error {
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.RemoveStoragePool(ctx, poolName)

		return err
	})
	if err != nil {
		return err
	}

	// Update the storage drivers cache in api_1.0.go.
	storagePoolDriversCacheUpdate(ctx, s)

	return err
}
