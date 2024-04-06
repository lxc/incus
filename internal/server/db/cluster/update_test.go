package cluster_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/query"
	"github.com/lxc/incus/v6/shared/osarch"
)

func TestUpdateFromV0(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(1, nil)
	require.NoError(t, err)

	stmt := "INSERT INTO nodes VALUES (1, 'foo', 'blah', '1.2.3.4:666', 1, 32, ?, 0)"
	_, err = db.Exec(stmt, time.Now())
	require.NoError(t, err)

	// Unique constraint on name
	stmt = "INSERT INTO nodes VALUES (2, 'foo', 'gosh', '5.6.7.8:666', 5, 20, ?, 0)"
	_, err = db.Exec(stmt, time.Now())
	require.Error(t, err)

	// Unique constraint on address
	stmt = "INSERT INTO nodes VALUES (3, 'bar', 'gasp', '1.2.3.4:666', 9, 11), ?, 0)"
	_, err = db.Exec(stmt, time.Now())
	require.Error(t, err)
}

func TestUpdateFromV1_Certificates(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO certificates VALUES (1, 'abcd:efgh', 1, 'foo', 'FOO')")
	require.NoError(t, err)

	// Unique constraint on fingerprint.
	_, err = db.Exec("INSERT INTO certificates VALUES (2, 'abcd:efgh', 2, 'bar', 'BAR')")
	require.Error(t, err)
}

func TestUpdateFromV1_Config(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO config VALUES (1, 'foo', 'blah')")
	require.NoError(t, err)

	// Unique constraint on key.
	_, err = db.Exec("INSERT INTO config VALUES (2, 'foo', 'gosh')")
	require.Error(t, err)
}

func TestUpdateFromV1_Containers(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (1, 'one', '', '1.1.1.1', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (2, 'two', '', '2.2.2.2', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	_, err = db.Exec(`
INSERT INTO containers VALUES (1, 1, 'jammy', 1, 1, 0, ?, 0, ?, 'Jammy Jellyfish')
`, time.Now(), time.Now())
	require.NoError(t, err)

	// Unique constraint on name
	_, err = db.Exec(`
INSERT INTO containers VALUES (2, 2, 'jammy', 2, 2, 1, ?, 1, ?, 'Ubuntu LTS')
`, time.Now(), time.Now())
	require.Error(t, err)

	// Cascading delete
	_, err = db.Exec("INSERT INTO containers_config VALUES (1, 1, 'thekey', 'thevalue')")
	require.NoError(t, err)
	_, err = db.Exec("DELETE FROM containers")
	require.NoError(t, err)
	result, err := db.Exec("DELETE FROM containers_config")
	require.NoError(t, err)
	n, err := result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n) // The row was already deleted by the previous query
}

func TestUpdateFromV1_Network(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO networks VALUES (1, 'foo', 'blah', 1)")
	require.NoError(t, err)

	// Unique constraint on name.
	_, err = db.Exec("INSERT INTO networks VALUES (2, 'foo', 'gosh', 1)")
	require.Error(t, err)
}

func TestUpdateFromV1_ConfigTables(t *testing.T) {
	testConfigTable(t, "networks", func(db *sql.DB) {
		_, err := db.Exec("INSERT INTO networks VALUES (1, 'foo', 'blah', 1)")
		require.NoError(t, err)
	})
	testConfigTable(t, "storage_pools", func(db *sql.DB) {
		_, err := db.Exec("INSERT INTO storage_pools VALUES (1, 'default', 'dir', '')")
		require.NoError(t, err)
	})
}

func testConfigTable(t *testing.T, table string, setup func(db *sql.DB)) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (1, 'one', '', '1.1.1.1', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (2, 'two', '', '2.2.2.2', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	stmt := func(format string) string {
		return fmt.Sprintf(format, table)
	}

	setup(db)

	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (1, 1, 1, 'bar', 'baz')"))
	require.NoError(t, err)

	// Unique constraint on <entity>_id/node_id/key.
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (2, 1, 1, 'bar', 'egg')"))
	require.Error(t, err)
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (3, 1, 2, 'bar', 'egg')"))
	require.NoError(t, err)

	// Reference constraint on <entity>_id.
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (4, 2, 1, 'fuz', 'buz')"))
	require.Error(t, err)

	// Reference constraint on node_id.
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (5, 1, 3, 'fuz', 'buz')"))
	require.Error(t, err)

	// Cascade deletes on node_id
	result, err := db.Exec("DELETE FROM nodes WHERE id=2")
	require.NoError(t, err)
	n, err := result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	result, err = db.Exec(stmt("UPDATE %s_config SET value='yuk'"))
	require.NoError(t, err)
	n, err = result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n) // Only one row was affected, since the other got deleted

	// Cascade deletes on <entity>_id
	result, err = db.Exec(stmt("DELETE FROM %s"))
	require.NoError(t, err)
	n, err = result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	result, err = db.Exec(stmt("DELETE FROM %s_config"))
	require.NoError(t, err)
	n, err = result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n) // The row was already deleted by the previous query
}

