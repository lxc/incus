//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lxc/incus/internal/server/db/query"
	"github.com/lxc/incus/shared/api"
)

// CreateNetworkForward creates a new Network Forward.
// If memberSpecific is true, then the forward is associated to the current member, rather than being associated to
// all members.
func (c *ClusterTx) CreateNetworkForward(ctx context.Context, networkID int64, memberSpecific bool, info *api.NetworkForwardsPost) (int64, error) {
	var err error
	var forwardID int64
	var nodeID any

	if memberSpecific {
		nodeID = c.nodeID
	}

	var portsJSON []byte

	if info.Ports != nil {
		portsJSON, err = json.Marshal(info.Ports)
		if err != nil {
			return -1, fmt.Errorf("Failed marshalling ports: %w", err)
		}
	}

	// Insert a new Network forward record.
	result, err := c.tx.ExecContext(ctx, `
		INSERT INTO networks_forwards
		(network_id, node_id, listen_address, description, ports)
		VALUES (?, ?, ?, ?, ?)
		`, networkID, nodeID, info.ListenAddress, info.Description, string(portsJSON))
	if err != nil {
		return -1, err
	}

	forwardID, err = result.LastInsertId()
	if err != nil {
		return -1, err
	}

	// Save config.
	err = networkForwardConfigAdd(c.tx, forwardID, info.Config)
	if err != nil {
		return -1, err
	}

	return forwardID, err
}

// networkForwardConfigAdd inserts Network forward config keys.
func networkForwardConfigAdd(tx *sql.Tx, forwardID int64, config map[string]string) error {
	stmt, err := tx.Prepare(`
	INSERT INTO networks_forwards_config
	(network_forward_id, key, value)
	VALUES(?, ?, ?)
	`)
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(forwardID, k, v)
		if err != nil {
			return fmt.Errorf("Failed inserting config: %w", err)
		}
	}

	return nil
}

// UpdateNetworkForward updates an existing Network Forward.
func (c *ClusterTx) UpdateNetworkForward(ctx context.Context, networkID int64, forwardID int64, info *api.NetworkForwardPut) error {
	var err error
	var portsJSON []byte

	if info.Ports != nil {
		portsJSON, err = json.Marshal(info.Ports)
		if err != nil {
			return fmt.Errorf("Failed marshalling ports: %w", err)
		}
	}

	// Update existing Network forward record.
	res, err := c.tx.ExecContext(ctx, `
		UPDATE networks_forwards
		SET description = ?, ports = ?
		WHERE network_id = ? and id = ?
		`, info.Description, string(portsJSON), networkID, forwardID)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected <= 0 {
		return api.StatusErrorf(http.StatusNotFound, "Network forward not found")
	}

	// Save config.
	_, err = c.tx.ExecContext(ctx, "DELETE FROM networks_forwards_config WHERE network_forward_id=?", forwardID)
	if err != nil {
		return err
	}

	err = networkForwardConfigAdd(c.tx, forwardID, info.Config)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkForward deletes an existing Network Forward.
func (c *ClusterTx) DeleteNetworkForward(ctx context.Context, networkID int64, forwardID int64) error {
	// Delete existing Network forward record.
	res, err := c.tx.ExecContext(ctx, `
			DELETE FROM networks_forwards
			WHERE network_id = ? and id = ?
		`, networkID, forwardID)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected <= 0 {
		return api.StatusErrorf(http.StatusNotFound, "Network forward not found")
	}

	return nil
}

// GetNetworkForward returns the Network Forward ID and info for the given network ID and listen address.
// If memberSpecific is true, then the search is restricted to forwards that belong to this member or belong to
// all members.
func (c *ClusterTx) GetNetworkForward(ctx context.Context, networkID int64, memberSpecific bool, listenAddress string) (int64, *api.NetworkForward, error) {
	forwards, err := c.GetNetworkForwards(ctx, networkID, memberSpecific, listenAddress)
	if (err == nil && len(forwards) <= 0) || errors.Is(err, sql.ErrNoRows) {
		return -1, nil, api.StatusErrorf(http.StatusNotFound, "Network forward not found")
	} else if err == nil && len(forwards) > 1 {
		return -1, nil, api.StatusErrorf(http.StatusConflict, "Network forward found on more than one cluster member. Please target a specific member")
	} else if err != nil {
		return -1, nil, err
	}

	for forwardID, forward := range forwards {
		return forwardID, forward, nil // Only single forward in map.
	}

	return -1, nil, fmt.Errorf("Unexpected forward list size")
}

// networkForwardConfig populates the config map of the Network Forward with the given ID.
func networkForwardConfig(ctx context.Context, tx *ClusterTx, forwardID int64, forward *api.NetworkForward) error {
	q := `
	SELECT
		key,
		value
	FROM networks_forwards_config
	WHERE network_forward_id=?
	`

	forward.Config = make(map[string]string)
	return query.Scan(ctx, tx.Tx(), q, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := forward.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for network forward ID %d", key, forwardID)
		}

		forward.Config[key] = value

		return nil
	}, forwardID)
}

