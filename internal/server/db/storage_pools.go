//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db/query"
	"github.com/lxc/incus/v6/shared/api"
)

// StorageRemoteDriverNames returns a list of remote storage driver names.
var StorageRemoteDriverNames func() []string

// GetStoragePoolsLocalConfig returns a map associating each storage pool name to
// its node-specific config values (i.e. the ones where node_id is not NULL).
func (c *ClusterTx) GetStoragePoolsLocalConfig(ctx context.Context) (map[string]map[string]string, error) {
	names, err := query.SelectStrings(ctx, c.tx, "SELECT name FROM storage_pools")
	if err != nil {
		return nil, err
	}

	pools := make(map[string]map[string]string, len(names))
	for _, name := range names {
		table := `
storage_pools_config JOIN storage_pools ON storage_pools.id=storage_pools_config.storage_pool_id
`
		config, err := query.SelectConfig(ctx, c.tx, table, "storage_pools.name=? AND storage_pools_config.node_id=?",
			name, c.nodeID)
		if err != nil {
			return nil, err
		}

		pools[name] = config
	}

	return pools, nil
}

// GetStoragePoolID returns the ID of the pool with the given name.
func (c *ClusterTx) GetStoragePoolID(ctx context.Context, name string) (int64, error) {
	stmt := "SELECT id FROM storage_pools WHERE name=?"
	ids, err := query.SelectIntegers(ctx, c.tx, stmt, name)
	if err != nil {
		return -1, err
	}

	switch len(ids) {
	case 0:
		return -1, api.StatusErrorf(http.StatusNotFound, "Storage pool not found")
	case 1:
		return int64(ids[0]), nil
	default:
		return -1, fmt.Errorf("More than one pool has the given name")
	}
}

// GetStoragePoolDriver returns the driver of the pool with the given ID.
func (c *ClusterTx) GetStoragePoolDriver(ctx context.Context, id int64) (string, error) {
	stmt := "SELECT driver FROM storage_pools WHERE id=?"
	drivers, err := query.SelectStrings(ctx, c.tx, stmt, id)
	if err != nil {
		return "", err
	}

	switch len(drivers) {
	case 0:
		return "", api.StatusErrorf(http.StatusNotFound, "Storage pool not found")
	case 1:
		return drivers[0], nil
	default:
		return "", fmt.Errorf("More than one pool has the given id")
	}
}

// GetNonPendingStoragePoolsNamesToIDs returns a map associating each storage pool name to its ID.
//
// Pending storage pools are skipped.
func (c *ClusterTx) GetNonPendingStoragePoolsNamesToIDs(ctx context.Context) (map[string]int64, error) {
	type pool struct {
		id   int64
		name string
	}

	sql := "SELECT id, name FROM storage_pools WHERE NOT state=?"
	pools := []pool{}
	err := query.Scan(ctx, c.tx, sql, func(scan func(dest ...any) error) error {
		var p pool
		err := scan(&p.id, &p.name)
		if err != nil {
			return err
		}

		pools = append(pools, p)

		return nil
	}, StoragePoolPending)
	if err != nil {
		return nil, err
	}

	ids := map[string]int64{}
	for _, pool := range pools {
		ids[pool.name] = pool.id
	}

	return ids, nil
}

// UpdateStoragePoolAfterNodeJoin adds a new entry in the storage_pools_nodes table.
//
// It should only be used when a new node joins the cluster, when it's safe to
// assume that the relevant pool has already been created on the joining node,
// and we just need to track it.
func (c *ClusterTx) UpdateStoragePoolAfterNodeJoin(poolID, nodeID int64) error {
	columns := []string{"storage_pool_id", "node_id", "state"}
	// Create storage pool node with storagePoolCreated state as we expect the pool to already be setup.
	values := []any{poolID, nodeID, StoragePoolCreated}
	_, err := query.UpsertObject(c.tx, "storage_pools_nodes", columns, values)
	if err != nil {
		return fmt.Errorf("failed to add storage pools node entry: %w", err)
	}

	return nil
}

