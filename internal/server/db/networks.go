//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/query"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/util"
)

// GetNetworksLocalConfig returns a map associating each network name to its
// node-specific config values on the local member (i.e. the ones where node_id
// equals the ID of the local member).
func (c *ClusterTx) GetNetworksLocalConfig(ctx context.Context) (map[string]map[string]string, error) {
	names, err := query.SelectStrings(ctx, c.tx, "SELECT name FROM networks")
	if err != nil {
		return nil, err
	}

	networks := make(map[string]map[string]string, len(names))
	for _, name := range names {
		table := "networks_config JOIN networks ON networks.id=networks_config.network_id"
		config, err := query.SelectConfig(ctx, c.tx, table, "networks.name=? AND networks_config.node_id=?",
			name, c.nodeID)
		if err != nil {
			return nil, err
		}

		networks[name] = config
	}

	return networks, nil
}

// GetNonPendingNetworkIDs returns a map associating each network name to its ID.
//
// Pending networks are skipped.
func (c *ClusterTx) GetNonPendingNetworkIDs(ctx context.Context) (map[string]map[string]int64, error) {
	type network struct {
		id          int64
		name        string
		projectName string
	}

	networks := []network{}
	sql := "SELECT networks.id, networks.name, projects.name FROM networks JOIN projects on projects.id = networks.project_id WHERE NOT networks.state=?"
	err := query.Scan(ctx, c.tx, sql, func(scan func(dest ...any) error) error {
		n := network{}

		err := scan(&n.id, &n.name, &n.projectName)
		if err != nil {
			return err
		}

		networks = append(networks, n)

		return nil
	}, networkPending)
	if err != nil {
		return nil, err
	}

	ids := map[string]map[string]int64{}
	for _, network := range networks {
		if ids[network.projectName] == nil {
			ids[network.projectName] = map[string]int64{}
		}

		ids[network.projectName][network.name] = network.id
	}

	return ids, nil
}

// GetCreatedNetworks returns a map of api.Network associated to project and network ID.
// Only networks that have are in state networkCreated are returned.
func (c *ClusterTx) GetCreatedNetworks(ctx context.Context) (map[string]map[int64]api.Network, error) {
	return c.getCreatedNetworks(ctx, "")
}

// GetCreatedNetworksByProject returns a map of api.Network in a project associated to network ID.
// Only networks that have are in state networkCreated are returned.
func (c *ClusterTx) GetCreatedNetworksByProject(ctx context.Context, projectName string) (map[int64]api.Network, error) {
	nets, err := c.getCreatedNetworks(ctx, projectName)
	if err != nil {
		return nil, err
	}

	return nets[projectName], nil
}

