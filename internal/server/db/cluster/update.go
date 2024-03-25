package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/server/db/query"
	"github.com/lxc/incus/internal/server/db/schema"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/osarch"
)

// Schema for the cluster database.
func Schema() *schema.Schema {
	schema := schema.NewFromMap(updates)
	schema.Fresh(freshSchema)
	return schema
}

// FreshSchema returns the fresh schema definition of the global database.
func FreshSchema() string {
	return freshSchema
}

// SchemaDotGo refreshes the schema.go file in this package, using the updates
// defined here.
func SchemaDotGo() error {
	return schema.DotGo(updates, "schema")
}

// SchemaVersion is the current version of the cluster database schema.
var SchemaVersion = len(updates)

var updates = map[int]schema.Update{
	1:  updateFromV0,
	2:  updateFromV1,
	3:  updateFromV2,
	4:  updateFromV3,
	5:  updateFromV4,
	6:  updateFromV5,
	7:  updateFromV6,
	8:  updateFromV7,
	9:  updateFromV8,
	10: updateFromV9,
	11: updateFromV10,
	12: updateFromV11,
	13: updateFromV12,
	14: updateFromV13,
	15: updateFromV14,
	16: updateFromV15,
	17: updateFromV16,
	18: updateFromV17,
	19: updateFromV18,
	20: updateFromV19,
	21: updateFromV20,
	22: updateFromV21,
	23: updateFromV22,
	24: updateFromV23,
	25: updateFromV24,
	26: updateFromV25,
	27: updateFromV26,
	28: updateFromV27,
	29: updateFromV28,
	30: updateFromV29,
	31: updateFromV30,
	32: updateFromV31,
	33: updateFromV32,
	34: updateFromV33,
	35: updateFromV34,
	36: updateFromV35,
	37: updateFromV36,
	38: updateFromV37,
	39: updateFromV38,
	40: updateFromV39,
	41: updateFromV40,
	42: updateFromV41,
	43: updateFromV42,
	44: updateFromV43,
	45: updateFromV44,
	46: updateFromV45,
	47: updateFromV46,
	48: updateFromV47,
	49: updateFromV48,
	50: updateFromV49,
	51: updateFromV50,
	52: updateFromV51,
	53: updateFromV52,
	54: updateFromV53,
	55: updateFromV54,
	56: updateFromV55,
	57: updateFromV56,
	58: updateFromV57,
	59: updateFromV58,
	60: updateFromV59,
	61: updateFromV60,
	62: updateFromV61,
	63: updateFromV62,
	64: updateFromV63,
	65: updateFromV64,
	66: updateFromV65,
	67: updateFromV66,
	68: updateFromV67,
	69: updateFromV68,
	70: updateFromV69,
	71: updateFromV70,
	72: updateFromV71,
	73: updateFromV72,
}

// updateFromV72 removes the openfga.store.model_id server config key.
func updateFromV72(ctx context.Context, tx *sql.Tx) error {
	q := `DELETE FROM config WHERE key='openfga.store.model_id';`
	_, err := tx.Exec(q)
	if err != nil {
		return fmt.Errorf("Failed adding network integration support: %w", err)
	}

	return nil
}

// updateFromV71 adds network integration support.
func updateFromV71(ctx context.Context, tx *sql.Tx) error {
	q := `
CREATE TABLE networks_integrations (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	type INTEGER NOT NULL,
	UNIQUE (name)
);

CREATE TABLE networks_integrations_config (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_integration_id INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	UNIQUE (network_integration_id, key),
	FOREIGN KEY (network_integration_id) REFERENCES networks_integrations (id) ON DELETE CASCADE
);

ALTER TABLE networks_peers ADD COLUMN type INTEGER NOT NULL DEFAULT 0;
ALTER TABLE networks_peers ADD COLUMN target_network_integration_id INTEGER DEFAULT NULL REFERENCES networks_integrations (id) ON DELETE CASCADE;
CREATE UNIQUE INDEX networks_peers_unique_network_id_target_network_integration_id ON "networks_peers" (network_id, target_network_integration_id);
`
	_, err := tx.Exec(q)
	if err != nil {
		return fmt.Errorf("Failed adding network integration support: %w", err)
	}

	return nil
}

func updateFromV70(ctx context.Context, tx *sql.Tx) error {
	q := `
CREATE TABLE "storage_buckets_backups" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_bucket_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    FOREIGN KEY (storage_bucket_id) REFERENCES "storage_buckets" (id) ON DELETE CASCADE,
    UNIQUE (storage_bucket_id, name)
);
`
	_, err := tx.Exec(q)
	if err != nil {
		return fmt.Errorf("Failed adding storage bucket backup table: %w", err)
	}

	return nil
}

// updateFromV69 adds description column to certificate.
func updateFromV69(ctx context.Context, tx *sql.Tx) error {
	q := `
	ALTER TABLE certificates ADD COLUMN description TEXT NOT NULL DEFAULT "";
	`
	_, err := tx.Exec(q)
	if err != nil {
		return fmt.Errorf("Failed adding description column to certificate: %w", err)
	}

	return nil
}

// updateFromV68 fixes unique index for record name to make it zone specific.
func updateFromV68(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE networks_zones_records_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_zone_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	entries TEXT NOT NULL,
	UNIQUE (network_zone_id, name),
	FOREIGN KEY (network_zone_id) REFERENCES networks_zones (id) ON DELETE CASCADE
);

CREATE TABLE networks_zones_records_config_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_zone_record_id INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	UNIQUE (network_zone_record_id, key),
	FOREIGN KEY (network_zone_record_id) REFERENCES networks_zones_records_new (id) ON DELETE CASCADE
);

	INSERT INTO "networks_zones_records_new" SELECT * FROM "networks_zones_records";
	INSERT INTO "networks_zones_records_config_new" SELECT * FROM "networks_zones_records_config";

	DROP TABLE "networks_zones_records";
	ALTER TABLE "networks_zones_records_new" RENAME TO "networks_zones_records";

	DROP TABLE "networks_zones_records_config";
	ALTER TABLE "networks_zones_records_config_new" RENAME TO "networks_zones_records_config";
`)
	if err != nil {
		return fmt.Errorf("Failed altering network_zones_records schema: %w", err)
	}

	return nil
}

// updateFromV67 adds features.networks.zones=true to any project that has features.networks=true.
func updateFromV67(ctx context.Context, tx *sql.Tx) error {
	// Find projects that have features.networks=true.
	rows, err := tx.QueryContext(ctx, `
		SELECT
			projects.id
		FROM projects
		JOIN
			projects_config ON projects_config.project_id = projects.id
			AND projects_config.key = "features.networks"
			AND projects_config.value = "true"
	`)
	if err != nil {
		return fmt.Errorf("Failed finding projects with features.networks=true: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var projectIDs []int64
	for rows.Next() {
		var projectID int64

		err := rows.Scan(&projectID)
		if err != nil {
			return fmt.Errorf("Failed scanning project ID row: %w", err)
		}

		projectIDs = append(projectIDs, projectID)
	}

	_ = rows.Close()

	// Add features.networks.zones=true to any project that has features.networks=true.
	for _, projectID := range projectIDs {
		_, err = tx.Exec(`INSERT OR REPLACE INTO projects_config (project_id,key,value) VALUES(?,?,?);`, projectID, "features.networks.zones", "true")
		if err != nil {
			return fmt.Errorf("Failed adding features.networks.zones=true to project ID %q: %w", projectID, err)
		}

		logger.Info("Added features.networks.zones=true on project with features.networks=true", logger.Ctx{"projectID": projectID})
	}

	return nil
}

// updateFromV66 adds creation_date column to storage_volumes and storage_volumes_snapshots tables.
func updateFromV66(ctx context.Context, tx *sql.Tx) error {
	q := `
ALTER TABLE storage_volumes ADD COLUMN creation_date DATETIME NOT NULL DEFAULT "0001-01-01T00:00:00Z";
ALTER TABLE storage_volumes_snapshots ADD COLUMN creation_date DATETIME NOT NULL DEFAULT "0001-01-01T00:00:00Z";
DROP VIEW storage_volumes_all;
CREATE VIEW storage_volumes_all (
         id,
         name,
         storage_pool_id,
         node_id,
         type,
         description,
         project_id,
         content_type,
         creation_date) AS
  SELECT id,
         name,
         storage_pool_id,
         node_id,
         type,
         description,
         project_id,
         content_type,
         creation_date
    FROM storage_volumes UNION
  SELECT storage_volumes_snapshots.id,
         printf('%s/%s', storage_volumes.name, storage_volumes_snapshots.name),
         storage_volumes.storage_pool_id,
         storage_volumes.node_id,
         storage_volumes.type,
         storage_volumes_snapshots.description,
         storage_volumes.project_id,
         storage_volumes.content_type,
         storage_volumes_snapshots.creation_date
    FROM storage_volumes
    JOIN storage_volumes_snapshots ON storage_volumes.id = storage_volumes_snapshots.storage_volume_id;
`
	_, err := tx.Exec(q)
	if err != nil {
		return fmt.Errorf("Failed adding creation_date column to storage volumes: %w", err)
	}

	return nil
}

// updateFromV65 fixes typo in cephobject.radosgw.endpoint* settings.
func updateFromV65(ctx context.Context, tx *sql.Tx) error {
	q := `
	UPDATE storage_pools_config
	SET key = REPLACE(key, "cephobject.radosgsw.endpoint", "cephobject.radosgw.endpoint")
	WHERE key IN ("cephobject.radosgsw.endpoint", "cephobject.radosgsw.endpoint_cert_file")
	`
	_, err := tx.Exec(q)
	if err != nil {
		return fmt.Errorf("Failed replacing storage pool config cephobject.radosgsw.endpoint* with cephobject.radosgw.endpoint*: %w", err)
	}

	return nil
}

// updatefromV64 updates nodes_cluster_groups to include an ID field so that it works well with incus-generate.
func updateFromV64(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE "nodes_cluster_groups_new" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    node_id INTEGER NOT NULL,
    group_id INTEGER NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (group_id) REFERENCES cluster_groups (id) ON DELETE CASCADE,
    UNIQUE (node_id, group_id)
);

INSERT INTO nodes_cluster_groups_new (node_id, group_id)
    SELECT node_id, group_id FROM nodes_cluster_groups;

DROP TABLE nodes_cluster_groups;

ALTER TABLE nodes_cluster_groups_new RENAME TO nodes_cluster_groups;
`)
	if err != nil {
		return fmt.Errorf("Failed altering nodes_cluster_groups table: %w", err)
	}

	return nil
}

// updateFromV63 creates the storage buckets tables and adds features.storage.buckets=true to all projects that
// have features.storage.volumes=true.
func updateFromV63(ctx context.Context, tx *sql.Tx) error {
	// Find all projects that have features.storage.volumes=true and add features.storage.buckets=true.
	rows, err := tx.QueryContext(ctx, `SELECT project_id FROM projects_config WHERE key = "features.storage.volumes" AND value = "true"`)
	if err != nil {
		return fmt.Errorf("Failed getting projects with features.storage.volumes=true: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var projectIDs []int64

	for rows.Next() {
		var projectID int64
		err = rows.Scan(&projectID)
		if err != nil {
			return fmt.Errorf("Failed scanning project ID row: %w", err)
		}

		projectIDs = append(projectIDs, projectID)
	}

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("Got a row error getting projects with features.storage.volumes=true: %w", err)
	}

	for _, projectID := range projectIDs {
		_, err = tx.Exec(`INSERT OR REPLACE INTO projects_config (project_id,key,value) VALUES(?,?,?);`, projectID, "features.storage.buckets", "true")
		if err != nil {
			return fmt.Errorf("Failed adding features.storage.buckets=true to projects: %w", err)
		}
	}

	// Create storage buckets tables.
	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS "storage_buckets" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	name TEXT NOT NULL,
	storage_pool_id INTEGER NOT NULL,
	node_id INTEGER,
	description TEXT NOT NULL,
	project_id INTEGER NOT NULL,
	UNIQUE (node_id, name),
	FOREIGN KEY (storage_pool_id) REFERENCES "storage_pools" (id) ON DELETE CASCADE,
	FOREIGN KEY (node_id) REFERENCES "nodes" (id) ON DELETE CASCADE,
	FOREIGN KEY (project_id) REFERENCES "projects" (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX storage_buckets_unique_storage_pool_id_node_id_name ON "storage_buckets" (storage_pool_id, IFNULL(node_id, -1), name);

CREATE TABLE "storage_buckets_config" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	storage_bucket_id INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	UNIQUE (storage_bucket_id, key),
	FOREIGN KEY (storage_bucket_id) REFERENCES "storage_buckets" (id) ON DELETE CASCADE
);

CREATE TABLE "storage_buckets_keys" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	storage_bucket_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	access_key TEXT NOT NULL,
	secret_key TEXT NOT NULL,
	role TEXT NOT NULL,
	UNIQUE (storage_bucket_id, name),
	FOREIGN KEY (storage_bucket_id) REFERENCES "storage_buckets" (id) ON DELETE CASCADE
);
`)
	if err != nil {
		return fmt.Errorf("Failed adding storage bucket tables: %w", err)
	}

	return nil
}

// updateFromV62 adds unique index to storage_volumes that prevents duplicate volumes when using remote storage
// pool where the node_id column is NULL.
// Also ensures that the default project has features.networks set to true.
func updateFromV62(ctx context.Context, tx *sql.Tx) error {
	// Find the default project ID, and what it has features.networks config key set to (if at all).
	rows := tx.QueryRowContext(ctx, `
		SELECT
			projects.id,
			IFNULL(projects_config.key, "") as key,
			IFNULL(projects_config.value, "") as value
		FROM projects
		LEFT JOIN
			projects_config ON projects_config.project_id = projects.id
			AND projects_config.key = "features.networks"
		WHERE projects.name = "default"
	`)

	var defaultProjectID int64
	var featureKey, featureValue string

	err := rows.Scan(&defaultProjectID, &featureKey, &featureValue)
	if err != nil {
		return fmt.Errorf("Failed scanning default project row: %w", err)
	}

	// If the features.networks key is missing or not set to true, insert/replace the correct row.
	if featureKey == "" || featureValue != "true" {
		_, err = tx.Exec(`INSERT OR REPLACE INTO projects_config (project_id,key,value) VALUES(?,?,?);`, defaultProjectID, "features.networks", "true")
		if err != nil {
			return fmt.Errorf("Failed adding features.networks=true to default project: %w", err)
		}
	}

	// Create unique index on storage_volumes that protects against duplicate volumes when using remote
	// storage pool where the node_id field is NULL (which the current unique index doesn't protect against).
	_, err = tx.Exec(`CREATE UNIQUE INDEX storage_volumes_unique_storage_pool_id_node_id_project_id_name_type ON "storage_volumes" (storage_pool_id, IFNULL(node_id, -1), project_id, name, type);`)
	if err != nil {
		return fmt.Errorf("Failed adding storage volumes unique index: %w", err)
	}

	return nil
}

// updateFromV61 converts config value fields to NOT NULL and config key fields to TEXT (from VARCHAR).
func updateFromV61(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE "instances_config_new" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    instance_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    FOREIGN KEY (instance_id) REFERENCES "instances" (id) ON DELETE CASCADE,
    UNIQUE (instance_id, key)
);
INSERT INTO "instances_config_new" SELECT * FROM "instances_config";
DROP TABLE "instances_config";
ALTER TABLE "instances_config_new" RENAME TO "instances_config";
CREATE TABLE "instances_devices_config_new" (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    instance_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    FOREIGN KEY (instance_device_id) REFERENCES "instances_devices" (id) ON DELETE CASCADE,
    UNIQUE (instance_device_id, key)
);
INSERT INTO "instances_devices_config_new" SELECT * FROM "instances_devices_config";
DROP TABLE "instances_devices_config";
ALTER TABLE "instances_devices_config_new" RENAME TO "instances_devices_config";
CREATE TABLE "instances_snapshots_config_new" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    instance_snapshot_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    FOREIGN KEY (instance_snapshot_id) REFERENCES "instances_snapshots" (id) ON DELETE CASCADE,
    UNIQUE (instance_snapshot_id, key)
);
INSERT INTO "instances_snapshots_config_new" SELECT * FROM "instances_snapshots_config";
DROP TABLE "instances_snapshots_config";
ALTER TABLE "instances_snapshots_config_new" RENAME TO "instances_snapshots_config";
CREATE TABLE "instances_snapshots_devices_config_new" (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    instance_snapshot_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    FOREIGN KEY (instance_snapshot_device_id) REFERENCES "instances_snapshots_devices" (id) ON DELETE CASCADE,
    UNIQUE (instance_snapshot_device_id, key)
);
INSERT INTO "instances_snapshots_devices_config_new" SELECT * FROM "instances_snapshots_devices_config";
DROP TABLE "instances_snapshots_devices_config";
ALTER TABLE "instances_snapshots_devices_config_new" RENAME TO "instances_snapshots_devices_config";
CREATE TABLE "networks_acls_config_new" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_acl_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    UNIQUE (network_acl_id, key),
    FOREIGN KEY (network_acl_id) REFERENCES "networks_acls" (id) ON DELETE CASCADE
);
INSERT INTO "networks_acls_config_new" SELECT * FROM "networks_acls_config";
DROP TABLE "networks_acls_config";
ALTER TABLE "networks_acls_config_new" RENAME TO "networks_acls_config";
CREATE TABLE "networks_config_new" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    UNIQUE (network_id, node_id, key),
    FOREIGN KEY (network_id) REFERENCES "networks" (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES "nodes" (id) ON DELETE CASCADE
);
INSERT INTO "networks_config_new" SELECT * FROM "networks_config";
DROP TABLE "networks_config";
ALTER TABLE "networks_config_new" RENAME TO "networks_config";
CREATE UNIQUE INDEX networks_unique_network_id_node_id_key ON "networks_config" (network_id, IFNULL(node_id, -1), key);
CREATE TABLE "networks_forwards_config_new" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_forward_id INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	UNIQUE (network_forward_id, key),
	FOREIGN KEY (network_forward_id) REFERENCES "networks_forwards" (id) ON DELETE CASCADE
);
INSERT INTO "networks_forwards_config_new" SELECT * FROM "networks_forwards_config";
DROP TABLE "networks_forwards_config";
ALTER TABLE "networks_forwards_config_new" RENAME TO "networks_forwards_config";
CREATE TABLE "networks_peers_config_new" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_peer_id INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	UNIQUE (network_peer_id, key),
	FOREIGN KEY (network_peer_id) REFERENCES "networks_peers" (id) ON DELETE CASCADE
);
INSERT INTO "networks_peers_config_new" SELECT * FROM "networks_peers_config";
DROP TABLE "networks_peers_config";
ALTER TABLE "networks_peers_config_new" RENAME TO "networks_peers_config";
CREATE TABLE "networks_zones_config_new" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_zone_id INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	UNIQUE (network_zone_id, key),
	FOREIGN KEY (network_zone_id) REFERENCES "networks_zones" (id) ON DELETE CASCADE
);
INSERT INTO "networks_zones_config_new" SELECT * FROM "networks_zones_config";
DROP TABLE "networks_zones_config";
ALTER TABLE "networks_zones_config_new" RENAME TO "networks_zones_config";
CREATE TABLE networks_zones_records_config_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_zone_record_id INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	UNIQUE (network_zone_record_id, key),
	FOREIGN KEY (network_zone_record_id) REFERENCES networks_zones_records (id) ON DELETE CASCADE
);
INSERT INTO "networks_zones_records_config_new" SELECT * FROM "networks_zones_records_config";
DROP TABLE "networks_zones_records_config";
ALTER TABLE "networks_zones_records_config_new" RENAME TO "networks_zones_records_config";
CREATE TABLE "nodes_config_new" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	node_id INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	FOREIGN KEY (node_id) REFERENCES "nodes" (id) ON DELETE CASCADE,
	UNIQUE (node_id, key)
);
INSERT INTO "nodes_config_new" SELECT * FROM "nodes_config";
DROP TABLE "nodes_config";
ALTER TABLE "nodes_config_new" RENAME TO "nodes_config";
CREATE TABLE "profiles_config_new" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    UNIQUE (profile_id, key),
    FOREIGN KEY (profile_id) REFERENCES "profiles"(id) ON DELETE CASCADE
);
INSERT INTO "profiles_config_new" SELECT * FROM "profiles_config";
DROP TABLE "profiles_config";
ALTER TABLE "profiles_config_new" RENAME TO "profiles_config";
CREATE TABLE "profiles_devices_config_new" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    UNIQUE (profile_device_id, key),
    FOREIGN KEY (profile_device_id) REFERENCES "profiles_devices" (id) ON DELETE CASCADE
);
INSERT INTO "profiles_devices_config_new" SELECT * FROM "profiles_devices_config";
DROP TABLE "profiles_devices_config";
ALTER TABLE "profiles_devices_config_new" RENAME TO "profiles_devices_config";
CREATE TABLE "projects_config_new" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    project_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES "projects" (id) ON DELETE CASCADE,
    UNIQUE (project_id, key)
);
INSERT INTO "projects_config_new" SELECT * FROM "projects_config";
DROP TABLE "projects_config";
ALTER TABLE "projects_config_new" RENAME TO "projects_config";
CREATE TABLE "storage_pools_config_new" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    UNIQUE (storage_pool_id, node_id, key),
    FOREIGN KEY (storage_pool_id) REFERENCES "storage_pools" (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES "nodes" (id) ON DELETE CASCADE
);
INSERT INTO "storage_pools_config_new" SELECT * FROM "storage_pools_config";
DROP TABLE "storage_pools_config";
ALTER TABLE "storage_pools_config_new" RENAME TO "storage_pools_config";
CREATE UNIQUE INDEX storage_pools_unique_storage_pool_id_node_id_key ON storage_pools_config (storage_pool_id, IFNULL(node_id, -1), key);
CREATE TABLE "storage_volumes_config_new" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    UNIQUE (storage_volume_id, key),
    FOREIGN KEY (storage_volume_id) REFERENCES "storage_volumes" (id) ON DELETE CASCADE
);
INSERT INTO "storage_volumes_config_new" SELECT * FROM "storage_volumes_config";
DROP TABLE "storage_volumes_config";
ALTER TABLE "storage_volumes_config_new" RENAME TO "storage_volumes_config";
CREATE TABLE "storage_volumes_snapshots_config_new" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_snapshot_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    FOREIGN KEY (storage_volume_snapshot_id) REFERENCES "storage_volumes_snapshots" (id) ON DELETE CASCADE,
    UNIQUE (storage_volume_snapshot_id, key)
);
INSERT INTO "storage_volumes_snapshots_config_new" SELECT * FROM "storage_volumes_snapshots_config";
DROP TABLE "storage_volumes_snapshots_config";
ALTER TABLE "storage_volumes_snapshots_config_new" RENAME TO "storage_volumes_snapshots_config";
`)
	if err != nil {
		return fmt.Errorf("Failed altering config tables schema: %w", err)
	}

	return nil
}