// UpdateRemoteStoragePoolAfterNodeJoin updates internal state to reflect that nodeID is
// joining a cluster where poolID is a remote pool.
func (c *ClusterTx) UpdateRemoteStoragePoolAfterNodeJoin(ctx context.Context, poolID int64, nodeID int64) error {
	// Get the IDs of the other servers (they should be all linked to the pool).
	stmt := "SELECT node_id FROM storage_pools_nodes WHERE storage_pool_id=?"
	nodeIDs, err := query.SelectIntegers(ctx, c.tx, stmt, poolID)
	if err != nil {
		return fmt.Errorf("Failed to fetch IDs of servers with remote pool: %w", err)
	}

	if len(nodeIDs) == 0 {
		return fmt.Errorf("Remote pool is not linked to any server")
	}

	otherNodeID := nodeIDs[0]

	// Create entries of all the volumes for the new server.
	_, err = c.tx.Exec(`
INSERT INTO storage_volumes(name, storage_pool_id, node_id, type, description, project_id)
  SELECT name, storage_pool_id, ?, type, description, 1
    FROM storage_volumes WHERE storage_pool_id=? AND node_id=?
`, nodeID, poolID, otherNodeID)
	if err != nil {
		return fmt.Errorf("Failed to create volumes: %w", err)
	}

	// Create entries of all the volumes configs for the new server.
	stmt = `
SELECT id FROM storage_volumes WHERE storage_pool_id=? AND node_id=?
  ORDER BY name, type
`
	volumeIDs, err := query.SelectIntegers(ctx, c.tx, stmt, poolID, nodeID)
	if err != nil {
		return fmt.Errorf("Failed to get joining server's volume IDs: %w", err)
	}

	otherVolumeIDs, err := query.SelectIntegers(ctx, c.tx, stmt, poolID, otherNodeID)
	if err != nil {
		return fmt.Errorf("Failed to get other server's volume IDs: %w", err)
	}

	if len(volumeIDs) != len(otherVolumeIDs) { // Quick check.
		return fmt.Errorf("Not all remote volumes were copied")
	}

	for i, otherVolumeID := range otherVolumeIDs {
		volumeID := volumeIDs[i]
		config, err := query.SelectConfig(ctx, c.tx, "storage_volumes_config", "storage_volume_id=?", otherVolumeID)
		if err != nil {
			return fmt.Errorf("Failed to get storage volume config: %w", err)
		}

		for key, value := range config {
			_, err := c.tx.Exec(`
INSERT INTO storage_volumes_config(storage_volume_id, key, value) VALUES(?, ?, ?)
`, volumeID, key, value)
			if err != nil {
				return fmt.Errorf("Failed to copy volume config: %w", err)
			}
		}

		// Copy volume snapshots as well.
		otherSnapshotIDs, err := query.SelectIntegers(ctx, c.tx, "SELECT id FROM storage_volumes_snapshots WHERE storage_volume_id = ?",
			otherVolumeID)
		if err != nil {
			return err
		}

		for _, otherSnapshotID := range otherSnapshotIDs {
			var snapshotID int64
			_, err := c.tx.Exec("UPDATE sqlite_sequence SET seq = seq + 1 WHERE name = 'storage_volumes'")
			if err != nil {
				return fmt.Errorf("Increment storage volumes sequence: %w", err)
			}

			row := c.tx.QueryRowContext(ctx, "SELECT seq FROM sqlite_sequence WHERE name = 'storage_volumes' LIMIT 1")
			err = row.Scan(&snapshotID)
			if err != nil {
				return fmt.Errorf("Fetch next storage volume ID: %w", err)
			}

			_, err = c.tx.Exec(`
INSERT INTO storage_volumes_snapshots (id, storage_volume_id, name, description)
SELECT ?, ?, name, description
  FROM storage_volumes_snapshots WHERE id=?
`, snapshotID, volumeID, otherSnapshotID)
			if err != nil {
				return fmt.Errorf("Copy volume snapshot: %w", err)
			}

			_, err = c.tx.Exec(`
INSERT INTO storage_volumes_snapshots_config (storage_volume_snapshot_id, key, value)
SELECT ?, key, value
  FROM storage_volumes_snapshots_config
 WHERE storage_volume_snapshot_id=?
`, snapshotID, otherSnapshotID)
			if err != nil {
				return fmt.Errorf("Copy volume snapshot config: %w", err)
			}
		}
	}

	return nil
}