// getCreatedNetworks returns a map of api.Network associated to project and network ID.
// Supports an optional projectName filter. If projectName is empty, all networks in created state are returned.
func (c *ClusterTx) getCreatedNetworks(ctx context.Context, projectName string) (map[string]map[int64]api.Network, error) {
	var sb strings.Builder
	sb.WriteString(`SELECT projects.name, networks.id, networks.name, coalesce(networks.description, ''), networks.type, networks.state
	FROM networks
	JOIN projects on projects.id = networks.project_id
	WHERE networks.state = ?
	`)

	args := []any{networkCreated}

	if projectName != "" {
		sb.WriteString(" AND projects.name = ?")
		args = append(args, projectName)
	}

	rows, err := c.tx.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	projectNetworks := make(map[string]map[int64]api.Network)

	for i := 0; rows.Next(); i++ {
		var projectName string
		var networkID int64
		var networkType NetworkType
		var networkState NetworkState
		var network api.Network

		err := rows.Scan(&projectName, &networkID, &network.Name, &network.Description, &networkType, &networkState)
		if err != nil {
			return nil, err
		}

		// Populate Status and Type fields by converting from DB values.
		network.Status = NetworkStateToAPIStatus(networkState)
		networkFillType(&network, networkType)

		if projectNetworks[projectName] != nil {
			projectNetworks[projectName][networkID] = network
		} else {
			projectNetworks[projectName] = map[int64]api.Network{
				networkID: network,
			}
		}
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	// Populate config.
	for projectName, networks := range projectNetworks {
		for networkID, network := range networks {
			networkConfig, err := query.SelectConfig(ctx, c.tx, "networks_config", "network_id=? AND (node_id=? OR node_id IS NULL)", networkID, c.nodeID)
			if err != nil {
				return nil, err
			}

			network.Config = networkConfig

			nodes, err := c.NetworkNodes(ctx, networkID)
			if err != nil {
				return nil, err
			}

			for _, node := range nodes {
				network.Locations = append(network.Locations, node.Name)
			}

			projectNetworks[projectName][networkID] = network
		}
	}

	return projectNetworks, nil
}

// GetNetworkID returns the ID of the network with the given name.
func (c *ClusterTx) GetNetworkID(ctx context.Context, projectName string, name string) (int64, error) {
	stmt := "SELECT id FROM networks WHERE project_id = (SELECT id FROM projects WHERE name = ?) AND name=?"
	ids, err := query.SelectIntegers(ctx, c.tx, stmt, projectName, name)
	if err != nil {
		return -1, err
	}

	switch len(ids) {
	case 0:
		return -1, api.StatusErrorf(http.StatusNotFound, "Network not found")
	case 1:
		return int64(ids[0]), nil
	default:
		return -1, fmt.Errorf("More than one network has the given name")
	}
}

// GetNetworkNameAndProjectWithID returns the network name and project name for the given ID.
func (c *Cluster) GetNetworkNameAndProjectWithID(networkID int) (string, string, error) {
	var networkName string
	var projectName string

	q := `SELECT networks.name, projects.name FROM networks JOIN projects ON projects.id=networks.project_id WHERE networks.id=?`

	inargs := []any{networkID}
	outargs := []any{&networkName, &projectName}

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		return dbQueryRowScan(ctx, tx, q, inargs, outargs)
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", api.StatusErrorf(http.StatusNotFound, "Network not found")
		}

		return "", "", err
	}

	return networkName, projectName, nil
}

// CreateNetworkConfig adds a new entry in the networks_config table.
func (c *ClusterTx) CreateNetworkConfig(networkID, nodeID int64, config map[string]string) error {
	return networkConfigAdd(c.tx, networkID, nodeID, config)
}

// NetworkNodeJoin adds a new entry in the networks_nodes table.
//
// It should only be used when a new node joins the cluster, when it's safe to
// assume that the relevant network has already been created on the joining node,
// and we just need to track it.
func (c *ClusterTx) NetworkNodeJoin(networkID, nodeID int64) error {
	columns := []string{"network_id", "node_id", "state"}
	// Create network node with networkCreated state as we expect the network to already be setup.
	values := []any{networkID, nodeID, networkCreated}
	_, err := query.UpsertObject(c.tx, "networks_nodes", columns, values)
	return err
}

