//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/canonical/go-dqlite/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/incus/db"
	"github.com/lxc/incus/incus/db/cluster"
	"github.com/lxc/incus/incus/db/query"
	"github.com/lxc/incus/incus/project"
)

func TestLoadPreClusteringData(t *testing.T) {
	tx := newPreClusteringTx(t)

	dump, err := db.LoadPreClusteringData(context.Background(), tx)
	require.NoError(t, err)

	// config
	assert.Equal(t, []string{"id", "key", "value"}, dump.Schema["config"])
	assert.Len(t, dump.Data["config"], 3)
	assert.Equal(t, "1.2.3.4:666", dump.Data["config"][0][2])
	rows := []any{int64(1), "core.https_address", "1.2.3.4:666"}
	assert.Equal(t, rows, dump.Data["config"][0])
	rows = []any{int64(2), "core.trust_password", "sekret"}
	assert.Equal(t, rows, dump.Data["config"][1])
	rows = []any{int64(3), "maas.machine", "mymaas"}
	assert.Equal(t, rows, dump.Data["config"][2])

	// networks
	assert.Equal(t, []string{"id", "name", "description"}, dump.Schema["networks"])
	assert.Len(t, dump.Data["networks"], 1)
	rows = []any{int64(1), "lxcbr0", "LXD bridge"}
	assert.Equal(t, rows, dump.Data["networks"][0])
}

func TestImportPreClusteringData(t *testing.T) {
	tx := newPreClusteringTx(t)

	dump, err := db.LoadPreClusteringData(context.Background(), tx)
	require.NoError(t, err)

	dir, store, cleanup := db.NewTestDqliteServer(t)
	defer cleanup()

	dial := func(ctx context.Context, address string) (net.Conn, error) {
		return net.Dial("unix", address)
	}

	c, err := db.OpenCluster(context.Background(), "test.db", store, "1", dir, 5*time.Second, dump, driver.WithDialFunc(dial))
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	// certificates
	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		certs, err := cluster.GetCertificates(context.Background(), tx.Tx())
		require.NoError(t, err)
		assert.Len(t, certs, 1)
		cert := certs[0]
		assert.Equal(t, 1, cert.ID)
		assert.Equal(t, "abcd:efgh", cert.Fingerprint)
		assert.Equal(t, cluster.CertificateTypeClient, cert.Type)
		assert.Equal(t, "foo", cert.Name)
		assert.Equal(t, "FOO", cert.Certificate)
		return nil
	})
	require.NoError(t, err)

	// config
	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		config, err := tx.Config(ctx)
		require.NoError(t, err)
		values := map[string]string{"core.trust_password": "sekret"}
		assert.Equal(t, values, config)
		return nil
	})
	require.NoError(t, err)

	// networks
	networks, err := c.GetNetworks(project.Default)
	require.NoError(t, err)
	assert.Equal(t, []string{"lxcbr0"}, networks)
	id, network, _, err := c.GetNetworkInAnyState(project.Default, "lxcbr0")
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)
	assert.Equal(t, "true", network.Config["ipv4.nat"])
	assert.Equal(t, "Created", network.Status)
	assert.Equal(t, []string{"none"}, network.Locations)

	// storage
	pools, err := c.GetStoragePoolNames()
	require.NoError(t, err)
	assert.Equal(t, []string{"default"}, pools)
	id, pool, _, err := c.GetStoragePool("default")
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)
	assert.Equal(t, "/foo/bar", pool.Config["source"])
	assert.Equal(t, "123", pool.Config["size"])
	assert.Equal(t, "/foo/bar", pool.Config["volatile.initial_source"])
	assert.Equal(t, "mypool", pool.Config["zfs.pool_name"])
	assert.Equal(t, "true", pool.Config["zfs.clone_copy"])
	assert.Equal(t, "Created", pool.Status)
	assert.Equal(t, []string{"none"}, pool.Locations)

	var dbVolumes []*db.StorageVolume
	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		volumeType := db.StoragePoolVolumeTypeImage
		filters := []db.StorageVolumeFilter{{
			Type: &volumeType,
		}}

		dbVolumes, err = tx.GetStoragePoolVolumes(context.Background(), id, true, filters...)
		if err != nil {
			return fmt.Errorf("Failed loading storage volumes: %w", err)
		}

		return nil
	})

	require.NoError(t, err)
	assert.Len(t, dbVolumes, 1)
	assert.Equal(t, "/foo/bar", dbVolumes[0].Config["source"])

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// The zfs.clone_copy config got a NULL node_id, since it's cluster global.
		config, err := query.SelectConfig(ctx, tx.Tx(), "storage_pools_config", "node_id IS NULL")
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"zfs.clone_copy": "true"}, config)

		// The other config keys are node-specific.
		config, err = query.SelectConfig(ctx, tx.Tx(), "storage_pools_config", "node_id=?", 1)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"source": "/foo/bar", "size": "123", "volatile.initial_source": "/foo/bar", "zfs.pool_name": "mypool"}, config)

		// Storage volumes have now a node_id key set to 1 (the ID of
		// the default node).
		ids, err := query.SelectIntegers(ctx, tx.Tx(), "SELECT node_id FROM storage_volumes")
		require.NoError(t, err)
		assert.Equal(t, []int{1}, ids)

		return nil
	})
	require.NoError(t, err)

	// profiles
	profiles, err := c.GetProfileNames("default")
	require.NoError(t, err)
	assert.Equal(t, []string{"default", "users"}, profiles)
	_, profile, err := c.GetProfile("default", "default")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{}, profile.Config)
	assert.Equal(t,
		map[string]map[string]string{
			"root": {
				"path": "/",
				"pool": "default",
				"type": "nic"},
			"eth0": {
				"type":    "nic",
				"nictype": "bridged",
				"parent":  "lxdbr0"}},
		profile.Devices)
	_, profile, err = c.GetProfile("default", "users")
	require.NoError(t, err)
	assert.Equal(t,
		map[string]string{
			"boot.autostart":       "false",
			"limits.cpu.allowance": "50%"},
		profile.Config)
	assert.Equal(t, map[string]map[string]string{}, profile.Devices)
}

