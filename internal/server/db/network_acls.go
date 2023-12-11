//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lxc/incus/internal/server/db/query"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
)

// GetNetworkACLs returns the names of existing Network ACLs.
func (c *ClusterTx) GetNetworkACLs(ctx context.Context, project string) ([]string, error) {
	q := `SELECT name FROM networks_acls
		WHERE project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1)
		ORDER BY id
	`

	var aclNames []string

	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var aclName string

		err := scan(&aclName)
		if err != nil {
			return err
		}

		aclNames = append(aclNames, aclName)

		return nil
	}, project)
	if err != nil {
		return nil, err
	}

	return aclNames, nil
}

// GetNetworkACLIDsByNames returns a map of names to IDs of existing Network ACLs.
func (c *ClusterTx) GetNetworkACLIDsByNames(ctx context.Context, project string) (map[string]int64, error) {
	q := `SELECT id, name FROM networks_acls
		WHERE project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1)
		ORDER BY id
	`

	acls := make(map[string]int64)

	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var aclID int64
		var aclName string

		err := scan(&aclID, &aclName)
		if err != nil {
			return err
		}

		acls[aclName] = aclID

		return nil
	}, project)
	if err != nil {
		return nil, err
	}

	return acls, nil
}

// GetNetworkACL returns the Network ACL with the given name in the given project.
func (c *ClusterTx) GetNetworkACL(ctx context.Context, projectName string, name string) (int64, *api.NetworkACL, error) {
	var id int64 = int64(-1)
	var ingressJSON string
	var egressJSON string

	acl := api.NetworkACL{
		NetworkACLPost: api.NetworkACLPost{
			Name: name,
		},
	}

	q := `
		SELECT id, description, ingress, egress
		FROM networks_acls
		WHERE project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1) AND name=?
		LIMIT 1
	`

	err := c.tx.QueryRowContext(ctx, q, projectName, name).Scan(&id, &acl.Description, &ingressJSON, &egressJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, api.StatusErrorf(http.StatusNotFound, "Network ACL not found")
		}

		return -1, nil, err
	}

	err = networkACLConfig(ctx, c, id, &acl)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, api.StatusErrorf(http.StatusNotFound, "Network ACL not found")
		}

		return -1, nil, fmt.Errorf("Failed loading config: %w", err)
	}

	acl.Ingress = []api.NetworkACLRule{}
	if ingressJSON != "" {
		err = json.Unmarshal([]byte(ingressJSON), &acl.Ingress)
		if err != nil {
			return -1, nil, fmt.Errorf("Failed unmarshalling ingress rules: %w", err)
		}
	}

	acl.Egress = []api.NetworkACLRule{}
	if egressJSON != "" {
		err = json.Unmarshal([]byte(egressJSON), &acl.Egress)
		if err != nil {
			return -1, nil, fmt.Errorf("Failed unmarshalling egress rules: %w", err)
		}
	}

	return id, &acl, nil
}

// GetNetworkACLNameAndProjectWithID returns the network ACL name and project name for the given ID.
func (c *ClusterTx) GetNetworkACLNameAndProjectWithID(ctx context.Context, networkACLID int) (string, string, error) {
	var networkACLName string
	var projectName string

	q := `SELECT networks_acls.name, projects.name FROM networks_acls JOIN projects ON projects.id=networks.project_id WHERE networks_acls.id=?`

	err := c.tx.QueryRowContext(ctx, q, networkACLID).Scan(&networkACLName, &projectName)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", api.StatusErrorf(http.StatusNotFound, "Network ACL not found")
		}

		return "", "", err
	}

	return networkACLName, projectName, nil
}

// networkACLConfig populates the config map of the Network ACL with the given ID.
func networkACLConfig(ctx context.Context, tx *ClusterTx, id int64, acl *api.NetworkACL) error {
	q := `
		SELECT key, value
		FROM networks_acls_config
		WHERE network_acl_id=?
	`

	acl.Config = make(map[string]string)
	return query.Scan(ctx, tx.Tx(), q, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := acl.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for network ACL ID %d", key, id)
		}

		acl.Config[key] = value

		return nil
	}, id)
}