// NetworkNodeConfigs returns the node-specific configuration of all
// nodes grouped by node name, for the given networkID.
//
// If the network is not defined on all nodes, an error is returned.
func (c *ClusterTx) NetworkNodeConfigs(ctx context.Context, networkID int64) (map[string]map[string]string, error) {
	// Fetch all nodes.
	nodes, err := c.GetNodes(ctx)
	if err != nil {
		return nil, err
	}

	// Fetch the names of the nodes where the storage network is defined.
	stmt := `
SELECT nodes.name FROM nodes
  LEFT JOIN networks_nodes ON networks_nodes.node_id = nodes.id
  LEFT JOIN networks ON networks_nodes.network_id = networks.id
WHERE networks.id = ? AND networks.state = ?
`
	defined, err := query.SelectStrings(ctx, c.tx, stmt, networkID, networkPending)
	if err != nil {
		return nil, err
	}

	// Figure which nodes are missing
	missing := []string{}
	for _, node := range nodes {
		if !util.ValueInSlice(node.Name, defined) {
			missing = append(missing, node.Name)
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("Network not defined on nodes: %s", strings.Join(missing, ", "))
	}

	configs := map[string]map[string]string{}
	for _, node := range nodes {
		config, err := query.SelectConfig(ctx, c.tx, "networks_config", "node_id=?", node.ID)
		if err != nil {
			return nil, err
		}

		configs[node.Name] = config
	}

	return configs, nil
}

// CreatePendingNetwork creates a new pending network on the node with the given name.
func (c *ClusterTx) CreatePendingNetwork(ctx context.Context, node string, projectName string, name string, netType NetworkType, conf map[string]string) error {
	// First check if a network with the given name exists, and, if so, that it's in the pending state.
	network := struct {
		id      int64
		state   NetworkState
		netType NetworkType
	}{}

	sql := "SELECT id, state, type FROM networks WHERE project_id = (SELECT id FROM projects WHERE name = ?) AND name=?"
	count := 0
	err := query.Scan(ctx, c.tx, sql, func(scan func(dest ...any) error) error {
		// Ensure that there is at most one network with the given name.
		if count != 0 {
			return fmt.Errorf("More than one network exists with the given name")
		}

		count++

		return scan(&network.id, &network.state, &network.netType)
	}, projectName, name)
	if err != nil {
		return err
	}

	var networkID = network.id
	if networkID == 0 {
		projectID, err := cluster.GetProjectID(context.Background(), c.tx, projectName)
		if err != nil {
			return fmt.Errorf("Fetch project ID: %w", err)
		}

		// No existing network with the given name was found, let's create one.
		columns := []string{"project_id", "name", "type", "description"}
		values := []any{projectID, name, netType, ""}
		networkID, err = query.UpsertObject(c.tx, "networks", columns, values)
		if err != nil {
			return err
		}
	} else {
		// Check that the existing network is in the networkPending state.
		if network.state != networkPending {
			return fmt.Errorf("Network is not in pending state")
		}

		// Check that the existing network type matches the requested type.
		if network.netType != netType {
			return fmt.Errorf("Requested network type doesn't match type in existing database record")
		}
	}

	// Get the ID of the node with the given name.
	nodeInfo, err := c.GetNodeByName(ctx, node)
	if err != nil {
		return err
	}

	// Check that no network entry for this node and network exists yet.
	count, err = query.Count(ctx, c.tx, "networks_nodes", "network_id=? AND node_id=?", networkID, nodeInfo.ID)
	if err != nil {
		return err
	}

	if count != 0 {
		return ErrAlreadyDefined
	}

	// Insert the node-specific configuration with state networkPending.
	columns := []string{"network_id", "node_id", "state"}
	values := []any{networkID, nodeInfo.ID, networkPending}
	_, err = query.UpsertObject(c.tx, "networks_nodes", columns, values)
	if err != nil {
		return err
	}

	err = c.CreateNetworkConfig(networkID, nodeInfo.ID, conf)
	if err != nil {
		return err
	}

	return nil
}

// NetworkCreated sets the state of the given network to networkCreated.
func (c *ClusterTx) NetworkCreated(project string, name string) error {
	return c.networkState(project, name, networkCreated)
}

// NetworkErrored sets the state of the given network to networkErrored.
func (c *ClusterTx) NetworkErrored(project string, name string) error {
	return c.networkState(project, name, networkErrored)
}

func (c *ClusterTx) networkState(project string, name string, state NetworkState) error {
	stmt := "UPDATE networks SET state=? WHERE project_id = (SELECT id FROM projects WHERE name = ?) AND name=?"
	result, err := c.tx.Exec(stmt, state, project, name)
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if n != 1 {
		return api.StatusErrorf(http.StatusNotFound, "Network not found")
	}

	return nil
}

// NetworkNodeCreated sets the state of the given network for the local member to networkCreated.
func (c *ClusterTx) NetworkNodeCreated(networkID int64) error {
	return c.networkNodeState(networkID, networkCreated)
}

// networkNodeState updates the network member state for the local member and specified network ID.
func (c *ClusterTx) networkNodeState(networkID int64, state NetworkState) error {
	stmt := "UPDATE networks_nodes SET state=? WHERE network_id = ? and node_id = ?"
	result, err := c.tx.Exec(stmt, state, networkID, c.nodeID)
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if n != 1 {
		return api.StatusErrorf(http.StatusNotFound, "Network not found")
	}

	return nil
}

// UpdateNetwork updates the network with the given ID.
func (c *ClusterTx) UpdateNetwork(id int64, description string, config map[string]string) error {
	err := updateNetworkDescription(c.tx, id, description)
	if err != nil {
		return err
	}

	err = clearNetworkConfig(c.tx, id, c.nodeID)
	if err != nil {
		return err
	}

	err = networkConfigAdd(c.tx, id, c.nodeID, config)
	if err != nil {
		return err
	}

	return nil
}

// NetworkNodes returns the nodes keyed by node ID that the given network is defined on.
func (c *ClusterTx) NetworkNodes(ctx context.Context, networkID int64) (map[int64]NetworkNode, error) {
	nodes := []NetworkNode{}

	sql := `
		SELECT nodes.id, nodes.name, networks_nodes.state FROM nodes
		JOIN networks_nodes ON networks_nodes.node_id = nodes.id
		WHERE networks_nodes.network_id = ?
	`
	err := query.Scan(ctx, c.tx, sql, func(scan func(dest ...any) error) error {
		node := NetworkNode{}

		err := scan(&node.ID, &node.Name, &node.State)
		if err != nil {
			return err
		}

		nodes = append(nodes, node)

		return nil
	}, networkID)
	if err != nil {
		return nil, err
	}

	netNodes := map[int64]NetworkNode{}
	for _, node := range nodes {
		netNodes[node.ID] = node
	}

	return netNodes, nil
}

// GetNetworkURIs returns the URIs for the networks with the given project.
func (c *ClusterTx) GetNetworkURIs(ctx context.Context, projectID int, project string) ([]string, error) {
	sql := `SELECT networks.name from networks WHERE networks.project_id = ?`

	names, err := query.SelectStrings(ctx, c.tx, sql, projectID)
	if err != nil {
		return nil, fmt.Errorf("Unable to get URIs for network: %w", err)
	}

	uris := make([]string, len(names))
	for i := range names {
		uris[i] = api.NewURL().Path(version.APIVersion, "networks", names[i]).Project(project).String()
	}

	return uris, nil
}

// GetNetworks returns the names of existing networks.
func (c *Cluster) GetNetworks(project string) ([]string, error) {
	return c.networks(project, "")
}

// GetCreatedNetworks returns the names of all networks that are in state networkCreated.
func (c *Cluster) GetCreatedNetworks(project string) ([]string, error) {
	return c.networks(project, "state=?", networkCreated)
}

// Get all networks matching the given WHERE filter (if given).
func (c *Cluster) networks(project string, where string, args ...any) ([]string, error) {
	q := "SELECT name FROM networks WHERE project_id = (SELECT id FROM projects WHERE name = ?)"
	inargs := []any{project}

	if where != "" {
		q += fmt.Sprintf(" AND %s", where)
		inargs = append(inargs, args...)
	}

	var name string
	outfmt := []any{name}

	var result [][]any

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		var err error
		result, err = queryScan(ctx, tx, q, inargs, outfmt)
		return err
	})
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

