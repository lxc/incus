//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

// LoadPreClusteringData loads all the data that before the introduction of
// LXD clustering used to live in the node-level database.
//
// This is used for performing a one-off data migration when a LXD instance is
// upgraded from a version without clustering to a version that supports
// clustering, since in those version all data lives in the cluster database
// (regardless of whether clustering is actually on or off).
func LoadPreClusteringData(ctx context.Context, tx *sql.Tx) (*Dump, error) {
	// Sanitize broken foreign key references that might be around from the
	// time where we didn't enforce foreign key constraints.
	_, err := tx.Exec(`
DELETE FROM containers_config WHERE container_id NOT IN (SELECT id FROM containers);
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
`)
	if err != nil {
		return nil, fmt.Errorf("failed to sanitize broken foreign key references: %w", err)
	}

	// Dump all tables.
	dump := &Dump{
		Schema: map[string][]string{},
		Data:   map[string][][]any{},
	}

	for _, table := range preClusteringTables {
		logger.Debugf("Loading data from table %s", table)
		data := [][]any{}
		stmt := fmt.Sprintf("SELECT * FROM %s", table)

		rows, err := tx.QueryContext(ctx, stmt)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch rows from %s: %w", table, err)
		}

		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("failed to get columns of %s: %w", table, err)
		}

		dump.Schema[table] = columns

		for rows.Next() {
			values := make([]any, len(columns))
			row := make([]any, len(columns))
			for i := range values {
				row[i] = &values[i]
			}

			err := rows.Scan(row...)
			if err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("failed to scan row from %s: %w", table, err)
			}

			data = append(data, values)
		}

		err = rows.Err()
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("error while fetching rows from %s: %w", table, err)
		}

		_ = rows.Close()

		dump.Data[table] = data
	}

	return dump, nil
}

// List of tables existing before clustering that had no project_id column and
// that now require it.
var preClusteringTablesRequiringProjectID = []string{
	"containers",
	"images",
	"images_aliases",
	"profiles",
	"storage_volumes",
	"operations",
}

// ImportPreClusteringData imports the data loaded with LoadPreClusteringData.
func importPreClusteringData(tx *sql.Tx, dump *Dump) error {
	// Create version 14 of the cluster database schema.
	_, err := tx.Exec(clusterSchemaVersion14)
	if err != nil {
		return fmt.Errorf("Create cluster database schema version 14: %w", err)
	}

	// Insert an entry for node 1.
	var stmt strings.Builder
	stmt.WriteString(`INSERT INTO nodes(id, name, address, schema, api_extensions) VALUES(1, 'none', '0.0.0.0', 14, 1);`)

	// Default project
	stmt.WriteString(`INSERT INTO projects (name, description) VALUES ('default', 'Default LXD project');`)

	// Enable all features for default project.
	for featureName := range cluster.ProjectFeatures {
		stmt.WriteString(fmt.Sprintf(`INSERT INTO projects_config (project_id, key, value) VALUES (1, '%s', 'true');`, featureName))
	}

	_, err = tx.Exec(stmt.String())
	if err != nil {
		return err
	}

	for _, table := range preClusteringTables {
		logger.Debugf("Migrating data for table %s", table)

		for i, row := range dump.Data[table] {
			for i, element := range row {
				// Convert []byte columns to string. This is safe to do since
				// the pre-clustering schema only had TEXT fields and no BLOB.
				bytes, ok := element.([]byte)
				if ok {
					row[i] = string(bytes)
				}
			}
			columns := dump.Schema[table]

			nullNodeID := false // Whether node-related rows should have a NULL node ID
			appendNodeID := func() {
				columns = append(columns, "node_id")
				if nullNodeID {
					row = append(row, nil)
				} else {
					row = append(row, int64(1))
				}
			}

			switch table {
			case "config":
				// Don't migrate the core.https_address and maas.machine config keys,
				// which is node-specific and must remain in the node
				// database.
				keys := []string{"core.https_address", "maas.machine"}
				skip := false
				for i, column := range columns {
					value, ok := row[i].(string)
					if !ok {
						continue
					}

					if column == "key" && shared.StringInSlice(value, keys) {
						skip = true
					}
				}
				if skip {
					continue
				}

			case "containers":
				appendNodeID()
			case "networks_config":
				// The keys listed in NodeSpecificNetworkConfig
				// are the only ones which are not global to the
				// cluster, so all other keys will have a NULL
				// node_id.
				index := 0
				for i, column := range columns {
					if column == "key" {
						index = i
						break
					}
				}
				key := row[index].(string)
				if !shared.StringInSlice(key, NodeSpecificNetworkConfig) {
					nullNodeID = true
					break
				}

				appendNodeID()
			case "storage_pools_config":
				// The keys listed in NodeSpecificStorageConfig
				// are the only ones which are not global to the
				// cluster, so all other keys will have a NULL
				// node_id.
				index := 0
				for i, column := range columns {
					if column == "key" {
						index = i
						break
					}
				}
				key := row[index].(string)
				if !shared.StringInSlice(key, NodeSpecificStorageConfig) {
					nullNodeID = true
					break
				}

				appendNodeID()
			case "networks":
				fallthrough
			case "storage_pools":
				columns = append(columns, "state")
				row = append(row, StoragePoolCreated)
			case "storage_volumes":
				appendNodeID()
			}

			if shared.StringInSlice(table, preClusteringTablesRequiringProjectID) {
				// These tables have a project_id reference in the new schema.
				columns = append(columns, "project_id")
				row = append(row, 1) // Reference the default project.
			}

			stmt := fmt.Sprintf("INSERT INTO %s(%s)", table, strings.Join(columns, ", "))
			stmt += fmt.Sprintf(" VALUES %s", query.Params(len(columns)))
			result, err := tx.Exec(stmt, row...)
			if err != nil {
				return fmt.Errorf("failed to insert row %d into %s: %w", i, table, err)
			}

			n, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("no result count for row %d of %s: %w", i, table, err)
			}

			if n != 1 {
				return fmt.Errorf("could not insert %d int %s", i, table)
			}

			// Also insert the image ID to node ID association.
			if shared.StringInSlice(table, []string{"images", "networks", "storage_pools"}) {
				entity := table[:len(table)-1]
				err := importNodeAssociation(entity, columns, row, tx)
				if err != nil {
					return fmt.Errorf("Failed to import node associations")
				}
			}
		}
	}

	return nil
}