// CreateNetworkACL creates a new Network ACL.
func (c *ClusterTx) CreateNetworkACL(ctx context.Context, projectName string, info *api.NetworkACLsPost) (int64, error) {
	var err error
	var ingressJSON, egressJSON []byte

	if info.Ingress != nil {
		ingressJSON, err = json.Marshal(info.Ingress)
		if err != nil {
			return -1, fmt.Errorf("Failed marshalling ingress rules: %w", err)
		}
	}

	if info.Egress != nil {
		egressJSON, err = json.Marshal(info.Egress)
		if err != nil {
			return -1, fmt.Errorf("Failed marshalling egress rules: %w", err)
		}
	}

	// Insert a new Network ACL record.
	result, err := c.tx.ExecContext(ctx, `
			INSERT INTO networks_acls (project_id, name, description, ingress, egress)
			VALUES ((SELECT id FROM projects WHERE name = ? LIMIT 1), ?, ?, ?, ?)
		`, projectName, info.Name, info.Description, string(ingressJSON), string(egressJSON))
	if err != nil {
		return -1, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return -1, err
	}

	err = networkACLConfigAdd(c.tx, id, info.Config)
	if err != nil {
		return -1, err
	}

	return id, err
}

// networkACLConfigAdd inserts Network ACL config keys.
func networkACLConfigAdd(tx *sql.Tx, id int64, config map[string]string) error {
	sql := "INSERT INTO networks_acls_config (network_acl_id, key, value) VALUES(?, ?, ?)"
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

// UpdateNetworkACL updates the Network ACL with the given ID.
func (c *ClusterTx) UpdateNetworkACL(ctx context.Context, id int64, config *api.NetworkACLPut) error {
	var err error
	var ingressJSON, egressJSON []byte

	if config.Ingress != nil {
		ingressJSON, err = json.Marshal(config.Ingress)
		if err != nil {
			return fmt.Errorf("Failed marshalling ingress rules: %w", err)
		}
	}

	if config.Egress != nil {
		egressJSON, err = json.Marshal(config.Egress)
		if err != nil {
			return fmt.Errorf("Failed marshalling egress rules: %w", err)
		}
	}

	_, err = c.tx.ExecContext(ctx, `
			UPDATE networks_acls
			SET description=?, ingress = ?, egress = ?
			WHERE id=?
		`, config.Description, ingressJSON, egressJSON, id)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, "DELETE FROM networks_acls_config WHERE network_acl_id=?", id)
	if err != nil {
		return err
	}

	err = networkACLConfigAdd(c.tx, id, config.Config)
	if err != nil {
		return err
	}

	return nil
}

// RenameNetworkACL renames a Network ACL.
func (c *ClusterTx) RenameNetworkACL(ctx context.Context, id int64, newName string) error {
	_, err := c.tx.ExecContext(ctx, "UPDATE networks_acls SET name=? WHERE id=?", newName, id)

	return err
}

// DeleteNetworkACL deletes the Network ACL.
func (c *ClusterTx) DeleteNetworkACL(ctx context.Context, id int64) error {
	_, err := c.tx.ExecContext(ctx, "DELETE FROM networks_acls WHERE id=?", id)

	return err
}

// GetNetworkACLURIs returns the URIs for the network ACLs with the given project.
func (c *ClusterTx) GetNetworkACLURIs(ctx context.Context, projectID int, project string) ([]string, error) {
	sql := `SELECT networks_acls.name from networks_acls WHERE networks_acls.project_id = ?`

	names, err := query.SelectStrings(ctx, c.tx, sql, projectID)
	if err != nil {
		return nil, fmt.Errorf("Unable to get URIs for network acl: %w", err)
	}

	uris := make([]string, len(names))
	for i := range names {
		uris[i] = api.NewURL().Path(version.APIVersion, "network-acls", names[i]).Project(project).String()
	}

	return uris, nil
}