// NetworkState indicates the state of the network or network node.
type NetworkState int

// Network state.
const (
	networkPending NetworkState = iota // Network defined but not yet created globally or on specific node.
	networkCreated                     // Network created globally or on specific node.
	networkErrored                     // Deprecated (should no longer occur).
)

// NetworkType indicates type of network.
type NetworkType int

// Network types.
const (
	NetworkTypeBridge   NetworkType = iota // Network type bridge.
	NetworkTypeMacvlan                     // Network type macvlan.
	NetworkTypeSriov                       // Network type sriov.
	NetworkTypeOVN                         // Network type ovn.
	NetworkTypePhysical                    // Network type physical.
)

// NetworkNode represents a network node.
type NetworkNode struct {
	ID    int64
	Name  string
	State NetworkState
}

// GetNetworkInAnyState returns the network with the given name. The network can be in any state.
// Returns network ID, network info, and network cluster member info.
func (c *Cluster) GetNetworkInAnyState(projectName string, networkName string) (int64, *api.Network, map[int64]NetworkNode, error) {
	return c.getNetworkByProjectAndName(projectName, networkName, -1)
}

// getNetworkByProjectAndName returns the network with the given project, name and state.
// If stateFilter is -1, then a network can be in any state.
// Returns network ID, network info, and network cluster member info.
func (c *Cluster) getNetworkByProjectAndName(projectName string, networkName string, stateFilter NetworkState) (int64, *api.Network, map[int64]NetworkNode, error) {
	var err error
	var networkID int64
	var networkState NetworkState
	var networkType NetworkType
	var network *api.Network
	var nodes map[int64]NetworkNode

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		networkID, networkState, networkType, network, err = c.getPartialNetworkByProjectAndName(ctx, tx, projectName, networkName, stateFilter)
		if err != nil {
			return err
		}

		nodes, err = c.networkPopulatePeerInfo(ctx, tx, networkID, network, networkState, networkType)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return -1, nil, nil, err
	}

	return networkID, network, nodes, nil
}