// Insert a row in one of the nodes association tables (storage_pools_nodes,
// networks_nodes, images_nodes).
func importNodeAssociation(entity string, columns []string, row []any, tx *sql.Tx) error {
	stmt := fmt.Sprintf(
		"INSERT INTO %ss_nodes(%s_id, node_id) VALUES(?, 1)", entity, entity)
	var id int64
	for i, column := range columns {
		if column == "id" {
			id = row[i].(int64)
			break
		}
	}
	if id == 0 {
		return fmt.Errorf("entity %s has invalid ID", entity)
	}

	_, err := tx.Exec(stmt, id)
	if err != nil {
		return fmt.Errorf("failed to associate %s to node: %w", entity, err)
	}

	return nil
}

// Dump is a dump of all the user data in the local db prior the migration to
// the cluster db.
type Dump struct {
	// Map table names to the names or their columns.
	Schema map[string][]string

	// Map a table name to all the rows it contains. Each row is a slice
	// of interfaces.
	Data map[string][][]any
}

var preClusteringTables = []string{
	"certificates",
	"config",
	"profiles",
	"profiles_config",
	"profiles_devices",
	"profiles_devices_config",
	"containers",
	"containers_config",
	"containers_devices",
	"containers_devices_config",
	"containers_profiles",
	"images",
	"images_aliases",
	"images_properties",
	"images_source",
	"networks",
	"networks_config",
	"storage_pools",
	"storage_pools_config",
	"storage_volumes",
	"storage_volumes_config",
}