// updateFromV60 creates the networks_load_balancers and networks_load_balancers_config tables.
func updateFromV60(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE "networks_load_balancers" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_id INTEGER NOT NULL,
	node_id INTEGER,
	listen_address TEXT NOT NULL,
	description TEXT NOT NULL,
	backends TEXT NOT NULL,
	ports TEXT NOT NULL,
	UNIQUE (network_id, node_id, listen_address),
	FOREIGN KEY (network_id) REFERENCES "networks" (id) ON DELETE CASCADE,
	FOREIGN KEY (node_id) REFERENCES "nodes" (id) ON DELETE CASCADE
);

CREATE TABLE "networks_load_balancers_config" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_load_balancer_id INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	UNIQUE (network_load_balancer_id, key),
	FOREIGN KEY (network_load_balancer_id) REFERENCES "networks_load_balancers" (id) ON DELETE CASCADE
);
`)
	if err != nil {
		return fmt.Errorf("Failed creating network load balancers tables: %w", err)
	}

	return nil
}

func updateFromV59(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE networks_zones_records (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_zone_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	entries TEXT NOT NULL,
	UNIQUE (name),
	FOREIGN KEY (network_zone_id) REFERENCES networks_zones (id) ON DELETE CASCADE
);

CREATE TABLE networks_zones_records_config (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_zone_record_id INTEGER NOT NULL,
	key VARCHAR(255) NOT NULL,
	value TEXT,
	UNIQUE (network_zone_record_id, key),
	FOREIGN KEY (network_zone_record_id) REFERENCES networks_zones_records (id) ON DELETE CASCADE
);
`)
	if err != nil {
		return fmt.Errorf("Failed creating network zone records tables: %w", err)
	}

	return nil
}

func updateFromV58(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
UPDATE sqlite_sequence SET seq = (
    SELECT max(
        (SELECT coalesce(max(storage_volumes.id), 0) FROM storage_volumes),
        (SELECT coalesce(max(storage_volumes_snapshots.id), 0)
    FROM storage_volumes_snapshots)))
WHERE name='storage_volumes';
`)

	return err
}

func updateFromV57(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
UPDATE sqlite_sequence SET seq = (
    SELECT coalesce(max(max(coalesce(storage_volumes.id, 0)), max(coalesce(storage_volumes_snapshots.id, 0))), 0)
    FROM storage_volumes, storage_volumes_snapshots)
WHERE name='storage_volumes';
`)

	return err
}