func TestUpdateFromV2(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(3, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (1, 'one', '', '1.1.1.1', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO operations VALUES (1, 'abcd', 1)")
	require.NoError(t, err)

	// Unique constraint on uuid
	_, err = db.Exec("INSERT INTO operations VALUES (2, 'abcd', 1)")
	require.Error(t, err)

	// Cascade delete on node_id
	_, err = db.Exec("DELETE FROM nodes")
	require.NoError(t, err)
	result, err := db.Exec("DELETE FROM operations")
	require.NoError(t, err)
	n, err := result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

func TestUpdateFromV3(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(4, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (1, 'c1', '', '1.1.1.1', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'zfs', '', 0)")
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO storage_pools_nodes VALUES (1, 1, 1)")
	require.NoError(t, err)

	// Unique constraint on storage_pool_id/node_id
	_, err = db.Exec("INSERT INTO storage_pools_nodes VALUES (1, 1, 1)")
	require.Error(t, err)
}

func TestUpdateFromV5(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(6, func(db *sql.DB) {
		// Create two nodes.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now())
		require.NoError(t, err)
		_, err = db.Exec(
			"INSERT INTO nodes VALUES (2, 'n2', '', '5.6.7.8:666', 1, 32, ?, 0)",
			time.Now())
		require.NoError(t, err)

		// Create a pool p1 of type zfs.
		_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'zfs', '', 0)")
		require.NoError(t, err)

		// Create a pool p2 of type ceph.
		_, err = db.Exec("INSERT INTO storage_pools VALUES (2, 'p2', 'ceph', '', 0)")

		// Create a volume v1 on pool p1, associated with n1 and a config.
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (1, 'v1', 1, 1, 1, '')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes_config VALUES (1, 1, 'k', 'v')")
		require.NoError(t, err)

		// Create a volume v1 on pool p2, associated with n1 and a config.
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (2, 'v1', 2, 1, 1, '')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes_config VALUES (2, 2, 'k', 'v')")
		require.NoError(t, err)

		// Create a volume v2 on pool p2, associated with n2 and no config.
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (3, 'v2', 2, 2, 1, '')")
		require.NoError(t, err)
	})
	require.NoError(t, err)

	// Check that a volume row for n2 was added for v1 on p2.
	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	nodeIDs, err := query.SelectIntegers(context.Background(), tx, `
SELECT node_id FROM storage_volumes WHERE storage_pool_id=2 AND name='v1' ORDER BY node_id
`)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, nodeIDs)

	// Check that a volume row for n1 was added for v2 on p2.
	nodeIDs, err = query.SelectIntegers(context.Background(), tx, `
SELECT node_id FROM storage_volumes WHERE storage_pool_id=2 AND name='v2' ORDER BY node_id
`)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, nodeIDs)

	// Check that the config for volume v1 on p2 was duplicated.
	volumeIDs, err := query.SelectIntegers(context.Background(), tx, `
SELECT id FROM storage_volumes WHERE storage_pool_id=2 AND name='v1' ORDER BY id
`)
	require.NoError(t, err)
	require.Equal(t, []int{2, 4}, volumeIDs)
	config1, err := query.SelectConfig(context.Background(), tx, "storage_volumes_config", "storage_volume_id=?", volumeIDs[0])
	require.NoError(t, err)
	config2, err := query.SelectConfig(context.Background(), tx, "storage_volumes_config", "storage_volume_id=?", volumeIDs[1])
	require.NoError(t, err)
	require.Equal(t, config1, config2)
}

func TestUpdateFromV6(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(7, func(db *sql.DB) {
		// Create two nodes.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now())
		require.NoError(t, err)
		_, err = db.Exec(
			"INSERT INTO nodes VALUES (2, 'n2', '', '5.6.7.8:666', 1, 32, ?, 0)",
			time.Now())
		require.NoError(t, err)

		// Create a pool p1 of type zfs.
		_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'zfs', '', 0)")
		require.NoError(t, err)

		// Create a pool p2 of type zfs.
		_, err = db.Exec("INSERT INTO storage_pools VALUES (2, 'p2', 'zfs', '', 0)")
		require.NoError(t, err)

		// Create a zfs.pool_name config for p1.
		_, err = db.Exec(`
INSERT INTO storage_pools_config(storage_pool_id, node_id, key, value)
  VALUES(1, NULL, 'zfs.pool_name', 'my-pool')
`)
		require.NoError(t, err)

		// Create a zfs.clone_copy config for p2.
		_, err = db.Exec(`
INSERT INTO storage_pools_config(storage_pool_id, node_id, key, value)
  VALUES(2, NULL, 'zfs.clone_copy', 'true')
`)
		require.NoError(t, err)
	})
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	// Check the zfs.pool_name config is now node-specific.
	for _, nodeID := range []int{1, 2} {
		config, err := query.SelectConfig(context.Background(),
			tx, "storage_pools_config", "storage_pool_id=1 AND node_id=?", nodeID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"zfs.pool_name": "my-pool"}, config)
	}

	// Check the zfs.clone_copy is still global
	config, err := query.SelectConfig(context.Background(),
		tx, "storage_pools_config", "storage_pool_id=2 AND node_id IS NULL")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"zfs.clone_copy": "true"}, config)
}