// CreateStoragePoolConfig adds a new entry in the storage_pools_config table.
func (c *ClusterTx) CreateStoragePoolConfig(poolID, nodeID int64, config map[string]string) error {
	return storagePoolConfigAdd(c.tx, poolID, nodeID, config)
}

// StoragePoolState indicates the state of the storage pool or storage pool node.
type StoragePoolState int

// Storage pools state.
const (
	StoragePoolPending StoragePoolState = iota // Storage pool defined but not yet created globally or on specific node.
	StoragePoolCreated                         // Storage pool created globally or on specific node.
	storagePoolErrored                         // Deprecated (should no longer occur).
)

// StoragePoolNode represents a storage pool node.
type StoragePoolNode struct {
	ID    int64
	Name  string
	State StoragePoolState
}

// CreatePendingStoragePool creates a new pending storage pool on the node with
// the given name.
func (c *ClusterTx) CreatePendingStoragePool(ctx context.Context, node string, name string, driver string, conf map[string]string) error {
	// First check if a storage pool with the given name exists, and, if
	// so, that it has a matching driver and it's in the pending state.
	pool := struct {
		id     int64
		driver string
		state  StoragePoolState
	}{}

	sql := "SELECT id, driver, state FROM storage_pools WHERE name=?"
	count := 0
	err := query.Scan(ctx, c.tx, sql, func(scan func(dest ...any) error) error {
		// Ensure that there is at most one pool with the given name.
		if count != 0 {
			return fmt.Errorf("more than one pool exists with the given name")
		}

		count++

		return scan(&pool.id, &pool.driver, &pool.state)
	}, name)
	if err != nil {
		return err
	}

	var poolID = pool.id
	if poolID == 0 {
		// No existing pool with the given name was found, let's create
		// one.
		columns := []string{"name", "driver", "description"}
		values := []any{name, driver, ""}
		poolID, err = query.UpsertObject(c.tx, "storage_pools", columns, values)
		if err != nil {
			return err
		}
	} else {
		// Check that the existing pools matches the given driver and
		// is in the pending state.
		if pool.driver != driver {
			return fmt.Errorf("Storage pool already exists with a different driver")
		}

		if pool.state != StoragePoolPending {
			return fmt.Errorf("Storage pool is not in pending state")
		}
	}

	// Get the ID of the node with the given name.
	nodeInfo, err := c.GetNodeByName(ctx, node)
	if err != nil {
		return err
	}

	// Check that no storage_pool entry of this node and pool exists yet.
	count, err = query.Count(ctx, c.tx, "storage_pools_nodes", "storage_pool_id=? AND node_id=?", poolID, nodeInfo.ID)
	if err != nil {
		return err
	}

	if count != 0 {
		return ErrAlreadyDefined
	}

	// Insert a node-specific entry pointing to ourselves with state storagePoolPending.
	columns := []string{"storage_pool_id", "node_id", "state"}
	values := []any{poolID, nodeInfo.ID, StoragePoolPending}
	_, err = query.UpsertObject(c.tx, "storage_pools_nodes", columns, values)
	if err != nil {
		return err
	}

	err = c.CreateStoragePoolConfig(poolID, nodeInfo.ID, conf)
	if err != nil {
		return err
	}

	return nil
}

// StoragePoolCreated sets the state of the given pool to storagePoolCreated.
func (c *ClusterTx) StoragePoolCreated(name string) error {
	return c.storagePoolState(name, StoragePoolCreated)
}

// StoragePoolErrored sets the state of the given pool to storagePoolErrored.
func (c *ClusterTx) StoragePoolErrored(name string) error {
	return c.storagePoolState(name, storagePoolErrored)
}

func (c *ClusterTx) storagePoolState(name string, state StoragePoolState) error {
	stmt := "UPDATE storage_pools SET state=? WHERE name=?"
	result, err := c.tx.Exec(stmt, state, name)
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if n != 1 {
		return api.StatusErrorf(http.StatusNotFound, "Storage pool not found")
	}

	return nil
}