func updateFromV56(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
UPDATE sqlite_sequence SET seq = (
    SELECT max(max(coalesce(storage_volumes.id, 0)), max(coalesce(storage_volumes_snapshots.id, 0)))
    FROM storage_volumes, storage_volumes_snapshots)
WHERE name='storage_volumes';
`)

	return err
}

func updateFromV55(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
DROP VIEW storage_volumes_all;

CREATE TABLE projects_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    UNIQUE (name)
);

INSERT INTO projects_new (id, name, description) SELECT id, name, IFNULL(description, '') FROM projects;

CREATE TABLE certificates_projects_new (
	certificate_id INTEGER NOT NULL,
	project_id INTEGER NOT NULL,
	FOREIGN KEY (certificate_id) REFERENCES certificates (id) ON DELETE CASCADE,
	FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE,
	UNIQUE (certificate_id, project_id)
);

INSERT INTO certificates_projects_new (certificate_id, project_id) SELECT certificate_id, project_id FROM certificates_projects;

CREATE TABLE images_new (
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
    project_id INTEGER NOT NULL,
    type INTEGER NOT NULL DEFAULT 0,
    UNIQUE (project_id, fingerprint),
    FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE
);

INSERT INTO images_new (id, fingerprint, filename, size, public, architecture, creation_date, expiry_date, upload_date, cached, last_use_date, auto_update, project_id, type)
	SELECT id, fingerprint, filename, size, public, architecture, creation_date, expiry_date, upload_date, cached, last_use_date, auto_update, project_id, type FROM images;

CREATE TABLE images_aliases_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT NOT NULL,
    project_id INTEGER NOT NULL,
    UNIQUE (project_id, name),
    FOREIGN KEY (image_id) REFERENCES images_new (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE
);

INSERT INTO images_aliases_new (id, name, image_id, description, project_id)
	SELECT id, name, image_id, IFNULL(description, ''), project_id FROM images_aliases;

CREATE TABLE nodes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    address TEXT NOT NULL,
    schema INTEGER NOT NULL,
    api_extensions INTEGER NOT NULL,
    heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP,
    state INTEGER NOT NULL DEFAULT 0,
    arch INTEGER NOT NULL DEFAULT 0 CHECK (arch > 0),
    failure_domain_id INTEGER DEFAULT NULL REFERENCES nodes_failure_domains (id) ON DELETE SET NULL,
    UNIQUE (name),
    UNIQUE (address)
);

INSERT INTO nodes_new (id, name, description, address, schema, api_extensions, heartbeat, state, arch, failure_domain_id)
    SELECT id, name, IFNULL(description, ''), address, schema, api_extensions, heartbeat, state, arch, failure_domain_id FROM nodes;

CREATE TABLE images_nodes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (image_id, node_id),
    FOREIGN KEY (image_id) REFERENCES images_new (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE
);

INSERT INTO images_nodes_new (id, image_id, node_id)
    SELECT id, image_id, node_id FROM images_nodes;

CREATE TABLE profiles_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    project_id INTEGER NOT NULL,
    UNIQUE (project_id, name),
    FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE
);

INSERT INTO profiles_new (id, name, description, project_id)
    SELECT id, name, IFNULL(description, ''), project_id FROM profiles;

CREATE TABLE images_profiles_new (
	image_id INTEGER NOT NULL,
	profile_id INTEGER NOT NULL,
	FOREIGN KEY (image_id) REFERENCES images_new (id) ON DELETE CASCADE,
	FOREIGN KEY (profile_id) REFERENCES profiles_new (id) ON DELETE CASCADE,
	UNIQUE (image_id, profile_id)
);

INSERT INTO images_profiles_new (image_id, profile_id)
    SELECT image_id, profile_id FROM images_profiles;

CREATE TABLE images_properties_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (image_id) REFERENCES images_new (id) ON DELETE CASCADE
);

INSERT INTO images_properties_new (id, image_id, type, key, value)
    SELECT id, image_id, type, key, value FROM images_properties;

CREATE TABLE images_source_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    server TEXT NOT NULL,
    protocol INTEGER NOT NULL,
    certificate TEXT NOT NULL,
    alias TEXT NOT NULL,
    FOREIGN KEY (image_id) REFERENCES images_new (id) ON DELETE CASCADE
);

INSERT INTO images_source_new (id, image_id, server, protocol, certificate, alias)
    SELECT id, image_id, server, protocol, certificate, alias FROM images_source;

CREATE TABLE instances_new (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    node_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    ephemeral INTEGER NOT NULL DEFAULT 0,
    creation_date DATETIME NOT NULL DEFAULT 0,
    stateful INTEGER NOT NULL DEFAULT 0,
    last_use_date DATETIME,
    description TEXT NOT NULL,
    project_id INTEGER NOT NULL,
    expiry_date DATETIME,
    UNIQUE (project_id, name),
    FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE
);

INSERT INTO instances_new (id, node_id, name, architecture, type, ephemeral, creation_date, stateful, last_use_date, description, project_id, expiry_date)
    SELECT id, node_id, name, architecture, type, ephemeral, creation_date, stateful, last_use_date, IFNULL(description, ''), project_id, expiry_date FROM instances;

CREATE TABLE instances_backups_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    instance_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    container_only INTEGER NOT NULL default 0,
    optimized_storage INTEGER NOT NULL default 0,
    FOREIGN KEY (instance_id) REFERENCES instances_new (id) ON DELETE CASCADE,
    UNIQUE (instance_id, name)
);

INSERT INTO instances_backups_new (id, instance_id, name, creation_date, expiry_date, container_only, optimized_storage)
    SELECT id, instance_id, name, creation_date, expiry_date, container_only, optimized_storage FROM instances_backups;

CREATE TABLE instances_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    instance_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (instance_id) REFERENCES instances_new (id) ON DELETE CASCADE,
    UNIQUE (instance_id, key)
);

INSERT INTO instances_config_new (id, instance_id, key, value)
    SELECT id, instance_id, key, value FROM instances_config;

CREATE TABLE instances_devices_new (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    instance_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    FOREIGN KEY (instance_id) REFERENCES instances_new (id) ON DELETE CASCADE,
    UNIQUE (instance_id, name)
);

INSERT INTO instances_devices_new (id, instance_id, name, type)
    SELECT id, instance_id, name, type FROM instances_devices;

CREATE TABLE instances_devices_config_new (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    instance_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (instance_device_id) REFERENCES instances_devices_new (id) ON DELETE CASCADE,
    UNIQUE (instance_device_id, key)
);

INSERT INTO instances_devices_config_new (id, instance_device_id, key, value)
    SELECT id, instance_device_id, key, value FROM instances_devices_config;

CREATE TABLE instances_profiles_new (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    instance_id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    apply_order INTEGER NOT NULL default 0,
    UNIQUE (instance_id, profile_id),
    FOREIGN KEY (instance_id) REFERENCES instances_new (id) ON DELETE CASCADE,
    FOREIGN KEY (profile_id) REFERENCES profiles_new(id) ON DELETE CASCADE
);

INSERT INTO instances_profiles_new (id, instance_id, profile_id, apply_order)
    SELECT id, instance_id, profile_id, apply_order FROM instances_profiles;

CREATE TABLE instances_snapshots_new (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    instance_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    creation_date DATETIME NOT NULL DEFAULT 0,
    stateful INTEGER NOT NULL DEFAULT 0,
    description TEXT NOT NULL,
    expiry_date DATETIME,
    UNIQUE (instance_id, name),
    FOREIGN KEY (instance_id) REFERENCES instances_new (id) ON DELETE CASCADE
);

INSERT INTO instances_snapshots_new (id, instance_id, name, creation_date, stateful, description, expiry_date)
    SELECT id, instance_id, name, creation_date, stateful, IFNULL(description, ''), expiry_date FROM instances_snapshots;

CREATE TABLE instances_snapshots_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    instance_snapshot_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (instance_snapshot_id) REFERENCES instances_snapshots_new (id) ON DELETE CASCADE,
    UNIQUE (instance_snapshot_id, key)
);

INSERT INTO instances_snapshots_config_new (id, instance_snapshot_id, key, value)
    SELECT id, instance_snapshot_id, key, value FROM instances_snapshots_config;

CREATE TABLE instances_snapshots_devices_new (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    instance_snapshot_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    FOREIGN KEY (instance_snapshot_id) REFERENCES instances_snapshots_new (id) ON DELETE CASCADE,
    UNIQUE (instance_snapshot_id, name)
);

INSERT INTO instances_snapshots_devices_new (id, instance_snapshot_id, name, type)
    SELECT id, instance_snapshot_id, name, type FROM instances_snapshots_devices;

CREATE TABLE instances_snapshots_devices_config_new (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    instance_snapshot_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (instance_snapshot_device_id) REFERENCES instances_snapshots_devices_new (id) ON DELETE CASCADE,
    UNIQUE (instance_snapshot_device_id, key)
);

INSERT INTO instances_snapshots_devices_config_new (id, instance_snapshot_device_id, key, value)
    SELECT id, instance_snapshot_device_id, key, value FROM instances_snapshots_devices_config;

CREATE TABLE networks_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    project_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    state INTEGER NOT NULL DEFAULT 0,
    type INTEGER NOT NULL DEFAULT 0,
    UNIQUE (project_id, name),
    FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE
);

INSERT INTO networks_new (id, project_id, name, description, state, type)
    SELECT id, project_id, name, IFNULL(description, ''), state, type FROM networks;

CREATE TABLE networks_acls_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    project_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    ingress TEXT NOT NULL,
    egress TEXT NOT NULL,
    UNIQUE (project_id, name),
    FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE
);

INSERT INTO networks_acls_new (id, project_id, name, description, ingress, egress)
    SELECT id, project_id, name, IFNULL(description, ''), ingress, egress FROM networks_acls;

CREATE TABLE networks_acls_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_acl_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (network_acl_id, key),
    FOREIGN KEY (network_acl_id) REFERENCES networks_acls_new (id) ON DELETE CASCADE
);

CREATE TABLE networks_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (network_id, node_id, key),
    FOREIGN KEY (network_id) REFERENCES networks_new (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE
);

INSERT INTO networks_config_new (id, network_id, node_id, key, value)
    SELECT id, network_id, node_id, key, value FROM networks_config;

CREATE TABLE networks_forwards_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_id INTEGER NOT NULL,
	node_id INTEGER,
	listen_address TEXT NOT NULL,
	description TEXT NOT NULL,
	ports TEXT NOT NULL,
	UNIQUE (network_id, node_id, listen_address),
	FOREIGN KEY (network_id) REFERENCES networks_new (id) ON DELETE CASCADE,
	FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE
);

INSERT INTO networks_forwards_new (id, network_id, node_id, listen_address, description, ports)
    SELECT id, network_id, node_id, listen_address, IFNULL(description, ''), ports FROM networks_forwards;

CREATE TABLE networks_forwards_config_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_forward_id INTEGER NOT NULL,
	key VARCHAR(255) NOT NULL,
	value TEXT,
	UNIQUE (network_forward_id, key),
	FOREIGN KEY (network_forward_id) REFERENCES networks_forwards_new (id) ON DELETE CASCADE
);

INSERT INTO networks_forwards_config_new (id, network_forward_id, key, value)
    SELECT id, network_forward_id, key, value FROM networks_forwards_config;

CREATE TABLE networks_nodes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    state INTEGER NOT NULL DEFAULT 0,
    UNIQUE (network_id, node_id),
    FOREIGN KEY (network_id) REFERENCES networks_new (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE
);

INSERT INTO networks_nodes_new (id, network_id, node_id, state)
    SELECT id, network_id, node_id, state FROM networks_nodes;

CREATE TABLE networks_peers_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	target_network_project TEXT NULL,
	target_network_name TEXT NULL,
	target_network_id INTEGER NULL,
	UNIQUE (network_id, name),
	UNIQUE (network_id, target_network_project, target_network_name),
	UNIQUE (network_id, target_network_id),
	FOREIGN KEY (network_id) REFERENCES networks_new (id) ON DELETE CASCADE
);

INSERT INTO networks_peers_new (id, network_id, name, description, target_network_project, target_network_name, target_network_id)
    SELECT id, network_id, name, IFNULL(description, ''), target_network_project, target_network_name, target_network_id FROM networks_peers;

CREATE TABLE networks_peers_config_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_peer_id INTEGER NOT NULL,
	key VARCHAR(255) NOT NULL,
	value TEXT,
	UNIQUE (network_peer_id, key),
	FOREIGN KEY (network_peer_id) REFERENCES networks_peers_new (id) ON DELETE CASCADE
);

INSERT INTO networks_peers_config_new (id, network_peer_id, key, value)
    SELECT id, network_peer_id, key, value FROM networks_peers_config;

CREATE TABLE networks_zones_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	project_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	UNIQUE (name),
	FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE
);

INSERT INTO networks_zones_new (id, project_id, name, description)
    SELECT id, project_id, name, IFNULL(description, '') FROM networks_zones;

CREATE TABLE networks_zones_config_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_zone_id INTEGER NOT NULL,
	key VARCHAR(255) NOT NULL,
	value TEXT,
	UNIQUE (network_zone_id, key),
	FOREIGN KEY (network_zone_id) REFERENCES networks_zones_new (id) ON DELETE CASCADE
);

INSERT INTO networks_zones_config_new (id, network_zone_id, key, value)
    SELECT id, network_zone_id, key, value FROM networks_zones_config;

CREATE TABLE nodes_cluster_groups_new (
    node_id INTEGER NOT NULL,
    group_id INTEGER NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE,
    FOREIGN KEY (group_id) REFERENCES cluster_groups (id) ON DELETE CASCADE,
    UNIQUE (node_id, group_id)
);

INSERT INTO nodes_cluster_groups_new (node_id, group_id)
    SELECT node_id, group_id FROM nodes_cluster_groups;

CREATE TABLE nodes_config_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	node_id INTEGER NOT NULL,
	key TEXT NOT NULL,
	value TEXT,
	FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE,
	UNIQUE (node_id, key)
);

INSERT INTO nodes_config_new (id, node_id, key, value)
    SELECT id, node_id, key, value FROM nodes_config;

CREATE TABLE nodes_roles_new (
    node_id INTEGER NOT NULL,
    role INTEGER NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE,
    UNIQUE (node_id, role)
);

INSERT INTO nodes_roles_new (node_id, role)
    SELECT node_id, role FROM nodes_roles;

CREATE TABLE operations_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    uuid TEXT NOT NULL,
    node_id TEXT NOT NULL,
    type INTEGER NOT NULL DEFAULT 0,
    project_id INTEGER,
    UNIQUE (uuid),
    FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE
);

INSERT INTO operations_new (id, uuid, node_id, type, project_id)
    SELECT id, uuid, node_id, type, project_id FROM operations;

CREATE TABLE profiles_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (profile_id, key),
    FOREIGN KEY (profile_id) REFERENCES profiles_new(id) ON DELETE CASCADE
);

INSERT INTO profiles_config_new (id, profile_id, key, value)
    SELECT id, profile_id, key, value FROM profiles_config;

CREATE TABLE profiles_devices_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (profile_id, name),
    FOREIGN KEY (profile_id) REFERENCES profiles_new (id) ON DELETE CASCADE
);

INSERT INTO profiles_devices_new (id, profile_id, name, type)
    SELECT id, profile_id, name, type FROM profiles_devices;

CREATE TABLE profiles_devices_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (profile_device_id, key),
    FOREIGN KEY (profile_device_id) REFERENCES profiles_devices_new (id) ON DELETE CASCADE
);

INSERT INTO profiles_devices_config_new (id, profile_device_id, key, value)
    SELECT id, profile_device_id, key, value FROM profiles_devices_config;

CREATE TABLE projects_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    project_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE,
    UNIQUE (project_id, key)
);

INSERT INTO projects_config_new (id, project_id, key, value)
    SELECT id, project_id, key, value FROM projects_config;

CREATE TABLE storage_pools_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    driver TEXT NOT NULL,
    description TEXT NOT NULL,
    state INTEGER NOT NULL DEFAULT 0,
    UNIQUE (name)
);

INSERT INTO storage_pools_new (id, name, driver, description, state)
    SELECT id, name, driver, IFNULL(description, ''), state FROM storage_pools;

CREATE TABLE storage_pools_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (storage_pool_id, node_id, key),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools_new (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE
);

INSERT INTO storage_pools_config_new (id, storage_pool_id, node_id, key, value)
    SELECT id, storage_pool_id, node_id, key, value FROM storage_pools_config;

CREATE TABLE storage_pools_nodes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    state INTEGER NOT NULL DEFAULT 0,
    UNIQUE (storage_pool_id, node_id),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools_new (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE
);

INSERT INTO storage_pools_nodes_new (id, storage_pool_id, node_id, state)
    SELECT id, storage_pool_id, node_id, state FROM storage_pools_nodes;

CREATE TABLE storage_volumes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER,
    type INTEGER NOT NULL,
    description TEXT NOT NULL,
    project_id INTEGER NOT NULL,
    content_type INTEGER NOT NULL DEFAULT 0,
    UNIQUE (storage_pool_id, node_id, project_id, name, type),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools_new (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes_new (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE
);

INSERT INTO storage_volumes_new (id, name, storage_pool_id, node_id, type, description, project_id, content_type)
    SELECT id, name, storage_pool_id, node_id, type, IFNULL(description, ''), project_id, content_type FROM storage_volumes;

CREATE TABLE storage_volumes_backups_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    volume_only INTEGER NOT NULL default 0,
    optimized_storage INTEGER NOT NULL default 0,
    FOREIGN KEY (storage_volume_id) REFERENCES storage_volumes_new (id) ON DELETE CASCADE,
    UNIQUE (storage_volume_id, name)
);

INSERT INTO storage_volumes_backups_new (id, storage_volume_id, name, creation_date, expiry_date, volume_only, optimized_storage)
    SELECT id, storage_volume_id, name, creation_date, expiry_date, volume_only, optimized_storage FROM storage_volumes_backups;

CREATE TABLE storage_volumes_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (storage_volume_id, key),
    FOREIGN KEY (storage_volume_id) REFERENCES storage_volumes_new (id) ON DELETE CASCADE
);

INSERT INTO storage_volumes_config_new (id, storage_volume_id, key, value)
    SELECT id, storage_volume_id, key, value FROM storage_volumes_config;

CREATE TABLE storage_volumes_snapshots_new (
    id INTEGER NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    expiry_date DATETIME,
    UNIQUE (id),
    UNIQUE (storage_volume_id, name),
    FOREIGN KEY (storage_volume_id) REFERENCES storage_volumes_new (id) ON DELETE CASCADE
);

INSERT INTO storage_volumes_snapshots_new (id, storage_volume_id, name, description, expiry_date)
    SELECT id, storage_volume_id, name, IFNULL(description, ''), expiry_date FROM storage_volumes_snapshots;

CREATE TABLE storage_volumes_snapshots_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_snapshot_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (storage_volume_snapshot_id) REFERENCES storage_volumes_snapshots_new (id) ON DELETE CASCADE,
    UNIQUE (storage_volume_snapshot_id, key)
);

INSERT INTO storage_volumes_snapshots_config_new (id, storage_volume_snapshot_id, key, value)
    SELECT id, storage_volume_snapshot_id, key, value FROM storage_volumes_snapshots_config;

CREATE TABLE warnings_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	node_id INTEGER,
	project_id INTEGER,
	entity_type_code INTEGER,
	entity_id INTEGER,
	uuid TEXT NOT NULL,
	type_code INTEGER NOT NULL,
	status INTEGER NOT NULL,
	first_seen_date DATETIME NOT NULL,
	last_seen_date DATETIME NOT NULL,
	updated_date DATETIME,
	last_message TEXT NOT NULL,
	count INTEGER NOT NULL,
	UNIQUE (uuid),
	FOREIGN KEY (node_id) REFERENCES nodes_new(id) ON DELETE CASCADE,
	FOREIGN KEY (project_id) REFERENCES projects_new (id) ON DELETE CASCADE
);

INSERT INTO warnings_new (id, node_id, project_id, entity_type_code, entity_id, uuid, type_code, status, first_seen_date, last_seen_date, updated_date, last_message, count)
    SELECT id, node_id, project_id, entity_type_code, entity_id, uuid, type_code, status, first_seen_date, last_seen_date, updated_date, last_message, count FROM warnings;

DROP TABLE warnings;
DROP TABLE storage_volumes_snapshots_config;
DROP TABLE storage_volumes_snapshots;
DROP TABLE storage_volumes_config;
DROP TABLE storage_volumes_backups;
DROP TABLE storage_volumes;
DROP TABLE storage_pools_nodes;
DROP TABLE storage_pools_config;
DROP TABLE storage_pools;
DROP TABLE projects_config;
DROP TABLE profiles_devices_config;
DROP TABLE profiles_devices;
DROP TABLE profiles_config;
DROP TABLE operations;
DROP TABLE nodes_roles;
DROP TABLE nodes_config;
DROP TABLE nodes_cluster_groups;
DROP TABLE networks_zones_config;
DROP TABLE networks_zones;
DROP TABLE networks_peers_config;
DROP TABLE networks_peers;
DROP TABLE networks_nodes;
DROP TABLE networks_forwards_config;
DROP TABLE networks_forwards;
DROP TABLE networks_config;
DROP TABLE networks_acls_config;
DROP TABLE networks_acls;
DROP TABLE networks;
DROP TABLE instances_snapshots_devices_config;
DROP TABLE instances_snapshots_devices;
DROP TABLE instances_snapshots_config;
DROP TABLE instances_snapshots;
DROP TABLE instances_profiles;
DROP TABLE instances_devices_config;
DROP TABLE instances_devices;
DROP TABLE instances_config;
DROP TABLE instances_backups;
DROP TABLE instances;
DROP TABLE images_source;
DROP TABLE images_properties;
DROP TABLE images_profiles;
DROP TABLE profiles;
DROP TABLE images_nodes;
DROP TABLE images_aliases;
DROP TABLE nodes;
DROP TABLE certificates_projects;
DROP TABLE images;
DROP TABLE projects;

ALTER TABLE projects_new RENAME TO projects;
ALTER TABLE certificates_projects_new RENAME TO certificates_projects;
ALTER TABLE images_new RENAME TO images;
ALTER TABLE images_aliases_new RENAME TO images_aliases;
ALTER TABLE nodes_new RENAME TO nodes;
ALTER TABLE images_nodes_new RENAME TO images_nodes;
ALTER TABLE profiles_new RENAME TO profiles;
ALTER TABLE images_profiles_new RENAME TO images_profiles;
ALTER TABLE images_properties_new RENAME TO images_properties;
ALTER TABLE images_source_new RENAME TO images_source;
ALTER TABLE instances_new RENAME TO instances;
ALTER TABLE instances_backups_new RENAME TO instances_backups;
ALTER TABLE instances_config_new RENAME TO instances_config;
ALTER TABLE instances_devices_new RENAME TO instances_devices;
ALTER TABLE instances_devices_config_new RENAME TO instances_devices_config;
ALTER TABLE instances_profiles_new RENAME TO instances_profiles;
ALTER TABLE instances_snapshots_new RENAME TO instances_snapshots;
ALTER TABLE instances_snapshots_config_new RENAME TO instances_snapshots_config;
ALTER TABLE instances_snapshots_devices_new RENAME TO instances_snapshots_devices;
ALTER TABLE instances_snapshots_devices_config_new RENAME TO instances_snapshots_devices_config;
ALTER TABLE networks_new RENAME TO networks;
ALTER TABLE networks_acls_new RENAME TO networks_acls;
ALTER TABLE networks_acls_config_new RENAME TO networks_acls_config;
ALTER TABLE networks_config_new RENAME TO networks_config;
ALTER TABLE networks_forwards_new RENAME TO networks_forwards;
ALTER TABLE networks_forwards_config_new RENAME TO networks_forwards_config;
ALTER TABLE networks_nodes_new RENAME TO networks_nodes;
ALTER TABLE networks_peers_new RENAME TO networks_peers;
ALTER TABLE networks_peers_config_new RENAME TO networks_peers_config;
ALTER TABLE networks_zones_new RENAME TO networks_zones;
ALTER TABLE networks_zones_config_new RENAME TO networks_zones_config;
ALTER TABLE nodes_cluster_groups_new RENAME TO nodes_cluster_groups;
ALTER TABLE nodes_config_new RENAME TO nodes_config;
ALTER TABLE nodes_roles_new RENAME TO nodes_roles;
ALTER TABLE operations_new RENAME TO operations;
ALTER TABLE profiles_config_new RENAME TO profiles_config;
ALTER TABLE profiles_devices_new RENAME TO profiles_devices;
ALTER TABLE profiles_devices_config_new RENAME TO profiles_devices_config;
ALTER TABLE projects_config_new RENAME TO projects_config;
ALTER TABLE storage_pools_new RENAME TO storage_pools;
ALTER TABLE storage_pools_config_new RENAME TO storage_pools_config;
ALTER TABLE storage_pools_nodes_new RENAME TO storage_pools_nodes;
ALTER TABLE storage_volumes_new RENAME TO storage_volumes;
ALTER TABLE storage_volumes_backups_new RENAME TO storage_volumes_backups;
ALTER TABLE storage_volumes_config_new RENAME TO storage_volumes_config;
ALTER TABLE storage_volumes_snapshots_new RENAME TO storage_volumes_snapshots;
ALTER TABLE storage_volumes_snapshots_config_new RENAME TO storage_volumes_snapshots_config;
ALTER TABLE warnings_new RENAME TO warnings;

CREATE INDEX images_aliases_project_id_idx ON images_aliases (project_id);
CREATE INDEX images_project_id_idx ON images (project_id);
CREATE INDEX instances_project_id_and_name_idx ON instances (project_id, name);
CREATE INDEX instances_project_id_and_node_id_and_name_idx ON instances (project_id, node_id, name);
CREATE INDEX instances_project_id_and_node_id_idx ON instances (project_id, node_id);
CREATE INDEX instances_project_id_idx ON instances (project_id);
CREATE UNIQUE INDEX storage_pools_unique_storage_pool_id_node_id_key ON storage_pools_config (storage_pool_id, IFNULL(node_id, -1), key);
CREATE INDEX instances_node_id_idx ON instances (node_id);
CREATE UNIQUE INDEX networks_unique_network_id_node_id_key ON "networks_config" (network_id, IFNULL(node_id, -1), key);
CREATE INDEX profiles_project_id_idx ON profiles (project_id);
CREATE UNIQUE INDEX warnings_unique_node_id_project_id_entity_type_code_entity_id_type_code ON warnings(IFNULL(node_id, -1), IFNULL(project_id, -1), entity_type_code, entity_id, type_code);

CREATE TRIGGER storage_volumes_check_id
  BEFORE INSERT ON storage_volumes
  WHEN NEW.id IN (SELECT id FROM storage_volumes_snapshots)
  BEGIN
    SELECT RAISE(FAIL,
    "invalid ID");
  END;

CREATE TRIGGER storage_volumes_snapshots_check_id
  BEFORE INSERT ON storage_volumes_snapshots
  WHEN NEW.id IN (SELECT id FROM storage_volumes)
  BEGIN
    SELECT RAISE(FAIL,
    "invalid ID");
  END;

CREATE VIEW storage_volumes_all (
         id,
         name,
         storage_pool_id,
         node_id,
         type,
         description,
         project_id,
         content_type) AS
  SELECT id,
         name,
         storage_pool_id,
         node_id,
         type,
         description,
         project_id,
         content_type
    FROM storage_volumes UNION
  SELECT storage_volumes_snapshots.id,
         printf('%s/%s',
    storage_volumes.name,
    storage_volumes_snapshots.name),
         storage_volumes.storage_pool_id,
         storage_volumes.node_id,
         storage_volumes.type,
         storage_volumes_snapshots.description,
         storage_volumes.project_id,
         storage_volumes.content_type
    FROM storage_volumes
    JOIN storage_volumes_snapshots ON storage_volumes.id = storage_volumes_snapshots.storage_volume_id;
`)
	if err != nil {
		return fmt.Errorf("Could not add not null constraint to description field: %w", err)
	}

	return nil
}