func TestUpdateFromV9(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(10, func(db *sql.DB) {
		// Create a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now())
		require.NoError(t, err)

		// Create an operation.
		_, err = db.Exec("INSERT INTO operations VALUES (1, 'op1', 1)")
		require.NoError(t, err)
	})
	require.NoError(t, err)

	// Check that a type column has been added and that existing rows get type 0.
	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	types, err := query.SelectIntegers(context.Background(), tx, `SELECT type FROM operations`)
	require.NoError(t, err)
	require.Equal(t, []int{0}, types)
}

func TestUpdateFromV11(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(12, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now())
		require.NoError(t, err)

		// Insert a container.
		_, err = db.Exec(`
INSERT INTO containers VALUES (1, 1, 'bionic', 1, 1, 0, ?, 0, ?, 'Bionic Beaver')
`, time.Now(), time.Now())
		require.NoError(t, err)

		// Insert an image.
		_, err = db.Exec(`
INSERT INTO images VALUES (1, 'abcd', 'img.tgz', 123, 0, 0, NULL, NULL, ?, 0, NULL, 0)
`, time.Now())
		require.NoError(t, err)

		// Insert an image alias.
		_, err = db.Exec(`
INSERT INTO images_aliases VALUES (1, 'my-img', 1, NULL)
`, time.Now())
		require.NoError(t, err)

		// Insert some profiles.
		_, err = db.Exec(`
INSERT INTO profiles VALUES (1, 'default', NULL);
INSERT INTO profiles VALUES(2, 'users', '');
INSERT INTO profiles_config VALUES(2, 2, 'boot.autostart', 'false');
INSERT INTO profiles_config VALUES(3, 2, 'limits.cpu.allowance', '50%');
INSERT INTO profiles_devices VALUES(1, 1, 'eth0', 1);
INSERT INTO profiles_devices VALUES(2, 1, 'root', 1);
INSERT INTO profiles_devices_config VALUES(1, 1, 'nictype', 'bridged');
INSERT INTO profiles_devices_config VALUES(2, 1, 'parent', 'incusbr0');
INSERT INTO profiles_devices_config VALUES(3, 2, 'path', '/');
INSERT INTO profiles_devices_config VALUES(4, 2, 'pool', 'default');
`, time.Now())
		require.NoError(t, err)
	})
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	// Check that a project_id column has been added to the various talbles
	// and that existing rows default to 1 (the ID of the default project).
	for _, table := range []string{"containers", "images", "images_aliases"} {
		count, err := query.Count(context.Background(), tx, table, "")
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		stmt := fmt.Sprintf("SELECT project_id FROM %s", table)
		ids, err := query.SelectIntegers(context.Background(), tx, stmt)
		require.NoError(t, err)
		assert.Equal(t, []int{1}, ids)
	}

	// Create a new project.
	_, err = tx.Exec(`
INSERT INTO projects VALUES (2, 'staging', 'Staging environment')`)
	require.NoError(t, err)

	// Check that it's possible to have two containers with the same name
	// as long as they are in different projects.
	_, err = tx.Exec(`
INSERT INTO containers VALUES (2, 1, 'xenial', 1, 1, 0, ?, 0, ?, 'Xenial Xerus', 1)
`, time.Now(), time.Now())
	require.NoError(t, err)

	_, err = tx.Exec(`
INSERT INTO containers VALUES (3, 1, 'xenial', 1, 1, 0, ?, 0, ?, 'Xenial Xerus', 2)
`, time.Now(), time.Now())
	require.NoError(t, err)

	// Check that it's not possible to have two containers with the same name
	// in the same project.

	_, err = tx.Exec(`
INSERT INTO containers VALUES (4, 1, 'xenial', 1, 1, 0, ?, 0, ?, 'Xenial Xerus', 1)
`, time.Now(), time.Now())
	assert.EqualError(t, err, "UNIQUE constraint failed: containers.project_id, containers.name")
}

