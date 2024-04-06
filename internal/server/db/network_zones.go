//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db/query"
	"github.com/lxc/incus/v6/shared/api"
)

// GetNetworkZones returns the names of existing Network zones mapped to project name.
func (c *ClusterTx) GetNetworkZones(ctx context.Context) (map[string]string, error) {
	q := `SELECT networks_zones.name, projects.name AS project_name FROM networks_zones
		JOIN projects ON projects.id = networks_zones.project_id
		ORDER BY networks_zones.id
	`

	var err error
	zoneProjects := make(map[string]string)

	err = query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var zoneName string
		var projectName string

		err := scan(&zoneName, &projectName)
		if err != nil {
			return err
		}

		zoneProjects[zoneName] = projectName

		return nil
	})
	if err != nil {
		return nil, err
	}

	return zoneProjects, nil
}

// GetNetworkZonesByProject returns the names of existing Network zones.
func (c *ClusterTx) GetNetworkZonesByProject(ctx context.Context, project string) ([]string, error) {
	q := `SELECT name FROM networks_zones
		WHERE project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1)
		ORDER BY id
	`

	var zoneNames []string

	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var zoneName string

		err := scan(&zoneName)
		if err != nil {
			return err
		}

		zoneNames = append(zoneNames, zoneName)

		return nil
	}, project)
	if err != nil {
		return nil, err
	}

	return zoneNames, nil
}

// GetNetworkZoneKeys returns a map of key names to keys.
func (c *ClusterTx) GetNetworkZoneKeys(ctx context.Context) (map[string]string, error) {
	q := `SELECT networks_zones.name, networks_zones_config.key, networks_zones_config.value
		FROM networks_zones
		JOIN networks_zones_config ON networks_zones_config.network_zone_id=networks_zones.id
		WHERE networks_zones_config.key LIKE 'peers.%.key'
	`

	secrets := map[string]string{}

	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var name string
		var peer string
		var secret string

		err := scan(&name, &peer, &secret)
		if err != nil {
			return err
		}

		fields := strings.SplitN(peer, ".", 3)
		if len(fields) != 3 {
			// Skip invalid values.
			return nil
		}

		// Format as a valid TSIG secret (encode domain name, key name and make valid FQDN).
		secrets[fmt.Sprintf("%s_%s.", name, fields[1])] = secret

		return nil
	})
	if err != nil {
		return nil, err
	}

	return secrets, nil
}

// GetNetworkZone returns the Network zone with the given name.
func (c *ClusterTx) GetNetworkZone(ctx context.Context, name string) (int64, string, *api.NetworkZone, error) {
	var id int64 = int64(-1)

	zone := api.NetworkZone{
		Name: name,
	}

	q := `
		SELECT networks_zones.id, projects.name, networks_zones.description
		FROM networks_zones
		JOIN projects ON projects.id=networks_zones.project_id
		WHERE networks_zones.name=?
		LIMIT 1
	`

	var projectName string

	err := c.tx.QueryRowContext(ctx, q, name).Scan(&id, &projectName, &zone.Description)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, "", nil, api.StatusErrorf(http.StatusNotFound, "Network zone not found")
		}

		return -1, "", nil, err
	}

	err = networkZoneConfig(ctx, c, id, &zone)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, "", nil, api.StatusErrorf(http.StatusNotFound, "Network zone not found")
		}

		return -1, "", nil, fmt.Errorf("Failed loading config: %w", err)
	}

	return id, projectName, &zone, nil
}

// GetNetworkZoneByProject returns the Network zone with the given name in the given project.
func (c *ClusterTx) GetNetworkZoneByProject(ctx context.Context, projectName string, name string) (int64, *api.NetworkZone, error) {
	var id int64 = int64(-1)

	zone := api.NetworkZone{
		Name: name,
	}

	q := `
		SELECT id, description
		FROM networks_zones
		WHERE project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1) AND name=?
		LIMIT 1
	`

	err := c.tx.QueryRowContext(ctx, q, projectName, name).Scan(&id, &zone.Description)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, api.StatusErrorf(http.StatusNotFound, "Network zone not found")
		}

		return -1, nil, err
	}

	err = networkZoneConfig(ctx, c, id, &zone)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, api.StatusErrorf(http.StatusNotFound, "Network zone not found")
		}

		return -1, nil, fmt.Errorf("Failed loading config: %w", err)
	}

	return id, &zone, nil
}

// networkZoneConfig populates the config map of the Network zone with the given ID.
func networkZoneConfig(ctx context.Context, tx *ClusterTx, id int64, zone *api.NetworkZone) error {
	q := `
		SELECT key, value
		FROM networks_zones_config
		WHERE network_zone_id=?
	`

	zone.Config = make(map[string]string)
	return query.Scan(ctx, tx.Tx(), q, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := zone.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for network zone ID %d", key, id)
		}

		zone.Config[key] = value

		return nil
	}, id)
}

// CreateNetworkZone creates a new Network zone.
func (c *ClusterTx) CreateNetworkZone(ctx context.Context, projectName string, info *api.NetworkZonesPost) (int64, error) {
	// Insert a new Network zone record.
	result, err := c.tx.ExecContext(ctx, `
			INSERT INTO networks_zones (project_id, name, description)
			VALUES ((SELECT id FROM projects WHERE name = ? LIMIT 1), ?, ?)
		`, projectName, info.Name, info.Description)
	if err != nil {
		return -1, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return -1, err
	}

	err = networkzoneConfigAdd(c.tx, id, info.Config)
	if err != nil {
		return -1, err
	}

	return id, nil
}

