//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lxc/incus/v6/internal/server/db/query"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// GetNetworkAddressSets returns the names of existing Network Address Sets for a given project.
func (c *ClusterTx) GetNetworkAddressSets(ctx context.Context, project string) ([]string, error) {
	q := `
	SELECT name
	FROM address_sets
	WHERE project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1)
	ORDER BY id
	`

	var setNames []string

	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var setName string
		err := scan(&setName)
		if err != nil {
			return err
		}

		setNames = append(setNames, setName)
		return nil
	}, project)
	if err != nil {
		return nil, err
	}

	return setNames, nil
}

// GetNetworkAddressSetsAllProjects returns the names of existing Network Address Sets across all projects.
func (c *ClusterTx) GetNetworkAddressSetsAllProjects(ctx context.Context) (map[string][]string, error) {
	q := `
	SELECT projects.name, address_sets.name FROM address_sets
	JOIN projects ON projects.id=address_sets.project_id
	ORDER BY address_sets.id
	`

	setNames := map[string][]string{}
	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var projectName, setName string
		err := scan(&projectName, &setName)

		if err != nil {
			return err
		}

		setNames[projectName] = append(setNames[projectName], setName)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return setNames, nil
}

// GetNetworkAddressSetIDByNames returns a map of names to IDs of existing Address Sets in a project.
func (c *ClusterTx) GetNetworkAddressSetIDByNames(ctx context.Context, project string) (map[string]int64, error) {
	q := `
	SELECT id, name
	FROM address_sets
	WHERE project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1)
	ORDER BY id
	`

	addrSets := make(map[string]int64)

	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var addrSetID int64
		var addrSetName string

		err := scan(&addrSetID, &addrSetName)

		if err != nil {
			return err
		}

		addrSets[addrSetName] = addrSetID
		return nil
	}, project)

	if err != nil {
		return nil, err
	}

	return addrSets, nil
}

// GetNetworkAddressSet returns the Network Address Set with the given name in the given project.
func (c *ClusterTx) GetNetworkAddressSet(ctx context.Context, projectName string, name string) (int64, *api.NetworkAddressSet, error) {
	q := `
	SELECT id, addresses, description
	FROM address_sets
	WHERE project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1) AND name = ?
	LIMIT 1
	`
	var id int64
	var addressesJSON string
	var description string

	err := c.tx.QueryRowContext(ctx, q, projectName, name).Scan(&id, &addressesJSON, &description)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, api.StatusErrorf(http.StatusNotFound, "Network address set not found")
		}

		return -1, nil, err
	}

	var addresses []string
	if addressesJSON != "" {
		err = json.Unmarshal([]byte(addressesJSON), &addresses)
		if err != nil {
			return -1, nil, fmt.Errorf("Failed unmarshalling addresses: %w", err)
		}
	}

	extIDs, err := c.getNetworkAddressSetExternalIDs(ctx, id)
	if err != nil {
		return -1, nil, fmt.Errorf("Failed loading external_ids for address set: %w", err)
	}

	as := &api.NetworkAddressSet{
		NetworkAddressSetPost: api.NetworkAddressSetPost{
			Name: name,
		},
		NetworkAddressSetPut: api.NetworkAddressSetPut{
			Addresses:   addresses,
			Description: description,
			ExternalIDs: extIDs,
		},
	}

	return id, as, nil
}

// GetNetworkAddressSetNameAndProjectWithID returns the network address set name and project name for the given ID.
func (c *ClusterTx) GetNetworkAddressSetNameAndProjectWithID(ctx context.Context, networkAddressSetID int) (string, string, error) {
	var networkAddressSetName string
	var projectName string
	q := `SELECT address_sets.name, projects.name FROM address_sets JOIN projects ON projects.id = address_sets.project_id WHERE address_sets.id = ?`
	err := c.tx.QueryRowContext(ctx, q, networkAddressSetID).Scan(&networkAddressSetName, &projectName)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", api.StatusErrorf(http.StatusNotFound, "Network AddressSet not found")
		}

		return "", "", err
	}

	return networkAddressSetName, projectName, nil
}

// CreateNetworkAddressSet creates a new Network Address Set.
func (c *ClusterTx) CreateNetworkAddressSet(ctx context.Context, projectName string, info *api.NetworkAddressSetsPost) (int64, error) {
	addressesJSON, err := json.Marshal(info.Addresses)
	if err != nil {
		return -1, fmt.Errorf("Failed marshalling addresses: %w", err)
	}

	stmt := `
	INSERT INTO address_sets (project_id, name, addresses, description)
	VALUES ((SELECT id FROM projects WHERE name = ? LIMIT 1), ?, ?, ?)
	`
	res, err := c.tx.ExecContext(ctx, stmt, projectName, info.Name, string(addressesJSON), string(info.Description))
	if err != nil {
		return -1, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return -1, err
	}

	err = c.updateNetworkAddressSetExternalIDs(ctx, id, info.ExternalIDs)
	if err != nil {
		return -1, err
	}

	return id, nil
}