// storagePoolNodes returns the nodes keyed by node ID that the given storage pool is defined on.
func (c *ClusterTx) storagePoolNodes(ctx context.Context, poolID int64) (map[int64]StoragePoolNode, error) {
	nodes := []StoragePoolNode{}
	sql := `
		SELECT nodes.id, nodes.name, storage_pools_nodes.state FROM nodes
		JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id
		WHERE storage_pools_nodes.storage_pool_id = ?
	`

	err := query.Scan(ctx, c.tx, sql, func(scan func(dest ...any) error) error {
		node := StoragePoolNode{}

		err := scan(&node.ID, &node.Name, &node.State)
		if err != nil {
			return err
		}

		nodes = append(nodes, node)

		return nil
	}, poolID)
	if err != nil {
		return nil, err
	}

	poolNodes := map[int64]StoragePoolNode{}
	for _, node := range nodes {
		poolNodes[node.ID] = node
	}

	return poolNodes, nil
}

// StoragePoolNodeCreated sets the state of the given storage pool for the local member to storagePoolCreated.
func (c *ClusterTx) StoragePoolNodeCreated(poolID int64) error {
	return c.storagePoolNodeState(poolID, StoragePoolCreated)
}

// storagePoolNodeState updates the storage pool member state for the local member and specified network ID.
func (c *ClusterTx) storagePoolNodeState(poolID int64, state StoragePoolState) error {
	stmt := "UPDATE storage_pools_nodes SET state=? WHERE storage_pool_id = ? and node_id = ?"
	result, err := c.tx.Exec(stmt, state, poolID, c.nodeID)
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if n != 1 {
		return api.StatusErrorf(http.StatusNotFound, "Storage pool not found")
	}

	return nil
}

// GetStoragePools returns map of Storage Pools keyed on ID and Storage Pool member info keyed on ID and Member ID.
// Can optionally accept a state filter, if nil, then pools in any state are returned.
// Can optionally accept one or more poolNames to further filter the returned pools.
func (c *ClusterTx) GetStoragePools(ctx context.Context, state *StoragePoolState, poolNames ...string) (map[int64]api.StoragePool, map[int64]map[int64]StoragePoolNode, error) {
	var q *strings.Builder = &strings.Builder{}
	var args []any

	q.WriteString("SELECT id, name, driver, description, state FROM storage_pools ")

	if state != nil {
		q.WriteString("WHERE storage_pools.state = ? ")
		args = append(args, *state)
	}

	if len(poolNames) > 0 {
		verb := "WHERE"
		if len(args) > 0 {
			verb = "AND"
		}

		q.WriteString(fmt.Sprintf("%s storage_pools.name IN %s", verb, query.Params(len(poolNames))))
		for _, poolName := range poolNames {
			args = append(args, poolName)
		}
	}

	var err error
	pools := make(map[int64]api.StoragePool)
	memberInfo := make(map[int64]map[int64]StoragePoolNode)

	err = query.Scan(ctx, c.tx, q.String(), func(scan func(dest ...any) error) error {
		var poolID int64 = int64(-1)
		var poolState StoragePoolState
		var pool api.StoragePool

		err := scan(&poolID, &pool.Name, &pool.Driver, &pool.Description, &poolState)
		if err != nil {
			return err
		}

		pool.Status = StoragePoolStateToAPIStatus(poolState)

		pools[poolID] = pool

		return nil
	}, args...)
	if err != nil {
		return nil, nil, err
	}

	for poolID := range pools {
		pool := pools[poolID]

		err = c.getStoragePoolConfig(ctx, c, poolID, &pool)
		if err != nil {
			return nil, nil, err
		}

		memberInfo[poolID], err = c.storagePoolNodes(ctx, poolID)
		if err != nil {
			return nil, nil, err
		}

		pool.Locations = make([]string, 0, len(memberInfo[poolID]))
		for _, node := range memberInfo[poolID] {
			pool.Locations = append(pool.Locations, node.Name)
		}

		pools[poolID] = pool
	}

	return pools, memberInfo, nil
}