// Return a sql.Tx against a memory database populated with pre-clustering
// data.
func newPreClusteringTx(t *testing.T) *sql.Tx {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	stmts := []string{
		preClusteringNodeSchema,
		"INSERT INTO certificates VALUES (1, 'abcd:efgh', 1, 'foo', 'FOO')",
		"INSERT INTO config VALUES(1, 'core.https_address', '1.2.3.4:666')",
		"INSERT INTO config VALUES(2, 'core.trust_password', 'sekret')",
		"INSERT INTO config VALUES(3, 'maas.machine', 'mymaas')",
		"INSERT INTO profiles VALUES(1, 'default', 'Default Incus profile')",
		"INSERT INTO profiles VALUES(2, 'users', '')",
		"INSERT INTO profiles_config VALUES(2, 2, 'boot.autostart', 'false')",
		"INSERT INTO profiles_config VALUES(3, 2, 'limits.cpu.allowance', '50%')",
		"INSERT INTO profiles_devices VALUES(1, 1, 'eth0', 1)",
		"INSERT INTO profiles_devices VALUES(2, 1, 'root', 1)",
		"INSERT INTO profiles_devices_config VALUES(1, 1, 'nictype', 'bridged')",
		"INSERT INTO profiles_devices_config VALUES(2, 1, 'parent', 'lxdbr0')",
		"INSERT INTO profiles_devices_config VALUES(3, 2, 'path', '/')",
		"INSERT INTO profiles_devices_config VALUES(4, 2, 'pool', 'default')",
		"INSERT INTO images VALUES(1, 'abc', 'x.gz', 16, 0, 1, 0, 0, strftime('%d-%m-%Y', 'now'), 0, 0, 0)",
		"INSERT INTO networks VALUES(1, 'lxcbr0', 'LXD bridge')",
		"INSERT INTO networks_config VALUES(1, 1, 'ipv4.nat', 'true')",
		"INSERT INTO storage_pools VALUES (1, 'default', 'dir', '')",
		"INSERT INTO storage_pools_config VALUES(1, 1, 'source', '/foo/bar')",
		"INSERT INTO storage_pools_config VALUES(2, 1, 'size', '123')",
		"INSERT INTO storage_pools_config VALUES(3, 1, 'volatile.initial_source', '/foo/bar')",
		"INSERT INTO storage_pools_config VALUES(4, 1, 'zfs.pool_name', 'mypool')",
		"INSERT INTO storage_pools_config VALUES(5, 1, 'zfs.clone_copy', 'true')",
		"INSERT INTO storage_volumes VALUES (1, 'dev', 1, 1, '')",
		"INSERT INTO storage_volumes_config VALUES(1, 1, 'source', '/foo/bar')",
	}

	for _, stmt := range stmts {
		_, err := tx.Exec(stmt)
		require.NoError(t, err)
	}

	return tx
}