// Copy of version 14 of the clustering schema. The data migration code from
// LXD 2.0 is meant to be run against this schema. Further schema changes are
// applied using the normal schema update logic.
var clusterSchemaVersion14 = `
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
CREATE TABLE "containers" (
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
    expiry_date DATETIME,
    UNIQUE (project_id, name),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
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
CREATE TABLE containers_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    FOREIGN KEY (container_id) REFERENCES containers (id) ON DELETE CASCADE,
    UNIQUE (container_id, key)
);
CREATE VIEW containers_config_ref (project,
    node,
    name,
    key,
    value) AS
   SELECT projects.name,
    nodes.name,
    containers.name,
    containers_config.key,
    containers_config.value
     FROM containers_config
       JOIN containers ON containers.id=containers_config.container_id
       JOIN projects ON projects.id=containers.project_id
       JOIN nodes ON nodes.id=containers.node_id;
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
CREATE VIEW containers_devices_ref (project,
    node,
    name,
    device,
    type,
    key,
    value) AS
   SELECT projects.name,
    nodes.name,
    containers.name,
          containers_devices.name,
    containers_devices.type,
          coalesce(containers_devices_config.key,
    ''),
    coalesce(containers_devices_config.value,
    '')
   FROM containers_devices
     LEFT OUTER JOIN containers_devices_config ON containers_devices_config.container_device_id=containers_devices.id
     JOIN containers ON containers.id=containers_devices.container_id
     JOIN projects ON projects.id=containers.project_id
     JOIN nodes ON nodes.id=containers.node_id;
CREATE INDEX containers_node_id_idx ON containers (node_id);
CREATE TABLE containers_profiles (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    profile_id INTEGER NOT NULL,
    apply_order INTEGER NOT NULL default 0,
    UNIQUE (container_id, profile_id),
    FOREIGN KEY (container_id) REFERENCES containers(id) ON DELETE CASCADE,
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
CREATE VIEW containers_profiles_ref (project,
    node,
    name,
    value) AS
   SELECT projects.name,
    nodes.name,
    containers.name,
    profiles.name
     FROM containers_profiles
       JOIN containers ON containers.id=containers_profiles.container_id
       JOIN profiles ON profiles.id=containers_profiles.profile_id
       JOIN projects ON projects.id=containers.project_id
       JOIN nodes ON nodes.id=containers.node_id
     ORDER BY containers_profiles.apply_order;
CREATE INDEX containers_project_id_and_name_idx ON containers (project_id,
    name);
CREATE INDEX containers_project_id_and_node_id_and_name_idx ON containers (project_id,
    node_id,
    name);
CREATE INDEX containers_project_id_and_node_id_idx ON containers (project_id,
    node_id);
CREATE INDEX containers_project_id_idx ON containers (project_id);
CREATE TABLE "images" (
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
CREATE TABLE "images_aliases" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    image_id INTEGER NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (project_id, name),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
CREATE INDEX images_aliases_project_id_idx ON images_aliases (project_id);
CREATE TABLE images_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE (image_id, node_id),
    FOREIGN KEY (image_id) REFERENCES images (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE
);
CREATE INDEX images_project_id_idx ON images (project_id);
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
CREATE TABLE "operations" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    uuid TEXT NOT NULL,
    node_id TEXT NOT NULL,
    type INTEGER NOT NULL DEFAULT 0,
    project_id INTEGER,
    UNIQUE (uuid),
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
CREATE TABLE "profiles" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    project_id INTEGER NOT NULL,
    UNIQUE (project_id, name),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
CREATE TABLE profiles_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    profile_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (profile_id, key),
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
CREATE VIEW profiles_config_ref (project,
    name,
    key,
    value) AS
   SELECT projects.name,
    profiles.name,
    profiles_config.key,
    profiles_config.value
     FROM profiles_config
     JOIN profiles ON profiles.id=profiles_config.profile_id
     JOIN projects ON projects.id=profiles.project_id;
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
CREATE VIEW profiles_devices_ref (project,
    name,
    device,
    type,
    key,
    value) AS
   SELECT projects.name,
    profiles.name,
          profiles_devices.name,
    profiles_devices.type,
          coalesce(profiles_devices_config.key,
    ''),
    coalesce(profiles_devices_config.value,
    '')
   FROM profiles_devices
     LEFT OUTER JOIN profiles_devices_config ON profiles_devices_config.profile_device_id=profiles_devices.id
     JOIN profiles ON profiles.id=profiles_devices.profile_id
     JOIN projects ON projects.id=profiles.project_id;
CREATE INDEX profiles_project_id_idx ON profiles (project_id);
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
CREATE VIEW projects_config_ref (name,
    key,
    value) AS
   SELECT projects.name,
    projects_config.key,
    projects_config.value
     FROM projects_config
     JOIN projects ON projects.id=projects_config.project_id;
CREATE VIEW projects_used_by_ref (name,
    value) AS
  SELECT projects.name,
    printf('/1.0/containers/%s?project=%s',
    containers.name,
    projects.name)
    FROM containers JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/images/%s',
    images.fingerprint)
    FROM images JOIN projects ON project_id=projects.id UNION
  SELECT projects.name,
    printf('/1.0/profiles/%s?project=%s',
    profiles.name,
    projects.name)
    FROM profiles JOIN projects ON project_id=projects.id;
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
CREATE TABLE "storage_volumes" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    storage_pool_id INTEGER NOT NULL,
    node_id INTEGER,
    type INTEGER NOT NULL,
    description TEXT,
    snapshot INTEGER NOT NULL DEFAULT 0,
    project_id INTEGER NOT NULL,
    UNIQUE (storage_pool_id, node_id, project_id, name, type),
    FOREIGN KEY (storage_pool_id) REFERENCES storage_pools (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
CREATE TABLE storage_volumes_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    storage_volume_id INTEGER NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    UNIQUE (storage_volume_id, key),
    FOREIGN KEY (storage_volume_id) REFERENCES storage_volumes (id) ON DELETE CASCADE
);
CREATE TABLE schema (
    id         INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version    INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);
INSERT INTO schema (version, updated_at) VALUES (14, strftime("%s"))
`