// GetStoragePoolNodeConfigs returns the node-specific configuration of all
// nodes grouped by node name, for the given poolID.
//
// If the storage pool is not defined on all nodes, an error is returned.
func (c *ClusterTx) GetStoragePoolNodeConfigs(ctx context.Context, poolID int64) (map[string]map[string]string, error) {
	// Fetch all nodes.
	nodes, err := c.GetNodes(ctx)
	if err != nil {
		return nil, err
	}

	// Fetch the names of the nodes where the storage pool is defined.
	stmt := `
SELECT nodes.name FROM nodes
  LEFT JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id
  LEFT JOIN storage_pools ON storage_pools_nodes.storage_pool_id = storage_pools.id
WHERE storage_pools.id = ? AND storage_pools.state = ?
`
	defined, err := query.SelectStrings(ctx, c.tx, stmt, poolID, StoragePoolPending)
	if err != nil {
		return nil, err
	}

	// Figure which nodes are missing
	missing := []string{}
	for _, node := range nodes {
		if !slices.Contains(defined, node.Name) {
			missing = append(missing, node.Name)
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("Pool not defined on nodes: %s", strings.Join(missing, ", "))
	}

	configs := map[string]map[string]string{}
	for _, node := range nodes {
		config, err := query.SelectConfig(ctx, c.tx, "storage_pools_config", "storage_pool_id=? AND node_id=?", poolID, node.ID)
		if err != nil {
			return nil, err
		}

		configs[node.Name] = config
	}

	return configs, nil
}

// GetStoragePoolNames returns the names of all storage pools.
func (c *ClusterTx) GetStoragePoolNames(ctx context.Context) ([]string, error) {
	return c.storagePools(ctx, "")
}

// GetCreatedStoragePoolNames returns the names of all storage pools that are created.
func (c *ClusterTx) GetCreatedStoragePoolNames(ctx context.Context) ([]string, error) {
	return c.storagePools(ctx, "state=?", StoragePoolCreated)
}

// Get all storage pools matching the given WHERE filter (if given).
func (c *ClusterTx) storagePools(ctx context.Context, where string, args ...any) ([]string, error) {
	var name string
	stmt := "SELECT name FROM storage_pools"
	inargs := []any{}
	outargs := []any{name}

	if where != "" {
		stmt += fmt.Sprintf(" WHERE %s", where)
		inargs = append(inargs, args...)
	}

	result, err := queryScan(ctx, c, stmt, inargs, outargs)
	if err != nil {
		return []string{}, err
	}

	if len(result) == 0 {
		return []string{}, api.StatusErrorf(http.StatusNotFound, "Storage pool(s) not found")
	}

	pools := []string{}
	for _, r := range result {
		pools = append(pools, r[0].(string))
	}

	return pools, nil
}

// GetStoragePoolDrivers returns the names of all storage drivers currently
// being used by at least one storage pool.
func (c *ClusterTx) GetStoragePoolDrivers(ctx context.Context) ([]string, error) {
	var poolDriver string
	query := "SELECT DISTINCT driver FROM storage_pools"
	inargs := []any{}
	outargs := []any{poolDriver}

	result, err := queryScan(ctx, c, query, inargs, outargs)
	if err != nil {
		return []string{}, err
	}

	if len(result) == 0 {
		return []string{}, api.StatusErrorf(http.StatusNotFound, "Storage pool(s) not found")
	}

	drivers := []string{}
	for _, driver := range result {
		drivers = append(drivers, driver[0].(string))
	}

	return drivers, nil
}

// GetStoragePool returns a single storage pool.
//
// The pool must be in the created stated, not pending.
func (c *ClusterTx) GetStoragePool(ctx context.Context, poolName string) (int64, *api.StoragePool, map[int64]StoragePoolNode, error) {
	stateCreated := StoragePoolCreated
	pools, poolMembers, err := c.GetStoragePools(ctx, &stateCreated, poolName)
	if (err == nil && len(pools) <= 0) || errors.Is(err, sql.ErrNoRows) {
		return -1, nil, nil, api.StatusErrorf(http.StatusNotFound, "Storage pool not found")
	} else if err == nil && len(pools) > 1 {
		return -1, nil, nil, api.StatusErrorf(http.StatusConflict, "More than 1 storage pool found for that name")
	} else if err != nil {
		return -1, nil, nil, err
	}

	for poolID, pool := range pools {
		return poolID, &pool, poolMembers[poolID], err // Only single pool in map.
	}

	return -1, nil, nil, fmt.Errorf("Unexpected pool list size")
}

// GetStoragePoolInAnyState returns the storage pool with the given name.
//
// The pool can be in any state.
func (c *ClusterTx) GetStoragePoolInAnyState(ctx context.Context, poolName string) (int64, *api.StoragePool, map[int64]StoragePoolNode, error) {
	pools, poolMembers, err := c.GetStoragePools(ctx, nil, poolName)
	if (err == nil && len(pools) <= 0) || errors.Is(err, sql.ErrNoRows) {
		return -1, nil, nil, api.StatusErrorf(http.StatusNotFound, "Storage pool not found")
	} else if err == nil && len(pools) > 1 {
		return -1, nil, nil, api.StatusErrorf(http.StatusConflict, "More than 1 storage pool found for that name")
	} else if err != nil {
		return -1, nil, nil, err
	}

	for poolID, pool := range pools {
		return poolID, &pool, poolMembers[poolID], err // Only single pool in map.
	}

	return -1, nil, nil, fmt.Errorf("Unexpected pool list size")
}

// GetStoragePoolWithID returns the storage pool with the given ID.
func (c *ClusterTx) GetStoragePoolWithID(ctx context.Context, poolID int) (int64, *api.StoragePool, map[int64]StoragePoolNode, error) {
	return c.getStoragePool(ctx, true, "id=?", poolID)
}

// GetStoragePool returns a single storage pool.
func (c *ClusterTx) getStoragePool(ctx context.Context, onlyCreated bool, where string, args ...any) (int64, *api.StoragePool, map[int64]StoragePoolNode, error) {
	var err error
	var q *strings.Builder = &strings.Builder{}
	q.WriteString("SELECT id, name, driver, description, state FROM storage_pools WHERE ")
	q.WriteString(where)

	if onlyCreated {
		q.WriteString(" AND state=?")
		args = append(args, StoragePoolCreated)
	}

	poolID := int64(-1)
	var pool api.StoragePool
	var nodes map[int64]StoragePoolNode

	var state StoragePoolState

	err = c.tx.QueryRowContext(ctx, q.String(), args...).Scan(&poolID, &pool.Name, &pool.Driver, &pool.Description, &state)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, nil, nil, api.StatusErrorf(http.StatusNotFound, "Storage pool not found")
		}

		return -1, nil, nil, err
	}

	pool.Status = StoragePoolStateToAPIStatus(state)

	err = c.getStoragePoolConfig(ctx, c, poolID, &pool)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, nil, nil, api.StatusErrorf(http.StatusNotFound, "Storage pool not found")
		}

		return -1, nil, nil, err
	}

	nodes, err = c.storagePoolNodes(ctx, poolID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, nil, nil, api.StatusErrorf(http.StatusNotFound, "Storage pool not found")
		}

		return -1, nil, nil, err
	}

	pool.Locations = make([]string, 0, len(nodes))
	for _, node := range nodes {
		pool.Locations = append(pool.Locations, node.Name)
	}

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, nil, nil, api.StatusErrorf(http.StatusNotFound, "Storage pool not found")
		}

		return -1, nil, nil, err
	}

	return poolID, &pool, nodes, nil
}