func updateFromV54(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
DROP VIEW certificates_projects_ref;
DROP VIEW instances_config_ref;
DROP VIEW instances_devices_ref;
DROP VIEW instances_profiles_ref;
DROP VIEW instances_snapshots_config_ref;
DROP VIEW instances_snapshots_devices_ref;
DROP VIEW profiles_config_ref;
DROP VIEW profiles_devices_ref;
DROP VIEW profiles_used_by_ref;
DROP VIEW projects_config_ref;
DROP VIEW projects_used_by_ref;
`)
	if err != nil {
		return fmt.Errorf("Failed to drop database views: %w", err)
	}

	return nil
}

// updateFromV53 creates the cluster_groups and nodes_cluster_groups tables.
func updateFromV53(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE "cluster_groups" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    UNIQUE (name)
);

CREATE TABLE "nodes_cluster_groups" (
    node_id INTEGER NOT NULL,
    group_id INTEGER NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (group_id) REFERENCES cluster_groups (id) ON DELETE CASCADE,
    UNIQUE (node_id, group_id)
);

INSERT INTO cluster_groups (id, name, description) VALUES (1, 'default', 'Default cluster group');

INSERT INTO nodes_cluster_groups (node_id, group_id) SELECT id, 1 FROM nodes;
`)
	if err != nil {
		return fmt.Errorf("Failed creating cluster group tables: %w", err)
	}

	return nil
}

// updateFromV52 creates the networks_zones and networks_zones_config tables.
func updateFromV52(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE "networks_zones" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	project_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	UNIQUE (name),
	FOREIGN KEY (project_id) REFERENCES "projects" (id) ON DELETE CASCADE
);

CREATE TABLE "networks_zones_config" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_zone_id INTEGER NOT NULL,
	key VARCHAR(255) NOT NULL,
	value TEXT,
	UNIQUE (network_zone_id, key),
	FOREIGN KEY (network_zone_id) REFERENCES "networks_zones" (id) ON DELETE CASCADE
);
`)
	if err != nil {
		return fmt.Errorf("Failed creating network zones tables: %w", err)
	}

	return nil
}

// updateFromV51 creates the networks_peers and networks_peers_config tables.
func updateFromV51(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE "networks_peers" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	target_network_project TEXT NULL,
	target_network_name TEXT NULL,
	target_network_id INTEGER NULL,
	UNIQUE (network_id, name),
	UNIQUE (network_id, target_network_project, target_network_name),
	UNIQUE (network_id, target_network_id),
	FOREIGN KEY (network_id) REFERENCES "networks" (id) ON DELETE CASCADE
);

CREATE TABLE "networks_peers_config" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_peer_id INTEGER NOT NULL,
	key VARCHAR(255) NOT NULL,
	value TEXT,
	UNIQUE (network_peer_id, key),
	FOREIGN KEY (network_peer_id) REFERENCES "networks_peers" (id) ON DELETE CASCADE
);
`)
	if err != nil {
		return fmt.Errorf("Failed creating network peers tables: %w", err)
	}

	return nil
}

// updateFromV50 creates the nodes_config table.
func updateFromV50(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE "nodes_config" (
id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
node_id INTEGER NOT NULL,
key TEXT NOT NULL,
value TEXT,
FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
UNIQUE (node_id, key)
);
	`)

	if err != nil {
		return fmt.Errorf("Failed creating nodes_config table: %w", err)
	}

	return nil
}

// updateFromV49 creates the networks_forwards and networks_forwards_config tables.
func updateFromV49(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE "networks_forwards" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_id INTEGER NOT NULL,
	node_id INTEGER,
	listen_address TEXT NOT NULL,
	description TEXT NOT NULL,
	ports TEXT NOT NULL,
	UNIQUE (network_id, node_id, listen_address),
	FOREIGN KEY (network_id) REFERENCES "networks" (id) ON DELETE CASCADE,
	FOREIGN KEY (node_id) REFERENCES "nodes" (id) ON DELETE CASCADE
);

CREATE TABLE "networks_forwards_config" (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	network_forward_id INTEGER NOT NULL,
	key VARCHAR(255) NOT NULL,
	value TEXT,
	UNIQUE (network_forward_id, key),
	FOREIGN KEY (network_forward_id) REFERENCES "networks_forwards" (id) ON DELETE CASCADE
);
`)
	if err != nil {
		return fmt.Errorf("Failed creating network forwards tables: %w", err)
	}

	return nil
}

// updateFromV48 renames the "pending" column to "state" in the "nodes" table.
func updateFromV48(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
ALTER TABLE nodes
RENAME COLUMN pending TO state;
`)
	if err != nil {
		return fmt.Errorf(`Failed to rename column "pending" to "state" in table "nodes": %w`, err)
	}

	return nil
}

// updateFromV47 adds warnings.
func updateFromV47(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE warnings (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	node_id INTEGER,
	project_id INTEGER,
	entity_type_code INTEGER,
	entity_id INTEGER,
	uuid TEXT NOT NULL,
	type_code INTEGER NOT NULL,
	status INTEGER NOT NULL,
	first_seen_date DATETIME NOT NULL,
	last_seen_date DATETIME NOT NULL,
	updated_date DATETIME,
	last_message TEXT NOT NULL,
	count INTEGER NOT NULL,
	UNIQUE (uuid),
	FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
	FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX warnings_unique_node_id_project_id_entity_type_code_entity_id_type_code ON warnings(IFNULL(node_id, -1), IFNULL(project_id, -1), entity_type_code, entity_id, type_code);
`)
	if err != nil {
		return fmt.Errorf("Failed to create warnings table and warnings_unique_node_id_project_id_entity_type_code_entity_id_type_code index: %w", err)
	}

	return err
}

// updateFromV46 adds support for restricting certificates to projects.
func updateFromV46(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
ALTER TABLE certificates ADD COLUMN restricted INTEGER NOT NULL DEFAULT 0;
CREATE TABLE certificates_projects (
	certificate_id INTEGER NOT NULL,
	project_id INTEGER NOT NULL,
	FOREIGN KEY (certificate_id) REFERENCES certificates (id) ON DELETE CASCADE,
	FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
	UNIQUE (certificate_id, project_id)
);
CREATE VIEW certificates_projects_ref (fingerprint, value) AS
	SELECT certificates.fingerprint, projects.name FROM certificates_projects
		JOIN certificates ON certificates.id=certificates_projects.certificate_id
		JOIN projects ON projects.id=certificates_projects.project_id
		ORDER BY projects.name;
`)
	if err != nil {
		return fmt.Errorf("Failed extending certificates to support project restrictions: %w", err)
	}

	return nil
}

// updateFromV45 updates projects_used_by_ref to include ceph volumes.
func updateFromV45(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
DROP VIEW projects_used_by_ref;
CREATE VIEW projects_used_by_ref (name,
    value) AS
  SELECT projects.name,
    printf('/1.0/instances/%s?project=%s',
    "instances".name,
    projects.name)
    FROM "instances" JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/images/%s?project=%s',
    images.fingerprint,
    projects.name)
    FROM images JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/storage-pools/%s/volumes/custom/%s?project=%s&target=%s',
    storage_pools.name,
    storage_volumes.name,
    projects.name,
    nodes.name)
    FROM storage_volumes JOIN storage_pools ON storage_pool_id=storage_pools.id JOIN nodes ON node_id=nodes.id JOIN projects ON project_id=projects.id WHERE storage_volumes.type=2 UNION
  SELECT projects.name,
    printf('/1.0/storage-pools/%s/volumes/custom/%s?project=%s',
    storage_pools.name,
    storage_volumes.name,
    projects.name)
    FROM storage_volumes JOIN storage_pools ON storage_pool_id=storage_pools.id JOIN projects ON project_id=projects.id WHERE storage_volumes.type=2 AND storage_volumes.node_id IS NULL UNION
  SELECT projects.name,
    printf('/1.0/profiles/%s?project=%s',
    profiles.name,
    projects.name)
    FROM profiles JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/networks/%s?project=%s',
    networks.name,
    projects.name)
    FROM networks JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/network-acls/%s?project=%s',
    networks_acls.name,
    projects.name)
    FROM networks_acls JOIN projects ON project_id=projects.id;
`)
	if err != nil {
		return fmt.Errorf("Failed to update projects_used_by_ref: %w", err)
	}

	return nil
}

// updateFromV44 adds networks_acls table, and adds a foreign key relationship between networks and projects.
// API extension: network_acl.
func updateFromV44(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
DROP VIEW projects_used_by_ref;

CREATE TABLE networks_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    project_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    state INTEGER NOT NULL DEFAULT 0,
    type INTEGER NOT NULL DEFAULT 0,
    UNIQUE (project_id, name),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

INSERT INTO networks_new (id, project_id, name, description, state, type)
    SELECT id, project_id, name, description, state, type FROM networks;

CREATE TABLE networks_nodes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    state INTEGER NOT NULL DEFAULT 0,
    UNIQUE (network_id, node_id),
    FOREIGN KEY (network_id) REFERENCES networks_new (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);

INSERT INTO networks_nodes_new (id, network_id, node_id, state)
    SELECT id, network_id, node_id, state FROM networks_nodes;

CREATE TABLE networks_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (network_id, node_id, key),
    FOREIGN KEY (network_id) REFERENCES networks_new (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);

INSERT INTO networks_config_new (id, network_id, node_id, key, value)
    SELECT id, network_id, node_id, key, value FROM networks_config;

DROP TABLE networks;
DROP TABLE networks_nodes;
DROP TABLE networks_config;

CREATE UNIQUE INDEX networks_unique_network_id_node_id_key ON networks_config_new (network_id, IFNULL(node_id, -1), key);

ALTER TABLE networks_new RENAME TO networks;
ALTER TABLE networks_nodes_new RENAME TO networks_nodes;
ALTER TABLE networks_config_new RENAME TO networks_config;

CREATE TABLE networks_acls (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    project_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    ingress TEXT NOT NULL,
    egress TEXT NOT NULL,
    UNIQUE (project_id, name),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE networks_acls_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_acl_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (network_acl_id, key),
    FOREIGN KEY (network_acl_id) REFERENCES networks_acls (id) ON DELETE CASCADE
);

CREATE VIEW projects_used_by_ref (name,
    value) AS
  SELECT projects.name,
    printf('/1.0/instances/%s?project=%s',
    "instances".name,
    projects.name)
    FROM "instances" JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/images/%s?project=%s',
    images.fingerprint,
    projects.name)
    FROM images JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/storage-pools/%s/volumes/custom/%s?project=%s&target=%s',
    storage_pools.name,
    storage_volumes.name,
    projects.name,
    nodes.name)
    FROM storage_volumes JOIN storage_pools ON storage_pool_id=storage_pools.id JOIN nodes ON node_id=nodes.id JOIN projects ON project_id=projects.id WHERE storage_volumes.type=2 UNION
  SELECT projects.name,
    printf('/1.0/profiles/%s?project=%s',
    profiles.name,
    projects.name)
    FROM profiles JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/networks/%s?project=%s',
    networks.name,
    projects.name)
    FROM networks JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/network-acls/%s?project=%s',
    networks_acls.name,
    projects.name)
    FROM networks_acls JOIN projects ON project_id=projects.id;
`)
	if err != nil {
		return fmt.Errorf("Failed to add networks_acls and networks_acls_config tables, and update projects_used_by_ref view: %w", err)
	}

	return nil
}

// updateFromV43 adds a unique index to the storage_pools_config and networks_config tables.
func updateFromV43(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE UNIQUE INDEX storage_pools_unique_storage_pool_id_node_id_key ON storage_pools_config (storage_pool_id, IFNULL(node_id, -1), key);
		CREATE UNIQUE INDEX networks_unique_network_id_node_id_key ON networks_config (network_id, IFNULL(node_id, -1), key);
	`)
	if err != nil {
		return fmt.Errorf("Failed adding unique index to storage_pools_config and networks_config tables: %w", err)
	}

	return nil
}

// updateFromV42 removes any duplicated storage pool config rows that have the same value.
// This can occur when multiple create requests have been issued when setting up a clustered storage pool.
func updateFromV42(ctx context.Context, tx *sql.Tx) error {
	// Find all duplicated config rows and return comma delimited list of affected row IDs for each dupe set.
	stmt := `SELECT storage_pool_id, IFNULL(node_id, -1), key, value, COUNT(*) AS rowCount, GROUP_CONCAT(id, ",") AS dupeRowIDs
			FROM storage_pools_config
			GROUP BY storage_pool_id, node_id, key, value
			HAVING rowCount > 1
		`
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return fmt.Errorf("Failed running query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	type dupeRow struct {
		storagePoolID int64
		nodeID        int64
		key           string
		value         string
		rowCount      int64
		dupeRowIDs    string
	}

	var dupeRows []dupeRow

	for rows.Next() {
		r := dupeRow{}
		err = rows.Scan(&r.storagePoolID, &r.nodeID, &r.key, &r.value, &r.rowCount, &r.dupeRowIDs)
		if err != nil {
			return fmt.Errorf("Failed scanning rows: %w", err)
		}

		dupeRows = append(dupeRows, r)
	}

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("Got a row error: %w", err)
	}

	for _, r := range dupeRows {
		logger.Warn("Found duplicated storage pool config rows", logger.Ctx{"storagePoolID": r.storagePoolID, "nodeID": r.nodeID, "key": r.key, "value": r.value, "rowCount": r.rowCount, "dupeRowIDs": r.dupeRowIDs})

		rowIDs := strings.Split(r.dupeRowIDs, ",")

		// Iterate and delete all but 1 of the rowIDs so we leave just one left.
		for i := 0; i < len(rowIDs)-1; i++ {
			rowID, err := strconv.Atoi(rowIDs[i])
			if err != nil {
				return fmt.Errorf("Failed converting row ID: %w", err)
			}

			_, err = tx.Exec("DELETE FROM storage_pools_config WHERE id = ?", rowID)
			if err != nil {
				return fmt.Errorf("Failed deleting storage pool config row with ID %d: %w", rowID, err)
			}

			logger.Warn("Deleted duplicated storage pool config row", logger.Ctx{"storagePoolID": r.storagePoolID, "nodeID": r.nodeID, "key": r.key, "value": r.value, "rowCount": r.rowCount, "rowID": rowID})
		}
	}

	return nil
}

// updateFromV41 removes any duplicated network config rows that have the same value.
// This can occur when multiple create requests have been issued when setting up a clustered network.
func updateFromV41(ctx context.Context, tx *sql.Tx) error {
	// Find all duplicated config rows and return comma delimited list of affected row IDs for each dupe set.
	stmt := `SELECT network_id, IFNULL(node_id, -1), key, value, COUNT(*) AS rowCount, GROUP_CONCAT(id, ",") AS dupeRowIDs
			FROM networks_config
			GROUP BY network_id, node_id, key, value
			HAVING rowCount > 1
		`
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return fmt.Errorf("Failed running query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	type dupeRow struct {
		networkID  int64
		nodeID     int64
		key        string
		value      string
		rowCount   int64
		dupeRowIDs string
	}

	var dupeRows []dupeRow

	for rows.Next() {
		r := dupeRow{}
		err = rows.Scan(&r.networkID, &r.nodeID, &r.key, &r.value, &r.rowCount, &r.dupeRowIDs)
		if err != nil {
			return fmt.Errorf("Failed scanning rows: %w", err)
		}

		dupeRows = append(dupeRows, r)
	}

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("Got a row error: %w", err)
	}

	for _, r := range dupeRows {
		logger.Warn("Found duplicated network config rows", logger.Ctx{"networkID": r.networkID, "nodeID": r.nodeID, "key": r.key, "value": r.value, "rowCount": r.rowCount, "dupeRowIDs": r.dupeRowIDs})

		rowIDs := strings.Split(r.dupeRowIDs, ",")

		// Iterate and delete all but 1 of the rowIDs so we leave just one left.
		for i := 0; i < len(rowIDs)-1; i++ {
			rowID, err := strconv.Atoi(rowIDs[i])
			if err != nil {
				return fmt.Errorf("Failed converting row ID: %w", err)
			}

			_, err = tx.Exec("DELETE FROM networks_config WHERE id = ?", rowID)
			if err != nil {
				return fmt.Errorf("Failed deleting network config row with ID %d: %w", rowID, err)
			}

			logger.Warn("Deleted duplicated network config row", logger.Ctx{"networkID": r.networkID, "nodeID": r.nodeID, "key": r.key, "value": r.value, "rowCount": r.rowCount, "rowID": rowID})
		}
	}

	return nil
}

// Add state column to storage_pools_nodes tables. Set existing row's state to 1 ("created").
func updateFromV40(ctx context.Context, tx *sql.Tx) error {
	stmt := `
		ALTER TABLE storage_pools_nodes ADD COLUMN state INTEGER NOT NULL DEFAULT 0;
		UPDATE storage_pools_nodes SET state = 1;
	`
	_, err := tx.Exec(stmt)
	return err
}

// Add state column to networks_nodes tables. Set existing row's state to 1 ("created").
func updateFromV39(ctx context.Context, tx *sql.Tx) error {
	stmt := `
		ALTER TABLE networks_nodes ADD COLUMN state INTEGER NOT NULL DEFAULT 0;
		UPDATE networks_nodes SET state = 1;
	`
	_, err := tx.Exec(stmt)
	return err
}

// Add storage_volumes_backups table.
func updateFromV38(ctx context.Context, tx *sql.Tx) error {
	stmt := `
CREATE TABLE storage_volumes_backups (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    volume_only INTEGER NOT NULL default 0,
    optimized_storage INTEGER NOT NULL default 0,
    FOREIGN KEY (storage_volume_id) REFERENCES "storage_volumes" (id) ON DELETE CASCADE,
    UNIQUE (storage_volume_id, name)
);
`
	_, err := tx.Exec(stmt)
	if err != nil {
		return err
	}

	return nil
}