// getPartialNetworkByProjectAndName gets the network with the given project, name and state.
// If stateFilter is -1, then a network can be in any state.
// Returns network ID, network state, network type, and partially populated network info.
func (c *Cluster) getPartialNetworkByProjectAndName(ctx context.Context, tx *ClusterTx, projectName string, networkName string, stateFilter NetworkState) (int64, NetworkState, NetworkType, *api.Network, error) {
	var err error
	var networkID int64 = int64(-1)
	var network api.Network
	var networkState NetworkState
	var networkType NetworkType

	// Managed networks exist in the database.
	network.Managed = true

	var q strings.Builder

	q.WriteString(`SELECT n.id, n.name, IFNULL(n.description, "") as description, n.state, n.type
		FROM networks AS n
		WHERE n.project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1)
		AND n.name=?
	`)
	args := []any{projectName, networkName}

	if stateFilter > -1 {
		q.WriteString(" AND n.state=?")
		args = append(args, networkCreated)
	}

	q.WriteString(" LIMIT 1")

	err = tx.tx.QueryRowContext(ctx, q.String(), args...).Scan(&networkID, &network.Name, &network.Description, &networkState, &networkType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, -1, -1, nil, api.StatusErrorf(http.StatusNotFound, "Network not found")
		}

		return -1, -1, -1, nil, err
	}

	return networkID, networkState, networkType, &network, err
}

// networkPopulatePeerInfo takes a pointer to partially populated network info struct and enriches it.
// Returns the network cluster member info.
func (c *Cluster) networkPopulatePeerInfo(ctx context.Context, tx *ClusterTx, networkID int64, network *api.Network, networkState NetworkState, networkType NetworkType) (map[int64]NetworkNode, error) {
	var err error

	// Populate Status and Type fields by converting from DB values.
	network.Status = NetworkStateToAPIStatus(networkState)
	networkFillType(network, networkType)

	err = c.getNetworkConfig(ctx, tx, networkID, network)
	if err != nil {
		return nil, err
	}

	// Populate Location field.
	nodes, err := tx.NetworkNodes(ctx, networkID)
	if err != nil {
		return nil, err
	}

	network.Locations = make([]string, 0, len(nodes))
	for _, node := range nodes {
		network.Locations = append(network.Locations, node.Name)
	}

	return nodes, nil
}

// NetworkStateToAPIStatus converts DB NetworkState to API status string.
func NetworkStateToAPIStatus(state NetworkState) string {
	switch state {
	case networkPending:
		return api.NetworkStatusPending
	case networkCreated:
		return api.NetworkStatusCreated
	case networkErrored:
		return api.NetworkStatusErrored
	default:
		return api.NetworkStatusUnknown
	}
}

func networkFillType(network *api.Network, netType NetworkType) {
	switch netType {
	case NetworkTypeBridge:
		network.Type = "bridge"
	case NetworkTypeMacvlan:
		network.Type = "macvlan"
	case NetworkTypeSriov:
		network.Type = "sriov"
	case NetworkTypeOVN:
		network.Type = "ovn"
	case NetworkTypePhysical:
		network.Type = "physical"
	default:
		network.Type = "" // Unknown
	}
}

// GetNetworkWithInterface returns the network associated with the interface with the given name.
func (c *Cluster) GetNetworkWithInterface(devName string) (int64, *api.Network, error) {
	id := int64(-1)
	name := ""
	value := ""

	q := "SELECT networks.id, networks.name, networks_config.value FROM networks LEFT JOIN networks_config ON networks.id=networks_config.network_id WHERE networks_config.key=\"bridge.external_interfaces\" AND networks_config.node_id=?"
	arg1 := []any{c.nodeID}
	arg2 := []any{id, name, value}

	var result [][]any

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		var err error
		result, err = queryScan(ctx, tx, q, arg1, arg2)
		return err
	})
	if err != nil {
		return -1, nil, err
	}

	for _, r := range result {
		for _, entry := range strings.Split(r[2].(string), ",") {
			entry = strings.TrimSpace(entry)

			if entry == devName {
				id = r[0].(int64)
				name = r[1].(string)
			}
		}
	}

	if id == -1 {
		return -1, nil, fmt.Errorf("No network found for interface: %s", devName)
	}

	network := api.Network{
		Name:    name,
		Managed: true,
		Type:    "bridge",
	}

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		return c.getNetworkConfig(ctx, tx, id, &network)
	})
	if err != nil {
		return -1, nil, err
	}

	return id, &network, nil
}