// StoragePoolStateToAPIStatus converts DB StoragePoolState to API status string.
func StoragePoolStateToAPIStatus(state StoragePoolState) string {
	switch state {
	case StoragePoolPending:
		return api.StoragePoolStatusPending
	case StoragePoolCreated:
		return api.StoragePoolStatusCreated
	case storagePoolErrored:
		return api.StoragePoolStatusErrored
	default:
		return api.StoragePoolStatusUnknown
	}
}

// getStoragePoolConfig populates the config map of the Storage pool with the given ID.
func (c *ClusterTx) getStoragePoolConfig(ctx context.Context, tx *ClusterTx, poolID int64, pool *api.StoragePool) error {
	q := "SELECT key, value FROM storage_pools_config WHERE storage_pool_id=? AND (node_id=? OR node_id IS NULL)"

	pool.Config = map[string]string{}

	return query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := pool.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for storage pool ID %d", key, poolID)
		}

		pool.Config[key] = value

		return nil
	}, poolID, c.nodeID)
}

// CreateStoragePool creates new storage pool. Also creates a local member entry with state storagePoolPending.
func (c *ClusterTx) CreateStoragePool(ctx context.Context, poolName string, poolDescription string, poolDriver string, poolConfig map[string]string) (int64, error) {
	var id int64
	result, err := c.tx.ExecContext(ctx, "INSERT INTO storage_pools (name, description, driver, state) VALUES (?, ?, ?, ?)", poolName, poolDescription, poolDriver, StoragePoolCreated)
	if err != nil {
		return -1, err
	}

	id, err = result.LastInsertId()
	if err != nil {
		return -1, err
	}

	// Insert a node-specific entry pointing to ourselves with state storagePoolPending.
	columns := []string{"storage_pool_id", "node_id", "state"}
	values := []any{id, c.nodeID, StoragePoolPending}
	_, err = query.UpsertObject(c.tx, "storage_pools_nodes", columns, values)
	if err != nil {
		return -1, err
	}

	err = storagePoolConfigAdd(c.tx, id, c.nodeID, poolConfig)
	if err != nil {
		return -1, err
	}

	return id, nil
}