// Attempt to add missing project features.networks feature to default project.
func updateFromV37(ctx context.Context, tx *sql.Tx) error {
	ids, err := query.SelectIntegers(ctx, tx, `SELECT id FROM projects WHERE name = "default" LIMIT 1`)
	if err != nil {
		return err
	}

	if len(ids) == 1 {
		_, _ = tx.Exec("INSERT INTO projects_config (project_id, key, value) VALUES (?, 'features.networks', 'true');", ids[0])
	}

	return nil
}

// Add networks to projects references.
func updateFromV36(ctx context.Context, tx *sql.Tx) error {
	stmts := `
DROP VIEW projects_used_by_ref;
CREATE VIEW projects_used_by_ref (name,
    value) AS
  SELECT projects.name,
    printf('/1.0/instances/%s?project=%s',
    "instances".name,
    projects.name)
    FROM "instances" JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/images/%s?project=%s',
    images.fingerprint,
    projects.name)
    FROM images JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/storage-pools/%s/volumes/custom/%s?project=%s&target=%s',
    storage_pools.name,
    storage_volumes.name,
    projects.name,
    nodes.name)
    FROM storage_volumes JOIN storage_pools ON storage_pool_id=storage_pools.id JOIN nodes ON node_id=nodes.id JOIN projects ON project_id=projects.id WHERE storage_volumes.type=2 UNION
  SELECT projects.name,
    printf('/1.0/profiles/%s?project=%s',
    profiles.name,
    projects.name)
    FROM profiles JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/networks/%s?project=%s',
    networks.name,
    projects.name)
    FROM networks JOIN projects ON project_id=projects.id;
`
	_, err := tx.Exec(stmts)
	return err
}

// This fixes node IDs of storage volumes on non-remote pools which were
// wrongly set to NULL.
func updateFromV35(ctx context.Context, tx *sql.Tx) error {
	stmts := `
WITH storage_volumes_tmp (id, node_id)
AS (
  SELECT storage_volumes.id, storage_pools_nodes.node_id
  FROM storage_volumes
	JOIN storage_pools_nodes ON storage_pools_nodes.storage_pool_id=storage_volumes.storage_pool_id
	JOIN storage_pools ON storage_pools.id=storage_volumes.storage_pool_id
  WHERE storage_pools.driver NOT IN ("ceph", "cephfs"))
UPDATE storage_volumes
SET node_id=(
  SELECT storage_volumes_tmp.node_id
  FROM storage_volumes_tmp
  WHERE storage_volumes.id=storage_volumes_tmp.id)
WHERE id IN (SELECT id FROM storage_volumes_tmp) AND node_id IS NULL
`

	_, err := tx.Exec(stmts)
	if err != nil {
		return err
	}

	return nil
}

// Remove multiple entries of the same volume when using remote storage.
// Also, allow node ID to be null for the instances and storage_volumes tables, and set it to null
// for instances and storage volumes using remote storage.
func updateFromV34(ctx context.Context, tx *sql.Tx) error {
	stmts := `
SELECT storage_volumes.id, storage_volumes.name
FROM storage_volumes
JOIN storage_pools ON storage_pools.id=storage_volumes.storage_pool_id
WHERE storage_pools.driver IN ("ceph", "cephfs")
ORDER BY storage_volumes.name
`

	// Get the total number of storage volume rows.
	count, err := query.Count(ctx, tx, "storage_volumes JOIN storage_pools ON storage_pools.id=storage_volumes.storage_pool_id",
		`storage_pools.driver IN ("ceph", "cephfs")`)
	if err != nil {
		return fmt.Errorf("Failed to get storage volumes count: %w", err)
	}

	type volume struct {
		ID            int
		Name          string
		StoragePoolID int
		NodeID        string
		Type          int
		Description   string
		ProjectID     int
		ContentType   int
	}

	volumes := make([]volume, 0, count)
	err = query.Scan(ctx, tx, stmts, func(scan func(dest ...any) error) error {
		vol := volume{}
		err := scan(&vol.ID, &vol.Name)
		if err != nil {
			return err
		}

		volumes = append(volumes, vol)

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to fetch storage volumes with remote storage: %w", err)
	}

	// Remove multiple entries of the same volume when using remote storage
	for i := 1; i < count; i++ {
		if volumes[i-1].Name == volumes[i].Name {
			_, err = tx.Exec(`DELETE FROM storage_volumes WHERE id=?`, volumes[i-1].ID)
			if err != nil {
				return fmt.Errorf("Failed to delete row from storage_volumes: %w", err)
			}
		}
	}

	stmts = `
CREATE TABLE storage_volumes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER,
    type INTEGER NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    content_type INTEGER NOT NULL DEFAULT 0,
    UNIQUE (storage_pool_id, node_id, project_id, name, type),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);`

	// Create new tables where node ID can be null.
	_, err = tx.Exec(stmts)
	if err != nil {
		return err
	}

	// Copy rows from storage_volumes to storage_volumes_new
	count, err = query.Count(ctx, tx, "storage_volumes", "")
	if err != nil {
		return fmt.Errorf("Failed to get storage_volumes count: %w", err)
	}

	storageVolumes := make([]volume, 0, count)

	sqlStr := `
SELECT id, name, storage_pool_id, node_id, type, coalesce(description, ''), project_id, content_type
FROM storage_volumes`

	err = query.Scan(ctx, tx, sqlStr, func(scan func(dest ...any) error) error {
		vol := volume{}
		err := scan(&vol.ID, &vol.Name, &vol.StoragePoolID, &vol.NodeID, &vol.Type, &vol.Description, &vol.ProjectID, &vol.ContentType)
		if err != nil {
			return err
		}

		storageVolumes = append(storageVolumes, vol)

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to fetch storage volumes: %w", err)
	}

	for _, storageVolume := range storageVolumes {
		_, err = tx.Exec(`
INSERT INTO storage_volumes_new (id, name, storage_pool_id, node_id, type, description, project_id, content_type)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);`,
			storageVolume.ID, storageVolume.Name, storageVolume.StoragePoolID, storageVolume.NodeID,
			storageVolume.Type, storageVolume.Description, storageVolume.ProjectID, storageVolume.ContentType)
		if err != nil {
			return err
		}
	}

	// Store rows of storage_volumes_config as we need to re-add them at the end.
	count, err = query.Count(ctx, tx, "storage_volumes_config", "")
	if err != nil {
		return fmt.Errorf("Failed to get storage_volumes_config count: %w", err)
	}

	type volumeConfig struct {
		ID              int
		StorageVolumeID int
		Key             string
		Value           string
	}

	storageVolumeConfigs := make([]volumeConfig, 0, count)
	sqlStr = `SELECT * FROM storage_volumes_config;`
	err = query.Scan(ctx, tx, sqlStr, func(scan func(dest ...any) error) error {
		config := volumeConfig{}
		err := scan(&config.ID, &config.StorageVolumeID, &config.Key, &config.Value)
		if err != nil {
			return err
		}

		storageVolumeConfigs = append(storageVolumeConfigs, config)

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to fetch storage volume configs: %w", err)
	}

	// Store rows of storage_volumes_snapshots as we need to re-add them at the end.
	count, err = query.Count(ctx, tx, "storage_volumes_snapshots", "")
	if err != nil {
		return fmt.Errorf("Failed to get storage_volumes_snapshots count: %w", err)
	}

	type volumeSnapshot struct {
		ID              int
		StorageVolumeID int
		Name            string
		Description     string
		ExpiryDate      sql.NullTime
	}

	sqlStr = `SELECT * FROM storage_volumes_snapshots;`
	storageVolumeSnapshots := make([]volumeSnapshot, 0, count)
	err = query.Scan(ctx, tx, sqlStr, func(scan func(dest ...any) error) error {
		vol := volumeSnapshot{}
		err := scan(&vol.ID, &vol.StorageVolumeID, &vol.Name, &vol.Description, &vol.ExpiryDate)
		if err != nil {
			return err
		}

		storageVolumeSnapshots = append(storageVolumeSnapshots, vol)

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to fetch storage volume snapshots: %w", err)
	}

	// Store rows of storage_volumes_snapshots_config as we need to re-add them at the end.
	count, err = query.Count(ctx, tx, "storage_volumes_snapshots_config", "")
	if err != nil {
		return fmt.Errorf("Failed to get storage_volumes_snapshots_config count: %w", err)
	}

	type volumeSnapshotConfig struct {
		ID                      int
		StorageVolumeSnapshotID int
		Key                     string
		Value                   string
	}

	storageVolumeSnapshotConfigs := make([]volumeSnapshotConfig, 0, count)

	sqlStr = `SELECT * FROM storage_volumes_snapshots_config;`
	err = query.Scan(ctx, tx, sqlStr, func(scan func(dest ...any) error) error {
		config := volumeSnapshotConfig{}
		err := scan(&config.ID, &config.StorageVolumeSnapshotID, &config.Key, &config.Value)
		if err != nil {
			return err
		}

		storageVolumeSnapshotConfigs = append(storageVolumeSnapshotConfigs, config)

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to fetch storage volume snapshot configs: %w", err)
	}

	_, err = tx.Exec(`
PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

DROP TABLE storage_volumes;
ALTER TABLE storage_volumes_new RENAME TO storage_volumes;

UPDATE storage_volumes
SET node_id=null
WHERE storage_volumes.id IN (
  SELECT storage_volumes.id FROM storage_volumes
  JOIN storage_pools ON storage_volumes.storage_pool_id=storage_pools.id
  WHERE storage_pools.driver IN ("ceph", "cephfs")
);

PRAGMA foreign_keys = ON;
PRAGMA legacy_alter_table = OFF;

CREATE TRIGGER storage_volumes_check_id
  BEFORE INSERT ON storage_volumes
  WHEN NEW.id IN (SELECT id FROM storage_volumes_snapshots)
  BEGIN
    SELECT RAISE(FAIL, "invalid ID");
  END;
`)
	if err != nil {
		return err
	}

	// When we dropped the storage_volumes table earlier, all config entries
	// were removed as well. Let's re-add them.
	for _, storageVolumeConfig := range storageVolumeConfigs {
		_, err = tx.Exec(`INSERT INTO storage_volumes_config (id, storage_volume_id, key, value) VALUES (?, ?, ?, ?);`, storageVolumeConfig.ID, storageVolumeConfig.StorageVolumeID, storageVolumeConfig.Key, storageVolumeConfig.Value)
		if err != nil {
			return err
		}
	}

	// When we dropped the storage_volumes table earlier, all snapshot entries
	// were removed as well. Let's re-add them.
	for _, storageVolumeSnapshot := range storageVolumeSnapshots {
		_, err = tx.Exec(`INSERT INTO storage_volumes_snapshots (id, storage_volume_id, name, description, expiry_date) VALUES (?, ?, ?, ?, ?);`, storageVolumeSnapshot.ID, storageVolumeSnapshot.StorageVolumeID, storageVolumeSnapshot.Name, storageVolumeSnapshot.Description, storageVolumeSnapshot.ExpiryDate)
		if err != nil {
			return err
		}
	}

	// When we dropped the storage_volumes table earlier, all snapshot config entries
	// were removed as well. Let's re-add them.
	for _, storageVolumeSnapshotConfig := range storageVolumeSnapshotConfigs {
		_, err = tx.Exec(`INSERT INTO storage_volumes_snapshots_config (id, storage_volume_snapshot_id, key, value) VALUES (?, ?, ?, ?);`, storageVolumeSnapshotConfig.ID, storageVolumeSnapshotConfig.StorageVolumeSnapshotID, storageVolumeSnapshotConfig.Key, storageVolumeSnapshotConfig.Value)
		if err != nil {
			return err
		}
	}

	count, err = query.Count(ctx, tx, "storage_volumes_all", "")
	if err != nil {
		return fmt.Errorf("Failed to get storage_volumes count: %w", err)
	}

	if count > 0 {
		var maxID int64

		row := tx.QueryRowContext(ctx, "SELECT MAX(id) FROM storage_volumes_all LIMIT 1")
		err = row.Scan(&maxID)
		if err != nil {
			return err
		}

		// Set sqlite_sequence to max(id)
		_, err = tx.Exec("UPDATE sqlite_sequence SET seq = ? WHERE name = 'storage_volumes'", maxID)
		if err != nil {
			return fmt.Errorf("Increment storage volumes sequence: %w", err)
		}
	}

	return nil
}

// Add project_id field to networks, add unique index across project_id and name,
// and set existing networks to project_id 1.
// This is made a lot more complex because it requires re-creating the referenced tables as there is no way to
// disable foreign keys temporarily within a transaction.
func updateFromV33(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE networks_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    project_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    state INTEGER NOT NULL DEFAULT 0,
    type INTEGER NOT NULL DEFAULT 0,
    UNIQUE (project_id, name)
);

INSERT INTO networks_new (id, project_id, name, description, state, type)
    SELECT id, 1, name, description, state, type FROM networks;

CREATE TABLE networks_nodes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (network_id, node_id),
    FOREIGN KEY (network_id) REFERENCES networks_new (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);

INSERT INTO networks_nodes_new (id, network_id, node_id)
    SELECT id, network_id, node_id FROM networks_nodes;

CREATE TABLE networks_config_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (network_id, node_id, key),
    FOREIGN KEY (network_id) REFERENCES networks_new (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);

INSERT INTO networks_config_new (id, network_id, node_id, key, value)
    SELECT id, network_id, node_id, key, value FROM networks_config;

DROP TABLE networks;
DROP TABLE networks_nodes;
DROP TABLE networks_config;

ALTER TABLE networks_new RENAME TO networks;
ALTER TABLE networks_nodes_new RENAME TO networks_nodes;
ALTER TABLE networks_config_new RENAME TO networks_config;
	`)
	if err != nil {
		return fmt.Errorf("Failed to add project_id column to networks table: %w", err)
	}

	return nil
}

// Add type field to networks.
func updateFromV32(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec("ALTER TABLE networks ADD COLUMN type INTEGER NOT NULL DEFAULT 0;")
	if err != nil {
		return fmt.Errorf("Failed to add type column to networks table: %w", err)
	}

	return nil
}

// Add failure_domain column to nodes table.
func updateFromV31(ctx context.Context, tx *sql.Tx) error {
	stmts := `
CREATE TABLE nodes_failure_domains (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    UNIQUE (name)
);
ALTER TABLE nodes
 ADD COLUMN failure_domain_id INTEGER DEFAULT NULL REFERENCES nodes_failure_domains (id) ON DELETE SET NULL;
`
	_, err := tx.Exec(stmts)
	if err != nil {
		return err
	}

	return nil
}

// Add content type field to storage volumes.
func updateFromV30(ctx context.Context, tx *sql.Tx) error {
	stmts := `ALTER TABLE storage_volumes ADD COLUMN content_type INTEGER NOT NULL DEFAULT 0;
UPDATE storage_volumes SET content_type = 1 WHERE type = 3;
UPDATE storage_volumes SET content_type = 1 WHERE storage_volumes.id IN (
	SELECT storage_volumes.id
	  FROM storage_volumes
	  JOIN images ON storage_volumes.name = images.fingerprint
	  WHERE images.type = 1
);
DROP VIEW storage_volumes_all;
CREATE VIEW storage_volumes_all (
         id,
         name,
         storage_pool_id,
         node_id,
         type,
         description,
         project_id,
         content_type) AS
  SELECT id,
         name,
         storage_pool_id,
         node_id,
         type,
         description,
         project_id,
         content_type
    FROM storage_volumes UNION
  SELECT storage_volumes_snapshots.id,
         printf('%s/%s', storage_volumes.name, storage_volumes_snapshots.name),
         storage_volumes.storage_pool_id,
         storage_volumes.node_id,
         storage_volumes.type,
         storage_volumes_snapshots.description,
         storage_volumes.project_id,
         storage_volumes.content_type
    FROM storage_volumes
    JOIN storage_volumes_snapshots ON storage_volumes.id = storage_volumes_snapshots.storage_volume_id;
`
	_, err := tx.Exec(stmts)
	if err != nil {
		return fmt.Errorf("Failed to add storage volume content type: %w", err)
	}

	return nil
}

// Add storage volumes to projects references and fix images.
func updateFromV29(ctx context.Context, tx *sql.Tx) error {
	stmts := `
DROP VIEW projects_used_by_ref;
CREATE VIEW projects_used_by_ref (name,
    value) AS
  SELECT projects.name,
    printf('/1.0/instances/%s?project=%s',
    "instances".name,
    projects.name)
    FROM "instances" JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/images/%s?project=%s',
    images.fingerprint,
    projects.name)
    FROM images JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/storage-pools/%s/volumes/custom/%s?project=%s&target=%s',
    storage_pools.name,
    storage_volumes.name,
    projects.name,
    nodes.name)
    FROM storage_volumes JOIN storage_pools ON storage_pool_id=storage_pools.id JOIN nodes ON node_id=nodes.id JOIN projects ON project_id=projects.id WHERE storage_volumes.type=2 UNION
  SELECT projects.name,
    printf('/1.0/profiles/%s?project=%s',
    profiles.name,
    projects.name)
    FROM profiles JOIN projects ON project_id=projects.id;
`
	_, err := tx.Exec(stmts)
	return err
}

// Attempt to add missing project feature.
func updateFromV28(ctx context.Context, tx *sql.Tx) error {
	_, _ = tx.Exec("INSERT INTO projects_config (project_id, key, value) VALUES (1, 'features.storage.volumes', 'true');")
	return nil
}

// Add expiry date to storage volume snapshots.
func updateFromV27(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec("ALTER TABLE storage_volumes_snapshots ADD COLUMN expiry_date DATETIME;")
	return err
}

// Bump the sqlite_sequence value for storage volumes, to avoid unique
// constraint violations when inserting new snapshots.
func updateFromV26(ctx context.Context, tx *sql.Tx) error {
	ids, err := query.SelectIntegers(ctx, tx, "SELECT coalesce(max(id), 0) FROM storage_volumes_all")
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE sqlite_sequence SET seq = ? WHERE name = 'storage_volumes'", ids[0])
	return err
}

// Create new storage snapshot tables and migrate data to them.
func updateFromV25(ctx context.Context, tx *sql.Tx) error {
	// Get the total number of snapshot rows in the storage_volumes table.
	count, err := query.Count(ctx, tx, "storage_volumes", "snapshot=1")
	if err != nil {
		return fmt.Errorf("Failed to volume snapshot count: %w", err)
	}

	type snapshot struct {
		ID            int
		Name          string
		StoragePoolID int
		NodeID        int
		Type          int
		Description   string
		ProjectID     int
		Config        map[string]string
	}

	sql := `
SELECT id, name, storage_pool_id, node_id, type, coalesce(description, ''), project_id
  FROM storage_volumes
 WHERE snapshot=1
`
	if err != nil {
		return fmt.Errorf("Failed to prepare volume snapshot query: %w", err)
	}

	// Fetch all snapshot rows in the storage_volumes table.
	snapshots := make([]snapshot, 0, count)
	err = query.Scan(ctx, tx, sql, func(scan func(dest ...any) error) error {
		s := snapshot{}
		err := scan(&s.ID, &s.Name, &s.StoragePoolID, &s.NodeID, &s.Type, &s.Description, &s.ProjectID)
		if err != nil {
			return err
		}

		snapshots = append(snapshots, s)

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to fetch instances: %w", err)
	}

	for i, snapshot := range snapshots {
		config, err := query.SelectConfig(ctx, tx,
			"storage_volumes_config", "storage_volume_id=?",
			snapshot.ID)
		if err != nil {
			return fmt.Errorf("Failed to fetch volume snapshot config: %w", err)
		}

		snapshots[i].Config = config
	}

	stmts := `
ALTER TABLE storage_volumes RENAME TO old_storage_volumes;
CREATE TABLE "storage_volumes" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (storage_pool_id, node_id, project_id, name, type),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
ALTER TABLE storage_volumes_config RENAME TO old_storage_volumes_config;
CREATE TABLE storage_volumes_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (storage_volume_id, key),
    FOREIGN KEY (storage_volume_id) REFERENCES storage_volumes (id) ON DELETE CASCADE
);
INSERT INTO storage_volumes(id, name, storage_pool_id, node_id, type, description, project_id)
   SELECT id, name, storage_pool_id, node_id, type, description, project_id FROM old_storage_volumes
     WHERE snapshot=0;
INSERT INTO storage_volumes_config
   SELECT * FROM old_storage_volumes_config
     WHERE storage_volume_id IN (SELECT id FROM storage_volumes);
DROP TABLE old_storage_volumes;
DROP TABLE old_storage_volumes_config;
CREATE TABLE storage_volumes_snapshots (
    id INTEGER NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    UNIQUE (id),
    UNIQUE (storage_volume_id, name),
    FOREIGN KEY (storage_volume_id) REFERENCES storage_volumes (id) ON DELETE CASCADE
);
CREATE TRIGGER storage_volumes_check_id
  BEFORE INSERT ON storage_volumes
  WHEN NEW.id IN (SELECT id FROM storage_volumes_snapshots)
  BEGIN
    SELECT RAISE(FAIL, "invalid ID");
  END;
CREATE TRIGGER storage_volumes_snapshots_check_id
  BEFORE INSERT ON storage_volumes_snapshots
  WHEN NEW.id IN (SELECT id FROM storage_volumes)
  BEGIN
    SELECT RAISE(FAIL, "invalid ID");
  END;
CREATE TABLE storage_volumes_snapshots_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_snapshot_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (storage_volume_snapshot_id) REFERENCES storage_volumes_snapshots (id) ON DELETE CASCADE,
    UNIQUE (storage_volume_snapshot_id, key)
);
CREATE VIEW storage_volumes_all (
         id,
         name,
         storage_pool_id,
         node_id,
         type,
         description,
         project_id) AS
  SELECT id,
         name,
         storage_pool_id,
         node_id,
         type,
         description,
         project_id
    FROM storage_volumes UNION
  SELECT storage_volumes_snapshots.id,
         printf('%s/%s', storage_volumes.name, storage_volumes_snapshots.name),
         storage_volumes.storage_pool_id,
         storage_volumes.node_id,
         storage_volumes.type,
         storage_volumes_snapshots.description,
         storage_volumes.project_id
    FROM storage_volumes
    JOIN storage_volumes_snapshots ON storage_volumes.id = storage_volumes_snapshots.storage_volume_id;
`
	_, err = tx.Exec(stmts)
	if err != nil {
		return fmt.Errorf("Failed to create storage snapshots tables: %w", err)
	}

	// Migrate snapshots to the new tables.
	for _, snapshot := range snapshots {
		parts := strings.Split(snapshot.Name, internalInstance.SnapshotDelimiter)
		if len(parts) != 2 {
			logger.Errorf("Invalid volume snapshot name: %s", snapshot.Name)
			continue
		}

		volume := parts[0]
		name := parts[1]
		ids, err := query.SelectIntegers(ctx, tx, "SELECT id FROM storage_volumes WHERE name=?", volume)
		if err != nil {
			return err
		}

		if len(ids) != 1 {
			logger.Errorf("Volume snapshot %s has no parent", snapshot.Name)
			continue
		}

		volumeID := ids[0]
		_, err = tx.Exec(`
INSERT INTO storage_volumes_snapshots(id, storage_volume_id, name, description) VALUES(?, ?, ?, ?)
`, snapshot.ID, volumeID, name, snapshot.Description)
		if err != nil {
			return err
		}

		for key, value := range snapshot.Config {
			_, err = tx.Exec(`
INSERT INTO storage_volumes_snapshots_config(storage_volume_snapshot_id, key, value) VALUES(?, ?, ?)
`, snapshot.ID, key, value)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// The ceph.user.name config key is required for Ceph to function.
func updateFromV24(ctx context.Context, tx *sql.Tx) error {
	// Fetch the IDs of all existing Ceph pools.
	poolIDs, err := query.SelectIntegers(ctx, tx, `SELECT id FROM storage_pools WHERE driver='ceph'`)
	if err != nil {
		return fmt.Errorf("Failed to get IDs of current ceph pools: %w", err)
	}

	for _, poolID := range poolIDs {
		// Fetch the config for this Ceph pool.
		config, err := query.SelectConfig(ctx, tx, "storage_pools_config", "storage_pool_id=?", poolID)
		if err != nil {
			return fmt.Errorf("Failed to fetch of ceph pool config: %w", err)
		}

		// Check if already set.
		_, ok := config["ceph.user.name"]
		if ok {
			continue
		}

		// Add ceph.user.name config entry.
		_, err = tx.Exec("INSERT INTO storage_pools_config (storage_pool_id, key, value) VALUES (?, 'ceph.user.name', 'admin')", poolID)
		if err != nil {
			return fmt.Errorf("Failed to create ceph.user.name config: %w", err)
		}
	}

	return nil
}

// The lvm.vg_name config key is required for LVM to function.
func updateFromV23(ctx context.Context, tx *sql.Tx) error {
	// Fetch the IDs of all existing nodes.
	nodeIDs, err := query.SelectIntegers(ctx, tx, "SELECT id FROM nodes")
	if err != nil {
		return fmt.Errorf("Failed to get IDs of current nodes: %w", err)
	}

	// Fetch the IDs of all existing lvm pools.
	poolIDs, err := query.SelectIntegers(ctx, tx, `SELECT id FROM storage_pools WHERE driver='lvm'`)
	if err != nil {
		return fmt.Errorf("Failed to get IDs of current lvm pools: %w", err)
	}

	for _, poolID := range poolIDs {
		for _, nodeID := range nodeIDs {
			// Fetch the config for this lvm pool.
			config, err := query.SelectConfig(ctx, tx, "storage_pools_config", "storage_pool_id=? AND node_id=?", poolID, nodeID)
			if err != nil {
				return fmt.Errorf("Failed to fetch of lvm pool config: %w", err)
			}

			// Check if already set.
			_, ok := config["lvm.vg_name"]
			if ok {
				continue
			}

			// Add lvm.vg_name config entry.
			_, err = tx.Exec(`
INSERT INTO storage_pools_config(storage_pool_id, node_id, key, value)
SELECT ?, ?, 'lvm.vg_name', name FROM storage_pools WHERE id=?
`, poolID, nodeID, poolID)
			if err != nil {
				return fmt.Errorf("Failed to create lvm.vg_name node config: %w", err)
			}
		}
	}

	return nil
}

// The zfs.pool_name config key is required for ZFS to function.
func updateFromV22(ctx context.Context, tx *sql.Tx) error {
	// Fetch the IDs of all existing nodes.
	nodeIDs, err := query.SelectIntegers(ctx, tx, "SELECT id FROM nodes")
	if err != nil {
		return fmt.Errorf("Failed to get IDs of current nodes: %w", err)
	}

	// Fetch the IDs of all existing zfs pools.
	poolIDs, err := query.SelectIntegers(ctx, tx, `SELECT id FROM storage_pools WHERE driver='zfs'`)
	if err != nil {
		return fmt.Errorf("Failed to get IDs of current zfs pools: %w", err)
	}

	for _, poolID := range poolIDs {
		for _, nodeID := range nodeIDs {
			// Fetch the config for this zfs pool.
			config, err := query.SelectConfig(ctx, tx, "storage_pools_config", "storage_pool_id=? AND node_id=?", poolID, nodeID)
			if err != nil {
				return fmt.Errorf("Failed to fetch of zfs pool config: %w", err)
			}

			// Check if already set.
			_, ok := config["zfs.pool_name"]
			if ok {
				continue
			}

			// Add zfs.pool_name config entry
			_, err = tx.Exec(`
INSERT INTO storage_pools_config(storage_pool_id, node_id, key, value)
SELECT ?, ?, 'zfs.pool_name', name FROM storage_pools WHERE id=?
`, poolID, nodeID, poolID)
			if err != nil {
				return fmt.Errorf("Failed to create zfs.pool_name node config: %w", err)
			}
		}
	}

	return nil
}

// Fix "images_profiles" table (missing UNIQUE).
func updateFromV21(ctx context.Context, tx *sql.Tx) error {
	stmts := `
ALTER TABLE images_profiles RENAME TO old_images_profiles;
CREATE TABLE images_profiles (
	image_id INTEGER NOT NULL,
	profile_id INTEGER NOT NULL,
	FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
	FOREIGN KEY (profile_id) REFERENCES profiles (id) ON DELETE CASCADE,
	UNIQUE (image_id, profile_id)
);
INSERT INTO images_profiles SELECT * FROM old_images_profiles;
DROP TABLE old_images_profiles;
`
	_, err := tx.Exec(stmts)
	return err
}

// Add "images_profiles" table.
func updateFromV20(ctx context.Context, tx *sql.Tx) error {
	stmts := `
CREATE TABLE images_profiles (
	image_id INTEGER NOT NULL,
	profile_id INTEGER NOT NULL,
	FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
	FOREIGN KEY (profile_id) REFERENCES profiles (id) ON DELETE CASCADE,
	UNIQUE (image_id, profile_id)
);
INSERT INTO images_profiles (image_id, profile_id)
	SELECT images.id, profiles.id FROM images
	JOIN profiles ON images.project_id = profiles.project_id
	WHERE profiles.name = 'default';
INSERT INTO images_profiles (image_id, profile_id)
	SELECT images.id, profiles.id FROM projects_config AS R
	JOIN projects_config AS S ON R.project_id = S.project_id
	JOIN images ON images.project_id = R.project_id
	JOIN profiles ON profiles.project_id = 1 AND profiles.name = "default"
	WHERE R.key = "features.images" AND S.key = "features.profiles" AND R.value = "true" AND S.value != "true";
INSERT INTO images_profiles (image_id, profile_id)
	SELECT images.id, profiles.id FROM projects_config AS R
	JOIN projects_config AS S ON R.project_id = S.project_id
	JOIN profiles ON profiles.project_id = R.project_id
	JOIN images ON images.project_id = 1
	WHERE R.key = "features.images" AND S.key = "features.profiles" AND R.value != "true" AND S.value = "true"
		AND profiles.name = "default";
`
	_, err := tx.Exec(stmts)
	return err
}

// Add a new "arch" column to the "nodes" table.
func updateFromV19(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec("PRAGMA ignore_check_constraints=on")
	if err != nil {
		return err
	}

	defer func() { _, _ = tx.Exec("PRAGMA ignore_check_constraints=off") }()

	// The column has a not-null constraint and a default value of
	// 0. However, leaving the 0 default won't effectively be accepted when
	// creating a new, due to the check constraint, so we are sure to end
	// up with a valid value.
	_, err = tx.Exec("ALTER TABLE nodes ADD COLUMN arch INTEGER NOT NULL DEFAULT 0 CHECK (arch > 0)")
	if err != nil {
		return err
	}

	arch, err := osarch.ArchitectureGetLocalID()
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE nodes SET arch = ?", arch)
	if err != nil {
		return err
	}

	return nil
}

// Rename 'containers' to 'instances' in *_used_by_ref views.
func updateFromV18(ctx context.Context, tx *sql.Tx) error {
	stmts := `
DROP VIEW profiles_used_by_ref;
CREATE VIEW profiles_used_by_ref (project,
    name,
    value) AS
  SELECT projects.name,
    profiles.name,
    printf('/1.0/instances/%s?project=%s',
    "instances".name,
    instances_projects.name)
    FROM profiles
    JOIN projects ON projects.id=profiles.project_id
    JOIN "instances_profiles"
      ON "instances_profiles".profile_id=profiles.id
    JOIN "instances"
      ON "instances".id="instances_profiles".instance_id
    JOIN projects AS instances_projects
      ON instances_projects.id="instances".project_id;
DROP VIEW projects_used_by_ref;
CREATE VIEW projects_used_by_ref (name,
    value) AS
  SELECT projects.name,
    printf('/1.0/instances/%s?project=%s',
    "instances".name,
    projects.name)
    FROM "instances" JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/images/%s',
    images.fingerprint)
    FROM images JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/profiles/%s?project=%s',
    profiles.name,
    projects.name)
    FROM profiles JOIN projects ON project_id=projects.id;