func TestUpdateFromV14(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(15, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now())
		require.NoError(t, err)

		// Insert a container.
		_, err = db.Exec(`
INSERT INTO containers VALUES (1, 1, 'eoan', 1, 1, 0, ?, 0, ?, 'Eoan Ermine', 1, NULL)
`, time.Now(), time.Now())
		require.NoError(t, err)
	})

	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	// Check that the new instances table can be queried.
	count, err := query.Count(context.Background(), tx, "instances", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestUpdateFromV15(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(16, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now())
		require.NoError(t, err)

		// Insert an instance.
		_, err = db.Exec(`
INSERT INTO instances VALUES (1, 1, 'eoan', 2, 0, 0, ?, 0, ?, NULL, 1, ?)
`, time.Now(), time.Now(), time.Now())
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_config VALUES (1, 1, 'key', 'value2')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_devices VALUES (1, 1, 'dev', 0)")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_devices_config VALUES (1, 1, 'k', 'v')")
		require.NoError(t, err)

		// Insert an instance snapshot.
		expiry_date := time.Date(2019, 8, 14, 11, 9, 0, 0, time.UTC)
		_, err = db.Exec(`
INSERT INTO instances VALUES (2, 1, 'eoan/snap', 2, 1, 0, ?, 0, ?, 'Eoan Ermine Snapshot', 1, ?)
`, time.Now(), time.Now(), expiry_date)
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_config VALUES (2, 2, 'key', 'value1')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_devices VALUES (2, 2, 'dev', 0)")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_devices_config VALUES (2, 2, 'k', 'v')")
		require.NoError(t, err)
	})

	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	// Check that snapshots were migrated to the new tables.
	count, err := query.Count(context.Background(), tx, "instances", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_config", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_devices", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_devices_config", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_snapshots", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_snapshots_config", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_snapshots_devices", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_snapshots_devices_config", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	config, err := query.SelectConfig(context.Background(), tx, "instances_config", "id = 1")
	require.NoError(t, err)
	assert.Equal(t, config, map[string]string{"key": "value2"})

	config, err = query.SelectConfig(context.Background(), tx, "instances_snapshots_config", "id = 1")
	require.NoError(t, err)
	assert.Equal(t, config, map[string]string{"key": "value1"})

	config, err = query.SelectConfig(context.Background(), tx, "instances_devices_config", "id = 1")
	require.NoError(t, err)
	assert.Equal(t, config, map[string]string{"k": "v"})

	config, err = query.SelectConfig(context.Background(), tx, "instances_snapshots_devices_config", "id = 1")
	require.NoError(t, err)
	assert.Equal(t, config, map[string]string{"k": "v"})
}