const preClusteringNodeSchema = `
CREATE TABLE schema (
    id         INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version    INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);
CREATE TABLE certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    certificate TEXT NOT NULL,
    UNIQUE (fingerprint)
);
CREATE TABLE config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (key)
);
CREATE TABLE "containers" (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    ephemeral INTEGER NOT NULL DEFAULT 0,
    creation_date DATETIME NOT NULL DEFAULT 0,
    stateful INTEGER NOT NULL DEFAULT 0,
    last_use_date DATETIME,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE containers_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, key)
);
CREATE TABLE containers_devices (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, name)
);
CREATE TABLE containers_devices_config (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_device_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_device_id) REFERENCES containers_devices (id) ON DELETE CASCADE,
    UNIQUE (container_device_id, key)
);
CREATE TABLE containers_profiles (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    apply_order INTEGER NOT NULL default 0,
    UNIQUE (container_id, profile_id),
    FOREIGN KEY (container_id) REFERENCES containers(id) ON DELETE CASCADE,
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
CREATE TABLE images (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    size INTEGER NOT NULL,
    public INTEGER NOT NULL DEFAULT 0,
    architecture INTEGER NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    upload_date DATETIME NOT NULL,
    cached INTEGER NOT NULL DEFAULT 0,
    last_use_date DATETIME,
    auto_update INTEGER NOT NULL DEFAULT 0,
    UNIQUE (fingerprint)
);
CREATE TABLE "images_aliases" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    UNIQUE (name)
);
CREATE TABLE images_properties (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
);
CREATE TABLE images_source (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    server TEXT NOT NULL,
    protocol INTEGER NOT NULL,
    certificate TEXT NOT NULL,
    alias VARCHAR(255) NOT NULL,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
);
CREATE TABLE networks (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE networks_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (network_id, key),
    FOREIGN KEY (network_id) REFERENCES networks (id) ON DELETE CASCADE
);
CREATE TABLE patches (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    applied_at DATETIME NOT NULL,
    UNIQUE (name)
);
CREATE TABLE profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE profiles_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value VARCHAR(255),
    UNIQUE (profile_id, key),
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
CREATE TABLE profiles_devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (profile_id, name),
    FOREIGN KEY (profile_id) REFERENCES profiles (id) ON DELETE CASCADE
);
CREATE TABLE profiles_devices_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_device_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (profile_device_id, key),
    FOREIGN KEY (profile_device_id) REFERENCES profiles_devices (id) ON DELETE CASCADE
);
CREATE TABLE raft_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    address TEXT NOT NULL,
    UNIQUE (address)
);
CREATE TABLE storage_pools (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    driver VARCHAR(255) NOT NULL,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE storage_pools_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (storage_pool_id, key),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE
);
CREATE TABLE storage_volumes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    description TEXT,
    UNIQUE (storage_pool_id, name, type),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE
);
CREATE TABLE storage_volumes_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (storage_volume_id, key),
    FOREIGN KEY (storage_volume_id) REFERENCES storage_volumes (id) ON DELETE CASCADE
);

INSERT INTO schema (version, updated_at) VALUES (37, strftime("%s"))
`