`
	_, err := tx.Exec(stmts)
	return err
}

// Add nodes_roles table.
func updateFromV17(ctx context.Context, tx *sql.Tx) error {
	stmts := `
CREATE TABLE nodes_roles (
    node_id INTEGER NOT NULL,
    role INTEGER NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    UNIQUE (node_id, role)
);
`
	_, err := tx.Exec(stmts)
	return err
}

// Add image type column.
func updateFromV16(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec("ALTER TABLE images ADD COLUMN type INTEGER NOT NULL DEFAULT 0;")
	return err
}

// Create new snapshot tables and migrate data to them.
func updateFromV15(ctx context.Context, tx *sql.Tx) error {
	stmts := `
CREATE TABLE instances_snapshots (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    instance_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    creation_date DATETIME NOT NULL DEFAULT 0,
    stateful INTEGER NOT NULL DEFAULT 0,
    description TEXT,
    expiry_date DATETIME,
    UNIQUE (instance_id, name),
    FOREIGN KEY (instance_id) REFERENCES instances (id) ON DELETE CASCADE
);
CREATE TABLE instances_snapshots_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    instance_snapshot_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (instance_snapshot_id) REFERENCES instances_snapshots (id) ON DELETE CASCADE,
    UNIQUE (instance_snapshot_id, key)
);
CREATE TABLE instances_snapshots_devices (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    instance_snapshot_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    FOREIGN KEY (instance_snapshot_id) REFERENCES instances_snapshots (id) ON DELETE CASCADE,
    UNIQUE (instance_snapshot_id, name)
);
CREATE TABLE instances_snapshots_devices_config (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    instance_snapshot_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (instance_snapshot_device_id) REFERENCES instances_snapshots_devices (id) ON DELETE CASCADE,
    UNIQUE (instance_snapshot_device_id, key)
);
CREATE VIEW instances_snapshots_config_ref (
  project,
  instance,
  name,
  key,
  value) AS
  SELECT
    projects.name,
    instances.name,
    instances_snapshots.name,
    instances_snapshots_config.key,
    instances_snapshots_config.value
  FROM instances_snapshots_config
    JOIN instances_snapshots ON instances_snapshots.id=instances_snapshots_config.instance_snapshot_id
    JOIN instances ON instances.id=instances_snapshots.instance_id
    JOIN projects ON projects.id=instances.project_id;
CREATE VIEW instances_snapshots_devices_ref (
  project,
  instance,
  name,
  device,
  type,
  key,
  value) AS
  SELECT
    projects.name,
    instances.name,
    instances_snapshots.name,
    instances_snapshots_devices.name,
    instances_snapshots_devices.type,
    coalesce(instances_snapshots_devices_config.key, ''),
    coalesce(instances_snapshots_devices_config.value, '')
  FROM instances_snapshots_devices
    LEFT OUTER JOIN instances_snapshots_devices_config
      ON instances_snapshots_devices_config.instance_snapshot_device_id=instances_snapshots_devices.id
     JOIN instances ON instances.id=instances_snapshots.instance_id
     JOIN projects ON projects.id=instances.project_id
     JOIN instances_snapshots ON instances_snapshots.id=instances_snapshots_devices.instance_snapshot_id