// UpdateNetworkAddressSet updates an existing Network Address Set.
func (c *ClusterTx) UpdateNetworkAddressSet(ctx context.Context, projectName string, name string, put *api.NetworkAddressSetPut) error {
	projectID, err := c.getProjectID(ctx, projectName)
	if err != nil {
		return err
	}

	addressesJSON, err := json.Marshal(put.Addresses)
	if err != nil {
		return fmt.Errorf("Failed marshalling addresses: %w", err)
	}

	stmt := `
	UPDATE address_sets
	SET addresses = ?, description = ?
	WHERE project_id = ? AND name = ?
	`
	res, err := c.tx.ExecContext(ctx, stmt, string(addressesJSON), put.Description, projectID, name)

	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Address set not found")
	}

	var id int64
	err = c.tx.QueryRowContext(ctx, "SELECT id FROM address_sets WHERE project_id = ? AND name = ? LIMIT 1", projectID, name).Scan(&id)

	if err != nil {
		return err
	}

	err = c.updateNetworkAddressSetExternalIDs(ctx, id, put.ExternalIDs)
	if err != nil {
		return err
	}

	return nil
}

// RenameNetworkAddressSet renames an existing Network Address Set.
func (c *ClusterTx) RenameNetworkAddressSet(ctx context.Context, projectName string, oldName string, newName string) error {
	projectID, err := c.getProjectID(ctx, projectName)
	if err != nil {
		return err
	}

	stmt := `
	UPDATE address_sets
	SET name = ?
	WHERE project_id = ? AND name = ?
	`

	res, err := c.tx.ExecContext(ctx, stmt, newName, projectID, oldName)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()

	if err != nil {
		return err
	}

	if rows == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Address set not found")
	}

	return nil
}

// DeleteNetworkAddressSet deletes an existing Address Set.
func (c *ClusterTx) DeleteNetworkAddressSet(ctx context.Context, projectName string, name string) error {
	projectID, err := c.getProjectID(ctx, projectName)
	if err != nil {
		return err
	}

	stmt := `
	DELETE FROM address_sets
	WHERE project_id = ? AND name = ?
	`

	res, err := c.tx.ExecContext(ctx, stmt, projectID, name)

	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()

	if err != nil {
		return err
	}

	if rows == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Address set not found")
	}

	return nil
}

// getNetworkAddressSetExternalIDs retrieves the external_ids for a given address set.
func (c *ClusterTx) getNetworkAddressSetExternalIDs(ctx context.Context, addressSetID int64) (map[string]string, error) {
	q := `
	SELECT key, value
	FROM address_sets_external_ids
	WHERE address_set_id=?
	`
	extIDs := make(map[string]string)
	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var k, v string
		err := scan(&k, &v)

		if err != nil {
			return err
		}

		extIDs[k] = v
		return nil
	}, addressSetID)

	if err != nil {
		return nil, err
	}

	return extIDs, nil
}

// updateNetworkAddressSetExternalIDs replaces the external_ids for the given address_set_id.
func (c *ClusterTx) updateNetworkAddressSetExternalIDs(ctx context.Context, addressSetID int64, externalIDs map[string]string) error {
	_, err := c.tx.ExecContext(ctx, "DELETE FROM address_sets_external_ids WHERE address_set_id=?", addressSetID)
	if err != nil {
		return fmt.Errorf("Failed clearing address set external IDs: %w", err)
	}

	if externalIDs != nil {
		stmt := "INSERT INTO address_sets_external_ids (address_set_id, key, value) VALUES (?, ?, ?)"
		for k, v := range externalIDs {
			_, err = c.tx.ExecContext(ctx, stmt, addressSetID, k, v)

			if err != nil {
				return fmt.Errorf("Failed inserting external_id %s=%s: %w", k, v, err)
			}
		}
	}

	return nil
}

// getProjectID retrieves the project ID for a given project name.
func (c *ClusterTx) getProjectID(ctx context.Context, projectName string) (int64, error) {
	var projectID int64
	err := c.tx.QueryRowContext(ctx, "SELECT id FROM projects WHERE name=? LIMIT 1", projectName).Scan(&projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, api.StatusErrorf(http.StatusNotFound, "Project not found")
		}

		return -1, err
	}

	return projectID, nil
}

// GetNetworkAddressSetURIs returns the URIs for the network address sets with the given project.
func (c *ClusterTx) GetNetworkAddressSetURIs(ctx context.Context, projectID int, project string) ([]string, error) {
	stmt := `SELECT name FROM address_sets WHERE project_id = ?`
	names, err := query.SelectStrings(ctx, c.tx, stmt, projectID)
	if err != nil {
		return nil, fmt.Errorf("Unable to get URIs for network address sets: %w", err)
	}

	uris := make([]string, len(names))
	for i := range names {
		uris[i] = api.NewURL().Path(version.APIVersion, "network-address-sets", names[i]).Project(project).String()
	}

	return uris, nil
}