// Add new storage pool config.
func storagePoolConfigAdd(tx *sql.Tx, poolID, nodeID int64, poolConfig map[string]string) error {
	str := "INSERT INTO storage_pools_config (storage_pool_id, node_id, key, value) VALUES(?, ?, ?, ?)"
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for k, v := range poolConfig {
		if v == "" {
			continue
		}

		var nodeIDValue any
		if !slices.Contains(NodeSpecificStorageConfig, k) {
			nodeIDValue = nil
		} else {
			nodeIDValue = nodeID
		}

		_, err = stmt.Exec(poolID, nodeIDValue, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

// UpdateStoragePool updates a storage pool.
func (c *ClusterTx) UpdateStoragePool(ctx context.Context, poolName, description string, poolConfig map[string]string) error {
	poolID, _, _, err := c.GetStoragePoolInAnyState(ctx, poolName)
	if err != nil {
		return err
	}

	err = updateStoragePoolDescription(c.tx, poolID, description)
	if err != nil {
		return err
	}

	err = clearStoragePoolConfig(c.tx, poolID, c.nodeID)
	if err != nil {
		return err
	}

	err = storagePoolConfigAdd(c.tx, poolID, c.nodeID, poolConfig)
	if err != nil {
		return err
	}

	return nil
}

// Uupdate the storage pool description.
func updateStoragePoolDescription(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE storage_pools SET description=? WHERE id=?", description, id)
	return err
}

// Delete the storage pool config.
func clearStoragePoolConfig(tx *sql.Tx, poolID, nodeID int64) error {
	_, err := tx.Exec("DELETE FROM storage_pools_config WHERE storage_pool_id=? AND (node_id=? OR node_id IS NULL)", poolID, nodeID)
	if err != nil {
		return err
	}

	return nil
}

// RemoveStoragePool deletes storage pool.
func (c *ClusterTx) RemoveStoragePool(ctx context.Context, poolName string) (*api.StoragePool, error) {
	poolID, pool, _, err := c.GetStoragePoolInAnyState(ctx, poolName)
	if err != nil {
		return nil, err
	}

	_, err = c.tx.ExecContext(ctx, "DELETE FROM storage_pools WHERE id=?", poolID)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

// NodeSpecificStorageConfig lists all storage pool config keys which are node-specific.
var NodeSpecificStorageConfig = []string{
	"size",
	"source",
	"source.wipe",
	"volatile.initial_source",
	"zfs.pool_name",
	"lvm.thinpool_name",
	"lvm.vg_name",
	"lvm.vg.force_reuse",
}

// IsRemoteStorage return whether a given pool is backed by remote storage.
func (c *ClusterTx) IsRemoteStorage(ctx context.Context, poolID int64) (bool, error) {
	driver, err := c.GetStoragePoolDriver(ctx, poolID)
	if err != nil {
		return false, err
	}

	isRemoteStorage := slices.Contains(StorageRemoteDriverNames(), driver)

	return isRemoteStorage, nil
}
