package query_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db/query"
)

func TestDump(t *testing.T) {
	tx := newTxForDump(t, "local")
	dump, err := query.Dump(context.Background(), tx, false)
	require.NoError(t, err)
	assert.Equal(t, `PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE schema (
    id         INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version    INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);
INSERT INTO schema VALUES(1,37,'2018-04-17 06:26:06+00:00');
CREATE TABLE config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (key)
);
CREATE TABLE patches (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    applied_at DATETIME NOT NULL,
    UNIQUE (name)
);
INSERT INTO patches VALUES(1,'invalid_profile_names','2018-04-17 06:26:06+00:00');
INSERT INTO patches VALUES(2,'leftover_profile_config','2018-04-17 06:26:06+00:00');
CREATE TABLE raft_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    address TEXT NOT NULL,
    UNIQUE (address)
);
CREATE INDEX config_key_idx ON config (key);
DELETE FROM sqlite_sequence;
INSERT INTO sqlite_sequence VALUES('schema',1);
INSERT INTO sqlite_sequence VALUES('patches',2);
COMMIT;
`, dump)
}

func TestDumpTablePatches(t *testing.T) {
	tx := newTxForDump(t, "local")

	dump, _, err := query.GetEntitiesSchemas(context.Background(), tx)
	require.NoError(t, err)
	assert.Equal(t, `CREATE TABLE patches (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    applied_at DATETIME NOT NULL,
    UNIQUE (name)
);`, dump["patches"][1])
	data, err := query.GetTableData(context.Background(), tx, "patches")
	require.NoError(t, err)
	assert.ElementsMatch(t, data, []string{
		"INSERT INTO patches VALUES(1,'invalid_profile_names','2018-04-17 06:26:06+00:00');",
		"INSERT INTO patches VALUES(2,'leftover_profile_config','2018-04-17 06:26:06+00:00');",
	})
}

func TestDumpTableConfig(t *testing.T) {
	tx := newTxForDump(t, "local")

	dump, _, err := query.GetEntitiesSchemas(context.Background(), tx)
	require.NoError(t, err)
	assert.Equal(t, `CREATE TABLE config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (key)
);`, dump["config"][1])
}

func TestDumpTableStoragePoolsConfig(t *testing.T) {
	tx := newTxForDump(t, "global")
	dump, _, err := query.GetEntitiesSchemas(context.Background(), tx)
	require.NoError(t, err)
	assert.Equal(t, `CREATE TABLE storage_pools_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (storage_pool_id, node_id, key),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);`, dump["storage_pools_config"][1])
	data, err := query.GetTableData(context.Background(), tx, "storage_pools_config")
	require.NoError(t, err)
	assert.Equal(t, data, []string{"INSERT INTO storage_pools_config VALUES(1,1,NULL,'k','v');"})
}

// Return a new transaction against an in-memory SQLite database populated with
// a few tables and data, according to the given schema.
func newTxForDump(t *testing.T, schema string) *sql.Tx {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)

	_, err = db.Exec(`CREATE TABLE schema (
    id         INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version    INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);`)
	require.NoError(t, err)

	_, err = db.Exec(schemas[schema])
	require.NoError(t, err)

	for _, stmt := range data[schema] {
		_, err = db.Exec(stmt)
		require.NoError(t, err)
	}

	tx, err := db.Begin()
	require.NoError(t, err)

	return tx
}

var schemas = map[string]string{
	"local": `
CREATE TABLE config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    UNIQUE (key)
);
CREATE TABLE patches (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    applied_at DATETIME NOT NULL,
    UNIQUE (name)
);
CREATE TABLE raft_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    address TEXT NOT NULL,
    UNIQUE (address)
);
CREATE INDEX config_key_idx ON config (key);
`,
	"global": `
CREATE TABLE certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint TEXT NOT NULL,
    type INTEGER NOT NULL,
    name TEXT NOT NULL,
    certificate TEXT NOT NULL,
    UNIQUE (fingerprint)
);
CREATE TABLE config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (key)
);
CREATE TABLE containers (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    node_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    ephemeral INTEGER NOT NULL DEFAULT 0,
    creation_date DATETIME NOT NULL DEFAULT 0,
    stateful INTEGER NOT NULL DEFAULT 0,
    last_use_date DATETIME,
    description TEXT,
    UNIQUE (name),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE containers_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, key)
);
CREATE TABLE containers_devices (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, name)
);
CREATE TABLE containers_devices_config (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
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
    fingerprint TEXT NOT NULL,
    filename TEXT NOT NULL,
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
CREATE TABLE images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    UNIQUE (name)
);
CREATE TABLE images_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (image_id, node_id),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE images_properties (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
);
CREATE TABLE images_source (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    server TEXT NOT NULL,
    protocol INTEGER NOT NULL,
    certificate TEXT NOT NULL,
    alias TEXT NOT NULL,
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE
);
CREATE TABLE networks (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    state INTEGER NOT NULL DEFAULT 0,
    UNIQUE (name)
);
CREATE TABLE networks_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (network_id, node_id, key),
    FOREIGN KEY (network_id) REFERENCES networks (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE networks_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (network_id, node_id),
    FOREIGN KEY (network_id) REFERENCES networks (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE nodes (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    address TEXT NOT NULL,
    schema INTEGER NOT NULL,
    api_extensions INTEGER NOT NULL,
    heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP,
    pending INTEGER NOT NULL DEFAULT 0,
    UNIQUE (name),
    UNIQUE (address)
);
CREATE TABLE operations (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    uuid TEXT NOT NULL,
    node_id TEXT NOT NULL,
    UNIQUE (uuid),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    UNIQUE (name)
);
CREATE TABLE profiles_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (profile_id, key),
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
CREATE TABLE profiles_devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (profile_id, name),
    FOREIGN KEY (profile_id) REFERENCES profiles (id) ON DELETE CASCADE
);
CREATE TABLE profiles_devices_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (profile_device_id, key),
    FOREIGN KEY (profile_device_id) REFERENCES profiles_devices (id) ON DELETE CASCADE
);
CREATE TABLE storage_pools (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    driver TEXT NOT NULL,
    description TEXT,
    state INTEGER NOT NULL DEFAULT 0,
    UNIQUE (name)
);
CREATE TABLE storage_pools_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (storage_pool_id, node_id, key),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE storage_pools_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (storage_pool_id, node_id),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE storage_volumes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    description TEXT,
    UNIQUE (storage_pool_id, node_id, name, type),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE storage_volumes_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (storage_volume_id, key),
    FOREIGN KEY (storage_volume_id) REFERENCES storage_volumes (id) ON DELETE CASCADE
);
`,
}

var data = map[string][]string{
	"local": {
		"INSERT INTO schema VALUES(1,37,1523946366)",
		"INSERT INTO patches VALUES(1,'invalid_profile_names',1523946366)",
		"INSERT INTO patches VALUES(2,'leftover_profile_config',1523946366)",
	},
	"global": {
		"INSERT INTO storage_pools VALUES(1,'p1','dir','',0)",
		"INSERT INTO storage_pools_config VALUES(1,1,NULL,'k','v')",
	},
}