// networkzoneConfigAdd inserts Network zone config keys.
func networkzoneConfigAdd(tx *sql.Tx, id int64, config map[string]string) error {
	sql := "INSERT INTO networks_zones_config (network_zone_id, key, value) VALUES(?, ?, ?)"
	stmt, err := tx.Prepare(sql)
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(id, k, v)
		if err != nil {
			return fmt.Errorf("Failed inserting config: %w", err)
		}
	}

	return nil
}

// UpdateNetworkZone updates the Network zone with the given ID.
func (c *Cluster) UpdateNetworkZone(id int64, config *api.NetworkZonePut) error {
	return c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		_, err := tx.tx.Exec(`
			UPDATE networks_zones
			SET description=?
			WHERE id=?
		`, config.Description, id)
		if err != nil {
			return err
		}

		_, err = tx.tx.Exec("DELETE FROM networks_zones_config WHERE network_zone_id=?", id)
		if err != nil {
			return err
		}

		err = networkzoneConfigAdd(tx.tx, id, config.Config)
		if err != nil {
			return err
		}

		return nil
	})
}

// DeleteNetworkZone deletes the Network zone.
func (c *ClusterTx) DeleteNetworkZone(ctx context.Context, id int64) error {
	_, err := c.tx.ExecContext(ctx, "DELETE FROM networks_zones WHERE id=?", id)

	return err
}

// GetNetworkZoneRecordNames returns the names of existing Network zone records.
func (c *ClusterTx) GetNetworkZoneRecordNames(ctx context.Context, zone int64) ([]string, error) {
	q := `SELECT name FROM networks_zones_records
		WHERE network_zone_id=?
		ORDER BY name
	`

	var recordNames []string

	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var recordName string

		err := scan(&recordName)
		if err != nil {
			return err
		}

		recordNames = append(recordNames, recordName)

		return nil
	}, zone)
	if err != nil {
		return nil, err
	}

	return recordNames, nil
}

// GetNetworkZoneRecord returns the network zone record for the given zone and name.
func (c *ClusterTx) GetNetworkZoneRecord(ctx context.Context, zone int64, name string) (int64, *api.NetworkZoneRecord, error) {
	var id int64 = int64(-1)

	record := api.NetworkZoneRecord{
		Name: name,
	}

	q := `
		SELECT networks_zones_records.id, networks_zones_records.description, networks_zones_records.entries
		FROM networks_zones_records
		WHERE networks_zones_records.network_zone_id=? AND networks_zones_records.name=?
		LIMIT 1
	`

	var entries string

	err := c.tx.QueryRowContext(ctx, q, zone, name).Scan(&id, &record.Description, &entries)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, api.StatusErrorf(http.StatusNotFound, "Network zone record not found")
		}

		return -1, nil, err
	}

	err = networkZoneRecordConfig(ctx, c, id, &record)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, api.StatusErrorf(http.StatusNotFound, "Network zone record not found")
		}

		return -1, nil, fmt.Errorf("Failed loading config: %w", err)
	}

	// Decode the JSON record.
	err = json.Unmarshal([]byte(entries), &record.Entries)
	if err != nil {
		return -1, nil, err
	}

	return id, &record, nil
}

// networkZoneRecordConfig populates the config map of the network zone record with the given ID.
func networkZoneRecordConfig(ctx context.Context, tx *ClusterTx, id int64, record *api.NetworkZoneRecord) error {
	q := `
		SELECT key, value
		FROM networks_zones_records_config
		WHERE network_zone_record_id=?
	`

	record.Config = make(map[string]string)
	return query.Scan(ctx, tx.Tx(), q, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := record.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for network zone ID %d", key, id)
		}

		record.Config[key] = value

		return nil
	}, id)
}

// CreateNetworkZoneRecord creates a new network zone record.
func (c *ClusterTx) CreateNetworkZoneRecord(ctx context.Context, zone int64, info api.NetworkZoneRecordsPost) (int64, error) {
	// Turn the entries into JSON.
	entries, err := json.Marshal(info.Entries)
	if err != nil {
		return -1, err
	}

	// Insert a new network zone record.
	result, err := c.tx.ExecContext(ctx, `
			INSERT INTO networks_zones_records (network_zone_id, name, description, entries)
			VALUES (?, ?, ?, ?)
		`, zone, info.Name, info.Description, string(entries))
	if err != nil {
		return -1, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return -1, err
	}

	err = networkZoneRecordConfigAdd(c.tx, id, info.Config)
	if err != nil {
		return -1, err
	}

	return id, nil
}

// networkzoneConfigAdd inserts Network zone config keys.
func networkZoneRecordConfigAdd(tx *sql.Tx, id int64, config map[string]string) error {
	sql := "INSERT INTO networks_zones_records_config (network_zone_record_id, key, value) VALUES(?, ?, ?)"
	stmt, err := tx.Prepare(sql)
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(id, k, v)
		if err != nil {
			return fmt.Errorf("Failed inserting config: %w", err)
		}
	}

	return nil
}

// UpdateNetworkZoneRecord updates the network zone record with the given ID.
func (c *ClusterTx) UpdateNetworkZoneRecord(ctx context.Context, id int64, config api.NetworkZoneRecordPut) error {
	// Turn the entries into JSON.
	entries, err := json.Marshal(config.Entries)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `
			UPDATE networks_zones_records
			SET description=?, entries=?
			WHERE id=?
		`, config.Description, string(entries), id)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, "DELETE FROM networks_zones_records_config WHERE network_zone_record_id=?", id)
	if err != nil {
		return err
	}

	err = networkZoneRecordConfigAdd(c.tx, id, config.Config)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkZoneRecord deletes the network zone record.
func (c *ClusterTx) DeleteNetworkZoneRecord(ctx context.Context, id int64) error {
	_, err := c.tx.ExecContext(ctx, "DELETE FROM networks_zones_records WHERE id=?", id)

	return err
}