`
	_, err := tx.Exec(stmts)
	if err != nil {
		return fmt.Errorf("Failed to create snapshots tables: %w", err)
	}

	// Get the total number of rows in the instances table.
	count, err := query.Count(ctx, tx, "instances", "")
	if err != nil {
		return fmt.Errorf("Failed to count rows in instances table: %w", err)
	}

	// Fetch all rows in the instances table.
	type instance struct {
		ID           int
		Name         string
		Type         int
		CreationDate time.Time
		Stateful     bool
		Description  string
		ExpiryDate   sql.NullTime
	}

	sql := `SELECT id, name, type, creation_date, stateful, coalesce(description, ''), expiry_date FROM instances`
	instances := make([]instance, 0, count)
	err = query.Scan(ctx, tx, sql, func(scan func(dest ...any) error) error {
		inst := instance{}
		err := scan(&inst.ID, &inst.Name, &inst.Type, &inst.CreationDate, &inst.Stateful, &inst.Description, &inst.ExpiryDate)
		if err != nil {
			return err
		}

		instances = append(instances, inst)

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to fetch instances: %w", err)
	}

	// Create an index mapping instance names to their IDs.
	instanceIDsByName := make(map[string]int)
	for _, instance := range instances {
		if instance.Type == 1 {
			continue
		}

		instanceIDsByName[instance.Name] = instance.ID
	}

	// Fetch all rows in the instances_config table that references
	// snapshots and index them by instance ID.
	count, err = query.Count(
		ctx,
		tx,
		"instances_config JOIN instances ON instances_config.instance_id = instances.id",
		"instances.type = 1")
	if err != nil {
		return fmt.Errorf("Failed to count rows in instances_config table: %w", err)
	}

	type instanceConfig struct {
		ID         int
		InstanceID int
		Key        string
		Value      string
	}

	configs := make([]instanceConfig, 0, count)
	sql = `
SELECT instances_config.id, instance_id, key, value
  FROM instances_config JOIN instances ON instances_config.instance_id = instances.id
  WHERE instances.type = 1
`

	err = query.Scan(ctx, tx, sql, func(scan func(dest ...any) error) error {
		config := instanceConfig{}
		err := scan(&config.ID, &config.InstanceID, &config.Key, &config.Value)
		if err != nil {
			return err
		}

		configs = append(configs, config)

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to fetch snapshots config: %w", err)
	}

	configBySnapshotID := make(map[int]map[string]string)
	for _, config := range configs {
		c, ok := configBySnapshotID[config.InstanceID]
		if !ok {
			c = make(map[string]string)
			configBySnapshotID[config.InstanceID] = c
		}

		c[config.Key] = config.Value
	}

	// Fetch all rows in the instances_devices table that references
	// snapshots and index them by instance ID.
	count, err = query.Count(
		ctx,
		tx,
		"instances_devices JOIN instances ON instances_devices.instance_id = instances.id",
		"instances.type = 1")
	if err != nil {
		return fmt.Errorf("Failed to count rows in instances_devices table: %w", err)
	}

	type device struct {
		ID         int
		InstanceID int
		Name       string
		Type       int
	}

	devices := make([]device, 0, count)
	sql = `
SELECT instances_devices.id, instance_id, instances_devices.name, instances_devices.type
  FROM instances_devices JOIN instances ON instances_devices.instance_id = instances.id
  WHERE instances.type = 1
`

	err = query.Scan(ctx, tx, sql, func(scan func(dest ...any) error) error {
		d := device{}
		err := scan(&d.ID, &d.InstanceID, &d.Name, &d.Type)
		if err != nil {
			return err
		}

		devices = append(devices, d)

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to fetch snapshots devices: %w", err)
	}

	devicesBySnapshotID := make(map[int]map[string]struct {
		Type   int
		Config map[string]string
	})
	for _, device := range devices {
		d, ok := devicesBySnapshotID[device.InstanceID]
		if !ok {
			d = make(map[string]struct {
				Type   int
				Config map[string]string
			})
			devicesBySnapshotID[device.InstanceID] = d
		}
		// Fetch the config for this device.
		config, err := query.SelectConfig(ctx, tx, "instances_devices_config", "instance_device_id = ?", device.ID)
		if err != nil {
			return fmt.Errorf("Failed to fetch snapshots devices config: %w", err)
		}

		d[device.Name] = struct {
			Type   int
			Config map[string]string
		}{
			Type:   device.Type,
			Config: config,
		}
	}

	// Migrate all snapshots to the new tables.
	for _, instance := range instances {
		if instance.Type == 0 {
			continue
		}

		// Figure out the instance and snapshot names.
		parts := strings.SplitN(instance.Name, internalInstance.SnapshotDelimiter, 2)
		if len(parts) != 2 {
			return fmt.Errorf("Snapshot %s has an invalid name", instance.Name)
		}

		instanceName := parts[0]
		instanceID, ok := instanceIDsByName[instanceName]
		if !ok {
			return fmt.Errorf("Found snapshot %s with no associated instance", instance.Name)
		}

		snapshotName := parts[1]

		// Insert a new row in instances_snapshots
		columns := []string{
			"instance_id",
			"name",
			"creation_date",
			"stateful",
			"description",
			"expiry_date",
		}

		id, err := query.UpsertObject(
			tx,
			"instances_snapshots",
			columns,
			[]any{
				instanceID,
				snapshotName,
				instance.CreationDate,
				instance.Stateful,
				instance.Description,
				instance.ExpiryDate,
			},
		)
		if err != nil {
			return fmt.Errorf("Failed migrate snapshot %s: %w", instance.Name, err)
		}

		// Migrate the snapshot config
		for key, value := range configBySnapshotID[instance.ID] {
			columns := []string{
				"instance_snapshot_id",
				"key",
				"value",
			}

			_, err := query.UpsertObject(
				tx,
				"instances_snapshots_config",
				columns,
				[]any{
					id,
					key,
					value,
				},
			)
			if err != nil {
				return fmt.Errorf("Failed migrate config %s/%s for snapshot %s: %w", key, value, instance.Name, err)
			}
		}

		// Migrate the snapshot devices
		for name, device := range devicesBySnapshotID[instance.ID] {
			columns := []string{
				"instance_snapshot_id",
				"name",
				"type",
			}

			deviceID, err := query.UpsertObject(
				tx,
				"instances_snapshots_devices",
				columns,
				[]any{
					id,
					name,
					device.Type,
				},
			)
			if err != nil {
				return fmt.Errorf("Failed migrate device %s for snapshot %s: %w", name, instance.Name, err)
			}

			for key, value := range device.Config {
				columns := []string{
					"instance_snapshot_device_id",
					"key",
					"value",
				}

				_, err := query.UpsertObject(
					tx,
					"instances_snapshots_devices_config",
					columns,
					[]any{
						deviceID,
						key,
						value,
					},
				)
				if err != nil {
					return fmt.Errorf("Failed migrate config %s/%s for device %s of snapshot %s: %w", key, value, name, instance.Name, err)
				}
			}
		}

		deleted, err := query.DeleteObject(tx, "instances", int64(instance.ID))
		if err != nil {
			return fmt.Errorf("Failed to delete snapshot %s: %w", instance.Name, err)
		}

		if !deleted {
			return fmt.Errorf("Expected to delete snapshot %s", instance.Name)
		}
	}

	// Make sure that no snapshot is left in the instances table.
	count, err = query.Count(ctx, tx, "instances", "type = 1")
	if err != nil {
		return fmt.Errorf("Failed to count leftover snapshot rows: %w", err)
	}

	if count != 0 {
		return fmt.Errorf("Found %d unexpected snapshots left in instances table", count)
	}

	return nil
}

// Rename all containers* tables to instances*/.
func updateFromV14(ctx context.Context, tx *sql.Tx) error {
	stmts := `
ALTER TABLE containers RENAME TO instances;
ALTER TABLE containers_backups RENAME COLUMN container_id TO instance_id;
ALTER TABLE containers_backups RENAME TO instances_backups;
ALTER TABLE containers_config RENAME COLUMN container_id TO instance_id;
ALTER TABLE containers_config RENAME TO instances_config;
DROP VIEW containers_config_ref;
CREATE VIEW instances_config_ref (project,
    node,
    name,
    key,
    value) AS
   SELECT projects.name,
    nodes.name,
    instances.name,
    instances_config.key,
    instances_config.value
     FROM instances_config
       JOIN instances ON instances.id=instances_config.instance_id
       JOIN projects ON projects.id=instances.project_id
       JOIN nodes ON nodes.id=instances.node_id;
ALTER TABLE containers_devices RENAME COLUMN container_id TO instance_id;
ALTER TABLE containers_devices RENAME TO instances_devices;
ALTER TABLE containers_devices_config RENAME COLUMN container_device_id TO instance_device_id;
ALTER TABLE containers_devices_config RENAME TO instances_devices_config;
DROP VIEW containers_devices_ref;
CREATE VIEW instances_devices_ref (project,
    node,
    name,
    device,
    type,
    key,
    value) AS
   SELECT projects.name,
    nodes.name,
    instances.name,
          instances_devices.name,
    instances_devices.type,
          coalesce(instances_devices_config.key,
    ''),
    coalesce(instances_devices_config.value,
    '')
   FROM instances_devices
     LEFT OUTER JOIN instances_devices_config ON instances_devices_config.instance_device_id=instances_devices.id
     JOIN instances ON instances.id=instances_devices.instance_id
     JOIN projects ON projects.id=instances.project_id
     JOIN nodes ON nodes.id=instances.node_id;
DROP INDEX containers_node_id_idx;
CREATE INDEX instances_node_id_idx ON instances (node_id);
ALTER TABLE containers_profiles RENAME COLUMN container_id TO instance_id;
ALTER TABLE containers_profiles RENAME TO instances_profiles;
DROP VIEW containers_profiles_ref;
CREATE VIEW instances_profiles_ref (project,
    node,
    name,
    value) AS
   SELECT projects.name,
    nodes.name,
    instances.name,
    profiles.name
     FROM instances_profiles
       JOIN instances ON instances.id=instances_profiles.instance_id
       JOIN profiles ON profiles.id=instances_profiles.profile_id
       JOIN projects ON projects.id=instances.project_id
       JOIN nodes ON nodes.id=instances.node_id
     ORDER BY instances_profiles.apply_order;
DROP INDEX containers_project_id_and_name_idx;
DROP INDEX containers_project_id_and_node_id_and_name_idx;
DROP INDEX containers_project_id_and_node_id_idx;
DROP INDEX containers_project_id_idx;
CREATE INDEX instances_project_id_and_name_idx ON instances (project_id, name);
CREATE INDEX instances_project_id_and_node_id_and_name_idx ON instances (project_id, node_id, name);
CREATE INDEX instances_project_id_and_node_id_idx ON instances (project_id, node_id);
CREATE INDEX instances_project_id_idx ON instances (project_id);
DROP VIEW profiles_used_by_ref;
CREATE VIEW profiles_used_by_ref (project,
    name,
    value) AS
  SELECT projects.name,
    profiles.name,
    printf('/1.0/containers/%s?project=%s',
    "instances".name,
    instances_projects.name)
    FROM profiles
    JOIN projects ON projects.id=profiles.project_id
    JOIN "instances_profiles"
      ON "instances_profiles".profile_id=profiles.id
    JOIN "instances"
      ON "instances".id="instances_profiles".instance_id
    JOIN projects AS instances_projects
      ON instances_projects.id="instances".project_id;
`
	_, err := tx.Exec(stmts)
	return err
}

func updateFromV13(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.Exec("ALTER TABLE containers ADD COLUMN expiry_date DATETIME;")
	return err
}

func updateFromV12(ctx context.Context, tx *sql.Tx) error {
	stmts := `
DROP VIEW profiles_used_by_ref;
CREATE VIEW profiles_used_by_ref (project,
    name,
    value) AS
  SELECT projects.name,
    profiles.name,
    printf('/1.0/containers/%s?project=%s',
    containers.name,
    containers_projects.name)
    FROM profiles
    JOIN projects ON projects.id=profiles.project_id
    JOIN containers_profiles
      ON containers_profiles.profile_id=profiles.id
    JOIN containers
      ON containers.id=containers_profiles.container_id
    JOIN projects AS containers_projects
      ON containers_projects.id=containers.project_id;
`
	_, err := tx.Exec(stmts)
	return err
}

func updateFromV11(ctx context.Context, tx *sql.Tx) error {
	// There was at least a case of dangling references to rows in the
	// containers table that don't exist anymore. So sanitize them before
	// we move forward. See #5176.
	stmts := `
DELETE FROM containers_config WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_backups WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_devices WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_devices_config WHERE container_device_id NOT IN (SELECT id FROM containers_devices);
DELETE FROM containers_profiles WHERE container_id NOT IN (SELECT id FROM containers);
DELETE FROM containers_profiles WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM images_aliases WHERE image_id NOT IN (SELECT id FROM images);
DELETE FROM images_properties WHERE image_id NOT IN (SELECT id FROM images);
DELETE FROM images_source WHERE image_id NOT IN (SELECT id FROM images);
DELETE FROM networks_config WHERE network_id NOT IN (SELECT id FROM networks);
DELETE FROM profiles_config WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices_config WHERE profile_device_id NOT IN (SELECT id FROM profiles_devices);
DELETE FROM storage_pools_config WHERE storage_pool_id NOT IN (SELECT id FROM storage_pools);
DELETE FROM storage_volumes WHERE storage_pool_id NOT IN (SELECT id FROM storage_pools);
DELETE FROM storage_volumes_config WHERE storage_volume_id NOT IN (SELECT id FROM storage_volumes);
`
	_, err := tx.Exec(stmts)
	if err != nil {
		return fmt.Errorf("Remove dangling references to containers: %w", err)
	}

	// Before doing anything save the counts of all tables, so we can later
	// check that we don't accidentally delete or add anything.
	counts1, err := query.CountAll(ctx, tx)
	if err != nil {
		return fmt.Errorf("Failed to count rows in current tables: %w", err)
	}

	// Temporarily increase the cache size and disable page spilling, to
	// avoid unnecessary writes to the WAL.
	_, err = tx.Exec("PRAGMA cache_size=100000")
	if err != nil {
		return fmt.Errorf("Increase cache size: %w", err)
	}

	_, err = tx.Exec("PRAGMA cache_spill=0")
	if err != nil {
		return fmt.Errorf("Disable spilling cache pages to disk: %w", err)
	}

	// Use a large timeout since the update might take a while, due to the
	// new indexes being created.
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	stmts = `
CREATE TABLE projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    UNIQUE (name)
);

CREATE TABLE projects_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    project_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    UNIQUE (project_id, key)
);

CREATE VIEW projects_config_ref (name, key, value) AS
   SELECT projects.name, projects_config.key, projects_config.value
     FROM projects_config
     JOIN projects ON projects.id=projects_config.project_id;

-- Insert the default project, with ID 1
INSERT INTO projects (name, description) VALUES ('default', 'Default Incus project');
INSERT INTO projects_config (project_id, key, value) VALUES (1, 'features.images', 'true');
INSERT INTO projects_config (project_id, key, value) VALUES (1, 'features.profiles', 'true');

-- Add a project_id column to all tables that need to be project-scoped.
-- The column is added without the FOREIGN KEY constraint
ALTER TABLE containers ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE images ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE images_aliases ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE profiles ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE storage_volumes ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE operations ADD COLUMN project_id INTEGER;