// getNetworkConfig populates the config map of the Network with the given ID.
func (c *Cluster) getNetworkConfig(ctx context.Context, tx *ClusterTx, networkID int64, network *api.Network) error {
	q := `
        SELECT key, value
        FROM networks_config
		WHERE network_id=?
		AND (node_id=? OR node_id IS NULL)
	`

	network.Config = map[string]string{}

	return query.Scan(ctx, tx.Tx(), q, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := network.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for network ID %d", key, networkID)
		}

		network.Config[key] = value

		return nil
	}, networkID, c.nodeID)
}

// CreateNetwork creates a new network.
func (c *Cluster) CreateNetwork(projectName string, name string, description string, netType NetworkType, config map[string]string) (int64, error) {
	var id int64
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		// Insert a new network record with state networkCreated.
		result, err := tx.tx.Exec("INSERT INTO networks (project_id, name, description, state, type) VALUES ((SELECT id FROM projects WHERE name = ?), ?, ?, ?, ?)",
			projectName, name, description, networkCreated, netType)
		if err != nil {
			return err
		}

		id, err := result.LastInsertId()
		if err != nil {
			return err
		}

		// Insert a node-specific entry pointing to ourselves with state networkPending.
		columns := []string{"network_id", "node_id", "state"}
		values := []any{id, c.nodeID, networkPending}
		_, err = query.UpsertObject(tx.tx, "networks_nodes", columns, values)
		if err != nil {
			return err
		}

		err = networkConfigAdd(tx.tx, id, c.nodeID, config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		id = -1
	}

	return id, err
}

// UpdateNetwork updates the network with the given name.
func (c *Cluster) UpdateNetwork(project string, name, description string, config map[string]string) error {
	id, _, _, err := c.GetNetworkInAnyState(project, name)
	if err != nil {
		return err
	}

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		err = tx.UpdateNetwork(id, description, config)
		if err != nil {
			return err
		}

		return nil
	})

	return err
}

// Update the description of the network with the given ID.
func updateNetworkDescription(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE networks SET description=? WHERE id=?", description, id)
	return err
}

func networkConfigAdd(tx *sql.Tx, networkID, nodeID int64, config map[string]string) error {
	str := "INSERT INTO networks_config (network_id, node_id, key, value) VALUES(?, ?, ?, ?)"
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for k, v := range config {
		if v == "" {
			continue
		}

		var nodeIDValue any
		if !util.ValueInSlice(k, NodeSpecificNetworkConfig) {
			nodeIDValue = nil
		} else {
			nodeIDValue = nodeID
		}

		_, err = stmt.Exec(networkID, nodeIDValue, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

// Remove any the config of the network with the given ID
// associated with the node with the given ID.
func clearNetworkConfig(tx *sql.Tx, networkID, nodeID int64) error {
	_, err := tx.Exec(
		"DELETE FROM networks_config WHERE network_id=? AND (node_id=? OR node_id IS NULL)",
		networkID, nodeID)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetwork deletes the network with the given name.
func (c *Cluster) DeleteNetwork(project string, name string) error {
	id, _, _, err := c.GetNetworkInAnyState(project, name)
	if err != nil {
		return err
	}

	return c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		_, err := tx.tx.ExecContext(ctx, "DELETE FROM networks WHERE id=?", id)
		return err
	})
}

// RenameNetwork renames a network.
func (c *Cluster) RenameNetwork(project string, oldName string, newName string) error {
	id, _, _, err := c.GetNetworkInAnyState(project, oldName)
	if err != nil {
		return err
	}

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		_, err = tx.tx.Exec("UPDATE networks SET name=? WHERE id=?", newName, id)
		return err
	})

	return err
}

// NodeSpecificNetworkConfig lists all network config keys which are node-specific.
var NodeSpecificNetworkConfig = []string{
	"bgp.ipv4.nexthop",
	"bgp.ipv6.nexthop",
	"bridge.external_interfaces",
	"parent",
}