// GetNetworkForwardListenAddresses returns map of Network Forward Listen Addresses for the given network ID keyed
// on Forward ID.
// If memberSpecific is true, then the search is restricted to forwards that belong to this member or belong to
// all members.
func (c *ClusterTx) GetNetworkForwardListenAddresses(ctx context.Context, networkID int64, memberSpecific bool) (map[int64]string, error) {
	var q *strings.Builder = &strings.Builder{}
	args := []any{networkID}

	q.WriteString(`
	SELECT
		id,
		listen_address
	FROM networks_forwards
	WHERE networks_forwards.network_id = ?
	`)

	if memberSpecific {
		q.WriteString("AND (networks_forwards.node_id = ? OR networks_forwards.node_id IS NULL) ")
		args = append(args, c.nodeID)
	}

	forwards := make(map[int64]string)

	err := query.Scan(ctx, c.tx, q.String(), func(scan func(dest ...any) error) error {
		var forwardID int64 = int64(-1)
		var listenAddress string

		err := scan(&forwardID, &listenAddress)
		if err != nil {
			return err
		}

		forwards[forwardID] = listenAddress

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	return forwards, nil
}

// GetProjectNetworkForwardListenAddressesByUplink returns map of Network Forward Listen Addresses that belong to
// networks connected to the specified uplinkNetworkName.
// Returns a map keyed on project name and network name containing a slice of listen addresses.
func (c *ClusterTx) GetProjectNetworkForwardListenAddressesByUplink(ctx context.Context, uplinkNetworkName string, memberSpecific bool) (map[string]map[string][]string, error) {
	q := strings.Builder{}
	args := []any{uplinkNetworkName}

	// As uplink networks can only be in default project, it is safe to look for networks that reference the
	// specified uplinkNetworkName in their "network" config property, or are the uplink network themselves in
	// the default project.
	q.WriteString(`
	SELECT
		projects.name,
		networks.name,
		networks_forwards.listen_address
	FROM networks_forwards
	JOIN networks on networks.id = networks_forwards.network_id
	JOIN networks_config on networks.id = networks_config.network_id
	JOIN projects ON projects.id = networks.project_id
	WHERE (
		(networks_config.key = "network" AND networks_config.value = ?1)
		OR (projects.name = "default" AND networks.name = ?1)
	)
	`)

	if memberSpecific {
		q.WriteString("AND (networks_forwards.node_id = ?2 OR networks_forwards.node_id IS NULL) ")
		args = append(args, c.nodeID)
	}

	q.WriteString("GROUP BY projects.name, networks.id, networks_forwards.listen_address")

	forwards := make(map[string]map[string][]string)

	err := query.Scan(ctx, c.Tx(), q.String(), func(scan func(dest ...any) error) error {
		var projectName string
		var networkName string
		var listenAddress string

		err := scan(&projectName, &networkName, &listenAddress)
		if err != nil {
			return err
		}

		if forwards[projectName] == nil {
			forwards[projectName] = make(map[string][]string)
		}

		if forwards[projectName][networkName] == nil {
			forwards[projectName][networkName] = make([]string, 0)
		}

		forwards[projectName][networkName] = append(forwards[projectName][networkName], listenAddress)

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	return forwards, nil
}

// GetProjectNetworkForwardListenAddressesOnMember returns map of Network Forward Listen Addresses that belong to
// to this specific cluster member. Will not include forwards that do not have a specific member.
// Returns a map keyed on project name and network ID containing a slice of listen addresses.
func (c *ClusterTx) GetProjectNetworkForwardListenAddressesOnMember(ctx context.Context) (map[string]map[int64][]string, error) {
	q := `
	SELECT
		projects.name,
		networks.id,
		networks_forwards.listen_address
	FROM networks_forwards
	JOIN networks on networks.id = networks_forwards.network_id
	JOIN projects ON projects.id = networks.project_id
	WHERE networks_forwards.node_id = ?
	`
	forwards := make(map[string]map[int64][]string)

	err := query.Scan(ctx, c.Tx(), q, func(scan func(dest ...any) error) error {
		var projectName string
		var networkID int64 = int64(-1)
		var listenAddress string

		err := scan(&projectName, &networkID, &listenAddress)
		if err != nil {
			return err
		}

		if forwards[projectName] == nil {
			forwards[projectName] = make(map[int64][]string)
		}

		if forwards[projectName][networkID] == nil {
			forwards[projectName][networkID] = make([]string, 0)
		}

		forwards[projectName][networkID] = append(forwards[projectName][networkID], listenAddress)

		return nil
	}, c.nodeID)
	if err != nil {
		return nil, err
	}

	return forwards, nil
}

// GetNetworkForwards returns map of Network Forwards for the given network ID keyed on Forward ID.
// If memberSpecific is true, then the search is restricted to forwards that belong to this member or belong to
// all members. Can optionally retrieve only specific network forwards by listen address.
func (c *ClusterTx) GetNetworkForwards(ctx context.Context, networkID int64, memberSpecific bool, listenAddresses ...string) (map[int64]*api.NetworkForward, error) {
	var q *strings.Builder = &strings.Builder{}
	args := []any{networkID}

	q.WriteString(`
	SELECT
		networks_forwards.id,
		networks_forwards.listen_address,
		networks_forwards.description,
		IFNULL(nodes.name, "") as location,
		networks_forwards.ports
	FROM networks_forwards
	LEFT JOIN nodes ON nodes.id = networks_forwards.node_id
	WHERE networks_forwards.network_id = ?
	`)

	if memberSpecific {
		q.WriteString("AND (networks_forwards.node_id = ? OR networks_forwards.node_id IS NULL) ")
		args = append(args, c.nodeID)
	}

	if len(listenAddresses) > 0 {
		q.WriteString(fmt.Sprintf("AND networks_forwards.listen_address IN %s ", query.Params(len(listenAddresses))))
		for _, listenAddress := range listenAddresses {
			args = append(args, listenAddress)
		}
	}

	var err error
	forwards := make(map[int64]*api.NetworkForward)

	err = query.Scan(ctx, c.tx, q.String(), func(scan func(dest ...any) error) error {
		var forwardID int64 = int64(-1)
		var portsJSON string
		var forward api.NetworkForward

		err := scan(&forwardID, &forward.ListenAddress, &forward.Description, &forward.Location, &portsJSON)
		if err != nil {
			return err
		}

		forward.Ports = []api.NetworkForwardPort{}
		if portsJSON != "" {
			err = json.Unmarshal([]byte(portsJSON), &forward.Ports)
			if err != nil {
				return fmt.Errorf("Failed unmarshalling ports: %w", err)
			}
		}

		forwards[forwardID] = &forward

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	// Populate config.
	for forwardID := range forwards {
		err = networkForwardConfig(ctx, c, forwardID, forwards[forwardID])
		if err != nil {
			return nil, err
		}
	}

	return forwards, nil
}