-- Create new versions of the above tables, this time with the FOREIGN key constraint
CREATE TABLE new_containers (
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
    project_id INTEGER NOT NULL,
    UNIQUE (project_id, name),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE new_images (
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
    project_id INTEGER NOT NULL,
    UNIQUE (project_id, fingerprint),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE new_images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (project_id, name),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE new_profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (project_id, name),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE new_storage_volumes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    description TEXT,
    snapshot INTEGER NOT NULL DEFAULT 0,
    project_id INTEGER NOT NULL,
    UNIQUE (storage_pool_id, node_id, project_id, name, type),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE new_operations (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    uuid TEXT NOT NULL,
    node_id TEXT NOT NULL,
    type INTEGER NOT NULL DEFAULT 0,
    project_id INTEGER,
    UNIQUE (uuid),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

-- Create copy version of all the tables that have direct or indirect references
-- to the tables above, which we are going to drop. The copy just have the data,
-- without FOREIGN KEY references.
CREATE TABLE containers_backups_copy (
    id INTEGER NOT NULL,
    container_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    container_only INTEGER NOT NULL default 0,
    optimized_storage INTEGER NOT NULL default 0,
    UNIQUE (container_id, name)
);
INSERT INTO containers_backups_copy SELECT * FROM containers_backups;

CREATE TABLE containers_config_copy (
    id INTEGER NOT NULL,
    container_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (container_id, key)
);
INSERT INTO containers_config_copy SELECT * FROM containers_config;

CREATE TABLE containers_devices_copy (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (container_id, name)
);
INSERT INTO containers_devices_copy SELECT * FROM containers_devices;

CREATE TABLE containers_devices_config_copy (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (container_device_id, key)
);
INSERT INTO containers_devices_config_copy SELECT * FROM containers_devices_config;

CREATE TABLE containers_profiles_copy (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    apply_order INTEGER NOT NULL default 0,
    UNIQUE (container_id, profile_id)
);
INSERT INTO containers_profiles_copy SELECT * FROM containers_profiles;

CREATE TABLE images_aliases_copy (
    id INTEGER NOT NULL,
    name TEXT NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (name)
);
INSERT INTO images_aliases_copy SELECT * FROM images_aliases;

CREATE TABLE images_nodes_copy (
    id INTEGER NOT NULL,
    image_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (image_id, node_id)
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
INSERT INTO images_nodes_copy SELECT * FROM images_nodes;

CREATE TABLE images_properties_copy (
    id INTEGER NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT
);
INSERT INTO images_properties_copy SELECT * FROM images_properties;

CREATE TABLE images_source_copy (
    id INTEGER NOT NULL,
    image_id INTEGER NOT NULL,
    server TEXT NOT NULL,
    protocol INTEGER NOT NULL,
    certificate TEXT NOT NULL,
    alias TEXT NOT NULL
);
INSERT INTO images_source_copy SELECT * FROM images_source;

CREATE TABLE profiles_config_copy (
    id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (profile_id, key)
);
INSERT INTO profiles_config_copy SELECT * FROM profiles_config;

CREATE TABLE profiles_devices_copy (
    id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type INTEGER NOT NULL default 0,
    UNIQUE (profile_id, name)
);
INSERT INTO profiles_devices_copy SELECT * FROM profiles_devices;

CREATE TABLE profiles_devices_config_copy (
    id INTEGER NOT NULL,
    profile_device_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (profile_device_id, key)
);
INSERT INTO profiles_devices_config_copy SELECT * FROM profiles_devices_config;

CREATE TABLE storage_volumes_config_copy (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (storage_volume_id, key)
);
INSERT INTO storage_volumes_config_copy SELECT * FROM storage_volumes_config;

-- Copy existing data into the new tables with the project_id reference
INSERT INTO new_containers SELECT * FROM containers;
INSERT INTO new_images SELECT * FROM images;
INSERT INTO new_profiles SELECT * FROM profiles;
INSERT INTO new_storage_volumes SELECT * FROM storage_volumes;
INSERT INTO new_operations SELECT * FROM operations;

-- Drop the old table and rename the new ones. This will trigger cascading
-- deletes on all tables that have direct or indirect references to the old
-- table, but we have a copy of them that we will use for restoring.
DROP TABLE containers;
ALTER TABLE new_containers RENAME TO containers;

DROP TABLE images;
ALTER TABLE new_images RENAME TO images;

DROP TABLE profiles;
ALTER TABLE new_profiles RENAME TO profiles;

DROP TABLE storage_volumes;
ALTER TABLE new_storage_volumes RENAME TO storage_volumes;

INSERT INTO new_images_aliases SELECT * FROM images_aliases_copy;

DROP TABLE images_aliases;
DROP TABLE images_aliases_copy;
ALTER TABLE new_images_aliases RENAME TO images_aliases;

DROP TABLE operations;
ALTER TABLE new_operations RENAME TO operations;

-- Restore the content of the tables with direct or indirect references.
INSERT INTO containers_backups SELECT * FROM containers_backups_copy;
INSERT INTO containers_config SELECT * FROM containers_config_copy;
INSERT INTO containers_devices SELECT * FROM containers_devices_copy;
INSERT INTO containers_devices_config SELECT * FROM containers_devices_config_copy;
INSERT INTO containers_profiles SELECT * FROM containers_profiles_copy;
INSERT INTO images_nodes SELECT * FROM images_nodes_copy;
INSERT INTO images_properties SELECT * FROM images_properties_copy;
INSERT INTO images_source SELECT * FROM images_source_copy;
INSERT INTO profiles_config SELECT * FROM profiles_config_copy;
INSERT INTO profiles_devices SELECT * FROM profiles_devices_copy;
INSERT INTO profiles_devices_config SELECT * FROM profiles_devices_config_copy;
INSERT INTO storage_volumes_config SELECT * FROM storage_volumes_config_copy;

-- Drop the copies.
DROP TABLE containers_backups_copy;
DROP TABLE containers_config_copy;
DROP TABLE containers_devices_copy;
DROP TABLE containers_devices_config_copy;
DROP TABLE containers_profiles_copy;
DROP TABLE images_nodes_copy;
DROP TABLE images_properties_copy;
DROP TABLE images_source_copy;
DROP TABLE profiles_config_copy;
DROP TABLE profiles_devices_copy;
DROP TABLE profiles_devices_config_copy;
DROP TABLE storage_volumes_config_copy;

-- Create some indexes to speed up queries filtered by project ID and node ID
CREATE INDEX containers_node_id_idx ON containers (node_id);
CREATE INDEX containers_project_id_idx ON containers (project_id);
CREATE INDEX containers_project_id_and_name_idx ON containers (project_id, name);
CREATE INDEX containers_project_id_and_node_id_idx ON containers (project_id, node_id);
CREATE INDEX containers_project_id_and_node_id_and_name_idx ON containers (project_id, node_id, name);
CREATE INDEX images_project_id_idx ON images (project_id);
CREATE INDEX images_aliases_project_id_idx ON images_aliases (project_id);
CREATE INDEX profiles_project_id_idx ON profiles (project_id);
`
	_, err = tx.ExecContext(ctx, stmts)
	if err != nil {
		return fmt.Errorf("Failed to add project_id column: %w", err)
	}

	// Create a view to easily query all resources using a certain project
	stmt := fmt.Sprintf(`
CREATE VIEW projects_used_by_ref (name, value) AS
  SELECT projects.name, printf('%s', containers.name, projects.name)
    FROM containers JOIN projects ON project_id=projects.id UNION
  SELECT projects.name, printf('%s', images.fingerprint)
    FROM images JOIN projects ON project_id=projects.id UNION
  SELECT projects.name, printf('%s', profiles.name, projects.name)
    FROM profiles JOIN projects ON project_id=projects.id
`, EntityURIs[TypeContainer], EntityURIs[TypeImage], EntityURIs[TypeProfile])
	_, err = tx.Exec(stmt)
	if err != nil {
		return fmt.Errorf("Failed to create projects_used_by_ref view: %w", err)
	}

	// Create a view to easily query all profiles used by a certain container
	stmt = `
CREATE VIEW containers_profiles_ref (project, node, name, value) AS
   SELECT projects.name, nodes.name, containers.name, profiles.name
     FROM containers_profiles
       JOIN containers ON containers.id=containers_profiles.container_id
       JOIN profiles ON profiles.id=containers_profiles.profile_id
       JOIN projects ON projects.id=containers.project_id
       JOIN nodes ON nodes.id=containers.node_id
     ORDER BY containers_profiles.apply_order
`
	_, err = tx.Exec(stmt)
	if err != nil {
		return fmt.Errorf("Failed to containers_profiles_ref view: %w", err)
	}

	// Create a view to easily query the config of a certain container.
	stmt = `
CREATE VIEW containers_config_ref (project, node, name, key, value) AS
   SELECT projects.name, nodes.name, containers.name, containers_config.key, containers_config.value
     FROM containers_config
       JOIN containers ON containers.id=containers_config.container_id
       JOIN projects ON projects.id=containers.project_id
       JOIN nodes ON nodes.id=containers.node_id
`
	_, err = tx.Exec(stmt)
	if err != nil {
		return fmt.Errorf("Failed to containers_config_ref view: %w", err)
	}

	// Create a view to easily query the devices of a certain container.
	stmt = `
CREATE VIEW containers_devices_ref (project, node, name, device, type, key, value) AS
   SELECT projects.name, nodes.name, containers.name,
          containers_devices.name, containers_devices.type,
          coalesce(containers_devices_config.key, ''), coalesce(containers_devices_config.value, '')
   FROM containers_devices
     LEFT OUTER JOIN containers_devices_config ON containers_devices_config.container_device_id=containers_devices.id
     JOIN containers ON containers.id=containers_devices.container_id
     JOIN projects ON projects.id=containers.project_id
     JOIN nodes ON nodes.id=containers.node_id
`
	_, err = tx.Exec(stmt)
	if err != nil {
		return fmt.Errorf("Failed to containers_devices_ref view: %w", err)
	}

	// Create a view to easily query the config of a certain profile.
	stmt = `
CREATE VIEW profiles_config_ref (project, name, key, value) AS
   SELECT projects.name, profiles.name, profiles_config.key, profiles_config.value
     FROM profiles_config
     JOIN profiles ON profiles.id=profiles_config.profile_id
     JOIN projects ON projects.id=profiles.project_id
`
	_, err = tx.Exec(stmt)
	if err != nil {
		return fmt.Errorf("Failed to profiles_config_ref view: %w", err)
	}

	// Create a view to easily query the devices of a certain profile.
	stmt = `
CREATE VIEW profiles_devices_ref (project, name, device, type, key, value) AS
   SELECT projects.name, profiles.name,
          profiles_devices.name, profiles_devices.type,
          coalesce(profiles_devices_config.key, ''), coalesce(profiles_devices_config.value, '')
   FROM profiles_devices
     LEFT OUTER JOIN profiles_devices_config ON profiles_devices_config.profile_device_id=profiles_devices.id
     JOIN profiles ON profiles.id=profiles_devices.profile_id
     JOIN projects ON projects.id=profiles.project_id
`
	_, err = tx.Exec(stmt)
	if err != nil {
		return fmt.Errorf("Failed to profiles_devices_ref view: %w", err)
	}

	// Create a view to easily query all resources using a certain profile
	stmt = fmt.Sprintf(`
CREATE VIEW profiles_used_by_ref (project, name, value) AS
  SELECT projects.name, profiles.name, printf('%s', containers.name, projects.name)
    FROM profiles
    JOIN projects ON projects.id=profiles.project_id
    JOIN containers_profiles
      ON containers_profiles.profile_id=profiles.id
    JOIN containers
      ON containers.id=containers_profiles.container_id
`, EntityURIs[TypeContainer])
	_, err = tx.Exec(stmt)
	if err != nil {
		return fmt.Errorf("Failed to create profiles_used_by_ref view: %w", err)
	}

	// Check that the count of all rows in the database is unchanged
	// (i.e. we didn't accidentally delete or add anything).
	counts2, err := query.CountAll(ctx, tx)
	if err != nil {
		return fmt.Errorf("Failed to count rows in updated tables: %w", err)
	}

	delete(counts2, "projects")

	for table, count1 := range counts1 {
		if table == "sqlite_sequence" {
			continue
		}

		count2 := counts2[table]
		if count1 != count2 {
			return fmt.Errorf("Row count mismatch in table '%s': %d vs %d", table, count1, count2)
		}
	}

	// Restore default cache values.
	_, err = tx.Exec("PRAGMA cache_size=2000")
	if err != nil {
		return fmt.Errorf("Increase cache size: %w", err)
	}

	_, err = tx.Exec("PRAGMA cache_spill=1")
	if err != nil {
		return fmt.Errorf("Disable spilling cache pages to disk: %w", err)
	}

	return err
}

func updateFromV10(ctx context.Context, tx *sql.Tx) error {
	stmt := `
ALTER TABLE storage_volumes ADD COLUMN snapshot INTEGER NOT NULL DEFAULT 0;
UPDATE storage_volumes SET snapshot = 0;
`
	_, err := tx.Exec(stmt)
	return err
}

// Add a new 'type' column to the operations table.
func updateFromV9(ctx context.Context, tx *sql.Tx) error {
	stmts := `
	ALTER TABLE operations ADD COLUMN type INTEGER NOT NULL DEFAULT 0;
	UPDATE operations SET type = 0;
	`
	_, err := tx.Exec(stmts)
	return err
}

// The lvm.thinpool_name and lvm.vg_name config keys are node-specific and need
// to be linked to nodes.
func updateFromV8(ctx context.Context, tx *sql.Tx) error {
	// Moved to patchLvmNodeSpecificConfigKeys, since there's no schema
	// change. That makes it easier to backport.
	return nil
}

func updateFromV7(ctx context.Context, tx *sql.Tx) error {
	stmts := `
CREATE TABLE containers_backups (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    container_only INTEGER NOT NULL default 0,
    optimized_storage INTEGER NOT NULL default 0,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, name)
);
`
	_, err := tx.Exec(stmts)
	return err
}

// The zfs.pool_name config key is node-specific, and needs to be linked to
// nodes.
func updateFromV6(ctx context.Context, tx *sql.Tx) error {
	// Fetch the IDs of all existing nodes.
	nodeIDs, err := query.SelectIntegers(ctx, tx, "SELECT id FROM nodes")
	if err != nil {
		return fmt.Errorf("failed to get IDs of current nodes: %w", err)
	}

	// Fetch the IDs of all existing zfs pools.
	poolIDs, err := query.SelectIntegers(ctx, tx, `SELECT id FROM storage_pools WHERE driver='zfs'`)
	if err != nil {
		return fmt.Errorf("failed to get IDs of current zfs pools: %w", err)
	}

	for _, poolID := range poolIDs {
		// Fetch the config for this zfs pool and check if it has the zfs.pool_name key
		config, err := query.SelectConfig(ctx, tx, "storage_pools_config", "storage_pool_id=? AND node_id IS NULL", poolID)
		if err != nil {
			return fmt.Errorf("failed to fetch of zfs pool config: %w", err)
		}

		poolName, ok := config["zfs.pool_name"]
		if !ok {
			continue // This zfs storage pool does not have a zfs.pool_name config
		}

		// Delete the current zfs.pool_name key
		_, err = tx.Exec(`
DELETE FROM storage_pools_config WHERE key='zfs.pool_name' AND storage_pool_id=? AND node_id IS NULL
`, poolID)
		if err != nil {
			return fmt.Errorf("failed to delete zfs.pool_name config: %w", err)
		}

		// Add zfs.pool_name config entry for each node
		for _, nodeID := range nodeIDs {
			_, err := tx.Exec(`
INSERT INTO storage_pools_config(storage_pool_id, node_id, key, value)
  VALUES(?, ?, 'zfs.pool_name', ?)
`, poolID, nodeID, poolName)
			if err != nil {
				return fmt.Errorf("failed to create zfs.pool_name node config: %w", err)
			}
		}
	}

	return nil
}

// For ceph volumes, add node-specific rows for all existing nodes, since any
// node is able to access those volumes.
func updateFromV5(ctx context.Context, tx *sql.Tx) error {
	// Fetch the IDs of all existing nodes.
	nodeIDs, err := query.SelectIntegers(ctx, tx, "SELECT id FROM nodes")
	if err != nil {
		return fmt.Errorf("failed to get IDs of current nodes: %w", err)
	}

	// Fetch the IDs of all existing ceph volumes.
	volumeIDs, err := query.SelectIntegers(ctx, tx, `
SELECT storage_volumes.id FROM storage_volumes
    JOIN storage_pools ON storage_volumes.storage_pool_id=storage_pools.id
    WHERE storage_pools.driver='ceph'
`)
	if err != nil {
		return fmt.Errorf("failed to get IDs of current ceph volumes: %w", err)
	}

	// Fetch all existing ceph volumes.
	type volume struct {
		ID            int
		Name          string
		StoragePoolID int
		NodeID        int
		Type          int
		Description   string
	}

	volumes := make([]volume, 0, len(volumeIDs))
	sql := `
SELECT
    storage_volumes.id,
    storage_volumes.name,
    storage_volumes.storage_pool_id,
    storage_volumes.node_id,
    storage_volumes.type,
    storage_volumes.description
FROM storage_volumes
    JOIN storage_pools ON storage_volumes.storage_pool_id=storage_pools.id
    WHERE storage_pools.driver='ceph'
`

	err = query.Scan(ctx, tx, sql, func(scan func(dest ...any) error) error {
		vol := volume{}
		err := scan(&vol.ID, &vol.Name, &vol.StoragePoolID, &vol.NodeID, &vol.Type, &vol.Description)
		if err != nil {
			return err
		}

		volumes = append(volumes, vol)

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to fetch current volumes: %w", err)
	}

	// Duplicate each volume row across all nodes, and keep track of the
	// new volume IDs that we've inserted.
	created := make(map[int][]int64, 0) // Existing volume ID to new volumes IDs.
	columns := []string{"name", "storage_pool_id", "node_id", "type", "description"}
	for _, volume := range volumes {
		for _, nodeID := range nodeIDs {
			if volume.NodeID == nodeID {
				// This node already has the volume row
				continue
			}

			values := []any{
				volume.Name,
				volume.StoragePoolID,
				nodeID,
				volume.Type,
				volume.Description,
			}

			id, err := query.UpsertObject(tx, "storage_volumes", columns, values)
			if err != nil {
				return fmt.Errorf("failed to insert new volume: %w", err)
			}

			_, ok := created[volume.ID]
			if !ok {
				created[volume.ID] = make([]int64, 0)
			}

			created[volume.ID] = append(created[volume.ID], id)
		}
	}

	// Duplicate each volume config row across all nodes.
	for id, newIDs := range created {
		config, err := query.SelectConfig(ctx, tx, "storage_volumes_config", "storage_volume_id=?", id)
		if err != nil {
			return fmt.Errorf("failed to fetch volume config: %w", err)
		}

		for _, newID := range newIDs {
			for key, value := range config {
				_, err := tx.Exec(`
INSERT INTO storage_volumes_config(storage_volume_id, key, value) VALUES(?, ?, ?)
`, newID, key, value)
				if err != nil {
					return fmt.Errorf("failed to insert new volume config: %w", err)
				}
			}
		}
	}

	return nil
}

func updateFromV4(ctx context.Context, tx *sql.Tx) error {
	stmt := "UPDATE networks SET state = 1"
	_, err := tx.Exec(stmt)
	return err
}

func updateFromV3(ctx context.Context, tx *sql.Tx) error {
	stmt := `
CREATE TABLE storage_pools_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (storage_pool_id, node_id),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
ALTER TABLE storage_pools ADD COLUMN state INTEGER NOT NULL DEFAULT 0;
UPDATE storage_pools SET state = 1;
`
	_, err := tx.Exec(stmt)
	return err
}

func updateFromV2(ctx context.Context, tx *sql.Tx) error {
	stmt := `
CREATE TABLE operations (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    uuid TEXT NOT NULL,
    node_id TEXT NOT NULL,
    UNIQUE (uuid),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
`
	_, err := tx.Exec(stmt)
	return err
}

func updateFromV1(ctx context.Context, tx *sql.Tx) error {
	stmt := `
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
CREATE TABLE images_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (image_id, node_id),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE TABLE networks (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    state INTEGER NOT NULL DEFAULT 0,
    UNIQUE (name)
);
CREATE TABLE networks_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    network_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (network_id, node_id),
    FOREIGN KEY (network_id) REFERENCES networks (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
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
`
	_, err := tx.Exec(stmt)
	return err
}

func updateFromV0(ctx context.Context, tx *sql.Tx) error {
	// v0..v1 the dawn of clustering
	stmt := `
CREATE TABLE nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
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
`
	_, err := tx.Exec(stmt)
	return err
}