func TestUpdateFromV19(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(20, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now())
		require.NoError(t, err)
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	expectedArch, err := osarch.ArchitectureGetLocalID()
	require.NoError(t, err)

	row := db.QueryRow("SELECT arch FROM nodes")
	arch := 0
	err = row.Scan(&arch)
	require.NoError(t, err)

	assert.Equal(t, expectedArch, arch)

	// Trying to create a row without specififying the architecture results
	// in an error.
	_, err = db.Exec(`
INSERT INTO nodes(id, name, description, address, schema, api_extensions, heartbeat, pending)
VALUES (2, 'n2', '', '2.2.3.4:666', 1, 32, ?, 0)`, time.Now())
	if err == nil {
		t.Fatal("expected insertion to fail")
	}

	sqliteErr, ok := err.(sqlite3.Error)
	require.True(t, ok)
	assert.Equal(t, sqliteErr.Code, sqlite3.ErrConstraint)
}

func TestUpdateFromV25(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(26, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0, 1)",
			time.Now())
		require.NoError(t, err)

		// Insert a pool
		_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'zfs', '', 0)")
		require.NoError(t, err)

		// Create a volume v1 on pool p1, associated with n1 and a config.
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (1, 'v1', 1, 1, 1, '', 0, 1)")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes_config VALUES (1, 1, 'k', 'v')")
		require.NoError(t, err)

		// Create a snapshot v1/snap0 with a config.
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (2, 'v1/snap0', 1, 1, 1, '', 1, 1)")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes_config VALUES (2, 2, 'k', 'v-old')")
		require.NoError(t, err)
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	// Check that regular volumes were kept.
	count, err := query.Count(context.Background(), tx, "storage_volumes", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	count, err = query.Count(context.Background(), tx, "storage_volumes_config", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Check that volume snapshots were migrated.
	count, err = query.Count(context.Background(), tx, "storage_volumes_snapshots", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	config, err := query.SelectConfig(context.Background(), tx, "storage_volumes_snapshots_config", "")
	require.NoError(t, err)
	assert.Len(t, config, 1)
	assert.Equal(t, config["k"], "v-old")
}

func TestUpdateFromV26_WithoutVolumes(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(27, func(db *sql.DB) {})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
}

func TestUpdateFromV26_WithVolumes(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(27, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0, 1)",
			time.Now())
		require.NoError(t, err)

		// Insert a pool
		_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'zfs', '', 0)")
		require.NoError(t, err)

		// Create a volume v1 on pool p1
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (1, 'v1', 1, 1, 1, '', 1)")
		require.NoError(t, err)

		// Create a snapshot snap0.
		_, err = db.Exec("INSERT INTO storage_volumes_snapshots VALUES (2, 1, 'snap0', '')")
		require.NoError(t, err)

		// Mess up the sqlite_sequence value.
		_, err = db.Exec("UPDATE sqlite_sequence SET seq = 1 WHERE name = 'storage_volumes'")
		require.NoError(t, err)
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()
	ids, err := query.SelectIntegers(context.Background(), tx, "SELECT seq FROM sqlite_sequence WHERE name = 'storage_volumes'")
	require.NoError(t, err)

	assert.Equal(t, ids[0], 2)
}

func TestUpdateFromV34(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(35, func(db *sql.DB) {
		// Insert two nodes.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0, 1, NULL)",
			time.Now())
		require.NoError(t, err)

		_, err = db.Exec(
			"INSERT INTO nodes VALUES (2, 'n2', '', '5.6.7.8:666', 1, 32, ?, 0, 1, NULL)",
			time.Now())
		require.NoError(t, err)

		// Insert a storage pool.
		_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'ceph', NULL, 0)")
		require.NoError(t, err)

		// Create two rows for the same volume on different nodes.
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (1, 'v1', 1, 1, 1, NULL, 1, 0)")
		require.NoError(t, err)

		_, err = db.Exec("INSERT INTO storage_volumes VALUES (2, 'v1', 1, 2, 1, NULL, 1, 0)")
		require.NoError(t, err)
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	// Only one volume is left and it's node ID is set to NULL.
	count, err := query.Count(context.Background(), tx, "storage_volumes", "")
	require.NoError(t, err)

	assert.Equal(t, count, 1)

	row := tx.QueryRow("SELECT id, node_id FROM storage_volumes")
	var id int
	var nodeID any
	require.NoError(t, row.Scan(&id, &nodeID))
	assert.Equal(t, id, 2)
	assert.Equal(t, nodeID, nil)
}
