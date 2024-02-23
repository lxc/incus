package cluster

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/cowsql/go-cowsql/app"
	"github.com/cowsql/go-cowsql/client"

	"github.com/lxc/incus/internal/server/certificate"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/node"
	"github.com/lxc/incus/internal/server/state"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/logger"
	localtls "github.com/lxc/incus/shared/tls"
	"github.com/lxc/incus/shared/util"
)

// clusterBusyError is returned by dqlite if attempting attempting to join a cluster at the same time as a role-change.
// This error tells us we can retry and probably join the cluster or fail due to something else.
// The error code here is SQLITE_BUSY.
var clusterBusyError = fmt.Errorf("A configuration change is already in progress (5)")

// Bootstrap turns a non-clustered server into the first (and leader)
// member of a new cluster.
//
// This instance must already have its cluster.https_address set and be listening
// on the associated network address.
func Bootstrap(state *state.State, gateway *Gateway, serverName string) error {
	// Check parameters
	if serverName == "" {
		return fmt.Errorf("Server name must not be empty")
	}

	err := membershipCheckNoLeftoverClusterCert(state.OS.VarDir)
	if err != nil {
		return err
	}

	var localClusterAddress string

	err = state.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		// Fetch current network address and raft nodes
		config, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to fetch node configuration: %w", err)
		}

		localClusterAddress = config.ClusterAddress()

		// Make sure node-local database state is in order.
		err = membershipCheckNodeStateForBootstrapOrJoin(ctx, tx, localClusterAddress)
		if err != nil {
			return err
		}

		// Add ourselves as first raft node
		err = tx.CreateFirstRaftNode(localClusterAddress, serverName)
		if err != nil {
			return fmt.Errorf("Failed to insert first raft node: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Update our own entry in the nodes table.
	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Make sure cluster database state is in order.
		err := membershipCheckClusterStateForBootstrapOrJoin(ctx, tx)
		if err != nil {
			return err
		}

		// Add ourselves to the nodes table.
		err = tx.BootstrapNode(serverName, localClusterAddress)
		if err != nil {
			return fmt.Errorf("Failed updating cluster member: %w", err)
		}

		err = EnsureServerCertificateTrusted(serverName, state.ServerCert(), tx)
		if err != nil {
			return fmt.Errorf("Failed ensuring server certificate is trusted: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Reload the trusted certificate cache to enable the certificate we just added to the local trust store
	// to be used when validating endpoint connections. This will allow Dqlite to connect to ourselves.
	state.UpdateCertificateCache()

	// Shutdown the gateway. This will trash any dqlite connection against
	// our in-memory dqlite driver and shutdown the associated raft
	// instance. We also lock regular access to the cluster database since
	// we don't want any other database code to run while we're
	// reconfiguring raft.
	err = state.DB.Cluster.EnterExclusive()
	if err != nil {
		return fmt.Errorf("Failed to acquire cluster database lock: %w", err)
	}

	err = gateway.Shutdown()
	if err != nil {
		return fmt.Errorf("Failed to shutdown gRPC SQL gateway: %w", err)
	}

	// The cluster CA certificate is a symlink against the regular server CA certificate.
	if util.PathExists(filepath.Join(state.OS.VarDir, "server.ca")) {
		err := os.Symlink("server.ca", filepath.Join(state.OS.VarDir, "cluster.ca"))
		if err != nil {
			return fmt.Errorf("Failed to symlink server CA cert to cluster CA cert: %w", err)
		}
	}

	// Generate a new cluster certificate.
	clusterCert, err := internalUtil.LoadClusterCert(state.OS.VarDir)
	if err != nil {
		return fmt.Errorf("Failed to create cluster cert: %w", err)
	}

	// If endpoint listeners are active, apply new cluster certificate.
	if state.Endpoints != nil {
		gateway.networkCert = clusterCert
		state.Endpoints.NetworkUpdateCert(clusterCert)
	}

	// Re-initialize the gateway. This will create a new raft factory an
	// dqlite driver instance, which will be exposed over gRPC by the
	// gateway handlers.
	err = gateway.init(true)
	if err != nil {
		return fmt.Errorf("Failed to re-initialize gRPC SQL gateway: %w", err)
	}

	err = gateway.WaitLeadership()
	if err != nil {
		return err
	}

	// Make sure we can actually connect to the cluster database through
	// the network endpoint. This also releases the previously acquired
	// lock and makes the Go SQL pooling system invalidate the old
	// connection, so new queries will be executed over the new network
	// connection.
	err = state.DB.Cluster.ExitExclusive(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Cluster database initialization failed: %w", err)
	}

	return nil
}

// EnsureServerCertificateTrusted adds the serverCert to the DB trusted certificates store using the serverName.
// If a certificate with the same fingerprint is already in the trust store, but is of the wrong type or name then
// the existing certificate is updated to the correct type and name. If the existing certificate is the correct
// type but the wrong name then an error is returned. And if the existing certificate is the correct type and name
// then nothing more is done.
func EnsureServerCertificateTrusted(serverName string, serverCert *localtls.CertInfo, tx *db.ClusterTx) error {
	// Parse our server certificate and prepare to add it to DB trust store.
	serverCertx509, err := x509.ParseCertificate(serverCert.KeyPair().Certificate[0])
	if err != nil {
		return err
	}

	fingerprint := localtls.CertFingerprint(serverCertx509)

	dbCert := cluster.Certificate{
		Fingerprint: fingerprint,
		Type:        certificate.TypeServer, // Server type for intra-member communication.
		Name:        serverName,
		Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCertx509.Raw})),
	}

	// Add our server cert to the DB trust store (so when other members join this cluster they will be
	// able to trust intra-cluster requests from this member).
	ctx := context.Background()
	existingCert, _ := cluster.GetCertificate(ctx, tx.Tx(), dbCert.Fingerprint)
	if existingCert != nil {
		if existingCert.Name != dbCert.Name && existingCert.Type == certificate.TypeServer {
			// Don't alter an existing server certificate that has our fingerprint but not our name.
			// Something is wrong as this shouldn't happen.
			return fmt.Errorf("Existing server certificate with different name %q already in trust store", existingCert.Name)
		} else if existingCert.Name != dbCert.Name && existingCert.Type != certificate.TypeServer {
			// Ensure that if a client certificate already exists that matches our fingerprint, that it
			// has the correct name and type for cluster operation, to allow us to associate member
			// server names to certificate names.
			err = cluster.UpdateCertificate(ctx, tx.Tx(), dbCert.Fingerprint, dbCert)
			if err != nil {
				return fmt.Errorf("Failed updating certificate name and type in trust store: %w", err)
			}
		}
	} else {
		_, err = cluster.CreateCertificate(ctx, tx.Tx(), dbCert)
		if err != nil {
			return fmt.Errorf("Failed adding server certifcate to trust store: %w", err)
		}
	}

	return nil
}

// Accept a new node and add it to the cluster.
//
// This instance must already be clustered.
//
// Return an updated list raft database nodes (possibly including the newly
// accepted node).
func Accept(state *state.State, gateway *Gateway, name, address string, schema, api, arch int) ([]db.RaftNode, error) {
	// Check parameters
	if name == "" {
		return nil, fmt.Errorf("Member name must not be empty")
	}

	if address == "" {
		return nil, fmt.Errorf("Member address must not be empty")
	}

	// Insert the new node into the nodes table.
	var id int64
	err := state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the node can be accepted with these parameters.
		err := membershipCheckClusterStateForAccept(ctx, tx, name, address, schema, api)
		if err != nil {
			return err
		}

		// Add the new node.
		id, err = tx.CreateNodeWithArch(name, address, arch)
		if err != nil {
			return fmt.Errorf("Failed to insert new node into the database: %w", err)
		}

		// Mark the node as pending, so it will be skipped when
		// performing heartbeats or sending cluster
		// notifications.
		err = tx.SetNodePendingFlag(id, true)
		if err != nil {
			return fmt.Errorf("Failed to mark the new node as pending: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Possibly insert the new node into the raft_nodes table (if we have
	// less than 3 database nodes).
	nodes, err := gateway.currentRaftNodes()
	if err != nil {
		return nil, fmt.Errorf("Failed to get raft nodes from the log: %w", err)
	}

	count := len(nodes) // Existing nodes
	voters := 0
	standbys := 0
	for _, node := range nodes {
		switch node.Role {
		case db.RaftVoter:
			voters++
		case db.RaftStandBy:
			standbys++
		}
	}

	node := db.RaftNode{
		NodeInfo: client.NodeInfo{
			ID:      uint64(id),
			Address: address,
			Role:    db.RaftSpare,
		},
		Name: name,
	}

	if count > 1 && voters < int(state.GlobalConfig.MaxVoters()) {
		node.Role = db.RaftVoter
	} else if standbys < int(state.GlobalConfig.MaxStandBy()) {
		node.Role = db.RaftStandBy
	}

	nodes = append(nodes, node)

	return nodes, nil
}

// Join makes a non-clustered server join an existing cluster.
//
// It's assumed that Accept() was previously called against the leader node,
// which handed the raft server ID.
//
// The cert parameter must contain the keypair/CA material of the cluster being
// joined.
func Join(state *state.State, gateway *Gateway, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, name string, raftNodes []db.RaftNode) error {
	// Check parameters
	if name == "" {
		return fmt.Errorf("Member name must not be empty")
	}

	var localClusterAddress string
	err := state.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		// Fetch current network address and raft nodes
		config, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to fetch node configuration: %w", err)
		}

		localClusterAddress = config.ClusterAddress()

		// Make sure node-local database state is in order.
		err = membershipCheckNodeStateForBootstrapOrJoin(ctx, tx, localClusterAddress)
		if err != nil {
			return err
		}

		// Set the raft nodes list to the one that was returned by Accept().
		err = tx.ReplaceRaftNodes(raftNodes)
		if err != nil {
			return fmt.Errorf("Failed to set raft nodes: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Get the local config keys for the cluster pools and networks. It
	// assumes that the local storage pools and networks match the cluster
	// networks, if not an error will be returned. Also get any outstanding
	// operation, typically there will be just one, created by the POST
	// /cluster/nodes request which triggered this code.
	var pools map[string]map[string]string
	var networks map[string]map[string]string
	var operations []cluster.Operation

	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		pools, err = tx.GetStoragePoolsLocalConfig(ctx)
		if err != nil {
			return err
		}

		networks, err = tx.GetNetworksLocalConfig(ctx)
		if err != nil {
			return err
		}

		nodeID := tx.GetNodeID()
		filter := cluster.OperationFilter{NodeID: &nodeID}
		operations, err = cluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Lock regular access to the cluster database since we don't want any
	// other database code to run while we're reconfiguring raft.
	err = state.DB.Cluster.EnterExclusive()
	if err != nil {
		return fmt.Errorf("Failed to acquire cluster database lock: %w", err)
	}

	// Shutdown the gateway and wipe any raft data. This will trash any
	// gRPC SQL connection against our in-memory dqlite driver and shutdown
	// the associated raft instance.
	err = gateway.Shutdown()
	if err != nil {
		return fmt.Errorf("Failed to shutdown gRPC SQL gateway: %w", err)
	}

	err = os.RemoveAll(state.OS.GlobalDatabaseDir())
	if err != nil {
		return fmt.Errorf("Failed to remove existing raft data: %w", err)
	}

	// Re-initialize the gateway. This will create a new raft factory an
	// dqlite driver instance, which will be exposed over gRPC by the
	// gateway handlers.
	gateway.networkCert = networkCert
	err = gateway.init(false)
	if err != nil {
		return fmt.Errorf("Failed to re-initialize gRPC SQL gateway: %w", err)
	}

	// If we are listed among the database nodes, join the raft cluster.
	var info *db.RaftNode
	for _, node := range raftNodes {
		if node.Address == localClusterAddress {
			info = &node
		}
	}

	if info == nil {
		panic("Joining member not found")
	}

	logger.Info("Joining dqlite raft cluster", logger.Ctx{"id": info.ID, "local": info.Address, "role": info.Role})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := client.FindLeader(
		ctx, gateway.NodeStore(),
		client.WithDialFunc(gateway.raftDial()),
		client.WithLogFunc(DqliteLog),
	)
	if err != nil {
		return fmt.Errorf("Failed to connect to cluster leader: %w", err)
	}

	defer func() { _ = client.Close() }()

	logger.Info("Adding node to cluster", logger.Ctx{"id": info.ID, "local": info.Address, "role": info.Role})
	ctx, cancel = context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Repeatedly try to join in case the cluster is busy with a role-change.
	joined := false
	for !joined {
		select {
		case <-ctx.Done():
			return fmt.Errorf("Failed to join cluster: %w", ctx.Err())
		default:
			err = client.Add(ctx, info.NodeInfo)
			if err != nil && err.Error() == clusterBusyError.Error() {
				// If the cluster is busy with a role change, sleep a second and then keep trying to join.
				time.Sleep(1 * time.Second)
				continue
			}

			if err != nil {
				return fmt.Errorf("Failed to join cluster: %w", err)
			}

			joined = true
		}
	}

	// Make sure we can actually connect to the cluster database through
	// the network endpoint. This also releases the previously acquired
	// lock and makes the Go SQL pooling system invalidate the old
	// connection, so new queries will be executed over the new gRPC
	// network connection. Also, update the storage_pools and networks
	// tables with our local configuration.
	logger.Info("Migrate local data to cluster database")
	err = state.DB.Cluster.ExitExclusive(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		node, err := tx.GetPendingNodeByAddress(ctx, localClusterAddress)
		if err != nil {
			return fmt.Errorf("Failed to get ID of joining node: %w", err)
		}

		state.DB.Cluster.NodeID(node.ID)
		tx.NodeID(node.ID)

		// Storage pools.
		ids, err := tx.GetNonPendingStoragePoolsNamesToIDs(ctx)
		if err != nil {
			return fmt.Errorf("Failed to get cluster storage pool IDs: %w", err)
		}

		for name, id := range ids {
			err := tx.UpdateStoragePoolAfterNodeJoin(id, node.ID)
			if err != nil {
				return fmt.Errorf("Failed to add joining node's to the pool: %w", err)
			}

			driver, err := tx.GetStoragePoolDriver(ctx, id)
			if err != nil {
				return fmt.Errorf("Failed to get storage pool driver: %w", err)
			}

			// For all pools we add the config provided by the joining node.
			config, ok := pools[name]
			if !ok {
				return fmt.Errorf("Joining member has no config for pool %s", name)
			}

			err = tx.CreateStoragePoolConfig(id, node.ID, config)
			if err != nil {
				return fmt.Errorf("Failed to add joining node's pool config: %w", err)
			}

			if slices.Contains(db.StorageRemoteDriverNames(), driver) {
				// For remote pools we have to create volume entries for the joining node.
				err := tx.UpdateRemoteStoragePoolAfterNodeJoin(ctx, id, node.ID)
				if err != nil {
					return fmt.Errorf("Failed to create remote volumes for joining node: %w", err)
				}
			}
		}

		// Networks.
		netids, err := tx.GetNonPendingNetworkIDs(ctx)
		if err != nil {
			return fmt.Errorf("Failed to get cluster network IDs: %w", err)
		}

		for _, network := range netids {
			for name, id := range network {
				config, ok := networks[name]
				if !ok {
					return fmt.Errorf("Joining member has no config for network %s", name)
				}

				err := tx.NetworkNodeJoin(id, node.ID)
				if err != nil {
					return fmt.Errorf("Failed to add joining node's to the network: %w", err)
				}

				err = tx.CreateNetworkConfig(id, node.ID, config)
				if err != nil {
					return fmt.Errorf("Failed to add joining node's network config: %w", err)
				}
			}
		}

		// Migrate outstanding operations.
		for _, operation := range operations {
			op := cluster.Operation{
				UUID:   operation.UUID,
				Type:   operation.Type,
				NodeID: tx.GetNodeID(),
			}

			_, err := cluster.CreateOrReplaceOperation(ctx, tx.Tx(), op)
			if err != nil {
				return fmt.Errorf("Failed to migrate operation %s: %w", operation.UUID, err)
			}
		}

		// Remove the pending flag for ourselves
		// notifications.
		err = tx.SetNodePendingFlag(node.ID, false)
		if err != nil {
			return fmt.Errorf("Failed to unmark the node as pending: %w", err)
		}

		// Set last heartbeat time to now, as member is clearly online as it just successfully joined,
		// that way when we send the notification to all members below it will consider this member online.
		err = tx.SetNodeHeartbeat(node.Address, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("Failed setting last heartbeat time for member: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Cluster database initialization failed: %w", err)
	}

	// Generate partial heartbeat request containing just a raft node list.
	if state.Endpoints != nil {
		NotifyHeartbeat(state, gateway)
	}

	return nil
}

// NotifyHeartbeat attempts to send a heartbeat to all other members to notify them of a new or changed member.
func NotifyHeartbeat(state *state.State, gateway *Gateway) {
	// If a heartbeat round is already running (and implicitly this means we are the leader), then cancel it
	// so we can distribute the fresh member state info.
	heartbeatCancel := gateway.HearbeatCancelFunc()
	if heartbeatCancel != nil {
		heartbeatCancel()

		// Wait for heartbeat to finish and then release.
		// Ignore staticcheck "SA2001: empty critical section" because we want to wait for the lock.
		gateway.HeartbeatLock.Lock()
		gateway.HeartbeatLock.Unlock() //nolint:staticcheck
	}

	hbState := NewAPIHearbeat(state.DB.Cluster)
	hbState.Time = time.Now().UTC()

	var err error
	var raftNodes []db.RaftNode
	var localClusterAddress string
	err = state.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		raftNodes, err = tx.GetRaftNodes(ctx)
		if err != nil {
			return err
		}

		config, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		localClusterAddress = config.ClusterAddress()

		return nil
	})
	if err != nil {
		logger.Warn("Failed to get current raft members", logger.Ctx{"err": err, "local": localClusterAddress})
		return
	}

	var members []db.NodeInfo
	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err = tx.GetNodes(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		logger.Warn("Failed to get current cluster members", logger.Ctx{"err": err, "local": localClusterAddress})
		return
	}

	// Setup a full-state notification heartbeat.
	hbState.Update(true, raftNodes, members, gateway.HeartbeatOfflineThreshold)

	var wg sync.WaitGroup

	// Refresh local event listeners.
	wg.Add(1)
	go func() {
		EventsUpdateListeners(state.Endpoints, state.DB.Cluster, state.ServerCert, hbState.Members, state.Events.Inject)
		wg.Done()
	}()

	// Notify all other members of the change in membership.
	logger.Info("Sending member change notification heartbeat to all members", logger.Ctx{"local": localClusterAddress})
	for _, member := range members {
		if member.Address == localClusterAddress {
			continue
		}

		wg.Add(1)
		go func(address string) {
			_ = HeartbeatNode(context.Background(), address, state.Endpoints.NetworkCert(), state.ServerCert(), hbState)
			wg.Done()
		}(member.Address)
	}

	// Wait until all members have been notified (or at least have had a change to be notified).
	wg.Wait()
}

// Rebalance the raft cluster, trying to see if we have a spare online node
// that we can promote to voter node if we are below membershipMaxRaftVoters,
// or to standby if we are below membershipMaxStandBys.
//
// If there's such spare node, return its address as well as the new list of
// raft nodes.
func Rebalance(state *state.State, gateway *Gateway, unavailableMembers []string) (string, []db.RaftNode, error) {
	// If we're a standalone node, do nothing.
	if gateway.memoryDial != nil {
		return "", nil, nil
	}

	nodes, err := gateway.currentRaftNodes()
	if err != nil {
		return "", nil, fmt.Errorf("Get current raft nodes: %w", err)
	}

	roles, err := newRolesChanges(state, gateway, nodes, unavailableMembers)
	if err != nil {
		return "", nil, err
	}

	role, candidates := roles.Adjust(gateway.info.ID)

	if role == -1 {
		// No node to promote
		return "", nodes, nil
	}

	localClusterAddress := state.LocalConfig.ClusterAddress()

	// Check if we have a spare node that we can promote to the missing role.
	candidateAddress := candidates[0].Address
	logger.Info("Found cluster member whose role needs to be changed", logger.Ctx{"candidateAddress": candidateAddress, "newRole": role, "local": localClusterAddress})

	for i, node := range nodes {
		if node.Address == candidateAddress {
			nodes[i].Role = role
			break
		}
	}

	return candidateAddress, nodes, nil
}

// Assign a new role to the local dqlite node.
func Assign(state *state.State, gateway *Gateway, nodes []db.RaftNode) error {
	// Figure out our own address.
	address := ""
	err := state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		address, err = tx.GetLocalNodeAddress(ctx)
		if err != nil {
			return fmt.Errorf("Failed to fetch the address of this cluster member: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Ensure we actually have an address.
	if address == "" {
		return fmt.Errorf("Cluster member is not exposed on the network")
	}

	// Figure out our node identity.
	var info *db.RaftNode
	for i, node := range nodes {
		if node.Address == address {
			info = &nodes[i]
		}
	}

	// Ensure that our address was actually included in the given list of raft nodes.
	if info == nil {
		return fmt.Errorf("This member is not included in the given list of database nodes")
	}

	// Replace our local list of raft nodes with the given one (which
	// includes ourselves).
	err = state.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		err = tx.ReplaceRaftNodes(nodes)
		if err != nil {
			return fmt.Errorf("Failed to set raft nodes: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	var transactor func(context.Context, func(ctx context.Context, tx *db.ClusterTx) error) error

	// If we are already running a dqlite node, it means we have cleanly
	// joined the cluster before, using the roles support API. In that case
	// there's no need to restart the gateway and we can just change our
	// dqlite role.
	if gateway.IsDqliteNode() {
		transactor = state.DB.Cluster.Transaction
		goto assign
	}

	// If we get here it means that we are an upgraded node from cluster
	// without roles support, or we didn't cleanly join the cluster. Either
	// way, we don't have a dqlite node running, so we need to restart the
	// gateway.

	// Lock regular access to the cluster database since we don't want any
	// other database code to run while we're reconfiguring raft.
	err = state.DB.Cluster.EnterExclusive()
	if err != nil {
		return fmt.Errorf("Failed to acquire cluster database lock: %w", err)
	}

	transactor = state.DB.Cluster.ExitExclusive

	// Wipe all existing raft data, for good measure (perhaps they were
	// somehow leftover).
	err = os.RemoveAll(state.OS.GlobalDatabaseDir())
	if err != nil {
		return fmt.Errorf("Failed to remove existing raft data: %w", err)
	}

	// Re-initialize the gateway. This will create a new raft factory an
	// dqlite driver instance, which will be exposed over gRPC by the
	// gateway handlers.
	err = gateway.init(false)
	if err != nil {
		return fmt.Errorf("Failed to re-initialize gRPC SQL gateway: %w", err)
	}

assign:
	logger.Info("Changing local dqlite raft role", logger.Ctx{"id": info.ID, "local": info.Address, "role": info.Role})

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	client, err := client.FindLeader(ctx, gateway.NodeStore(), client.WithDialFunc(gateway.raftDial()))
	if err != nil {
		return fmt.Errorf("Connect to cluster leader: %w", err)
	}

	defer func() { _ = client.Close() }()

	// Figure out our current role.
	role := db.RaftRole(-1)
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("Fetch current cluster configuration: %w", err)
	}

	for _, server := range cluster {
		if server.ID == info.ID {
			role = server.Role
			break
		}
	}
	if role == -1 {
		return fmt.Errorf("Node %s does not belong to the current raft configuration", address)
	}

	// If we're stepping back from voter to spare, let's first transition
	// to stand-by first and wait for the configuration change to be
	// notified to us. This prevent us from thinking we're still voters and
	// potentially disrupt the cluster.
	if role == db.RaftVoter && info.Role == db.RaftSpare {
		err = client.Assign(ctx, info.ID, db.RaftStandBy)
		if err != nil {
			return fmt.Errorf("Failed to step back to stand-by: %w", err)
		}

		local, err := gateway.getClient()
		if err != nil {
			return fmt.Errorf("Failed to get local dqlite client: %w", err)
		}

		notified := false
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			servers, err := local.Cluster(context.Background())
			if err != nil {
				return fmt.Errorf("Failed to get current cluster: %w", err)
			}

			for _, server := range servers {
				if server.ID != info.ID {
					continue
				}

				if server.Role == db.RaftStandBy {
					notified = true
					break
				}
			}
			if notified {
				break
			}
		}
		if !notified {
			return fmt.Errorf("Timeout waiting for configuration change notification")
		}
	}

	// Give the Assign operation a bit more budget in case we're promoting
	// to voter, since that might require a snapshot transfer.
	if info.Role == db.RaftVoter {
		ctx, cancel = context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
	}

	err = client.Assign(ctx, info.ID, info.Role)
	if err != nil {
		return fmt.Errorf("Failed to assign role: %w", err)
	}

	gateway.info = info

	// Unlock regular access to our cluster database.
	err = transactor(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return nil
	})
	if err != nil {
		return fmt.Errorf("Cluster database initialization failed: %w", err)
	}

	// Generate partial heartbeat request containing just a raft node list.
	if state.Endpoints != nil {
		NotifyHeartbeat(state, gateway)
	}

	return nil
}

// Leave a cluster.
//
// If the force flag is true, the node will leave even if it still has
// containers and images.
//
// The node will only leave the raft cluster, and won't be removed from the
// database. That's done by Purge().
//
// Upon success, return the address of the leaving node.
//
// This function must be called by the cluster leader.
func Leave(state *state.State, gateway *Gateway, name string, force bool) (string, error) {
	logger.Debugf("Make node %s leave the cluster", name)

	// Check if the node can be deleted and track its address.
	var address string
	err := state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the node (if it doesn't exists an error is returned).
		node, err := tx.GetNodeByName(ctx, name)
		if err != nil {
			return err
		}

		// Check that the node is eligeable for leaving.
		if !force {
			err := membershipCheckClusterStateForLeave(ctx, tx, node.ID)
			if err != nil {
				return err
			}
		}

		address = node.Address
		return nil
	})
	if err != nil {
		return "", err
	}

	nodes, err := gateway.currentRaftNodes()
	if err != nil {
		return "", err
	}

	var info *db.RaftNode // Raft node to remove, if any.
	for i, node := range nodes {
		if node.Address == address {
			info = &nodes[i]
			break
		}
	}

	if info == nil {
		// The node was not part of the raft cluster, nothing left to do.
		return address, nil
	}

	// Get the address of another database node,
	logger.Info(
		"Remove node from dqlite raft cluster",
		logger.Ctx{"id": info.ID, "address": info.Address})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := gateway.getClient()
	if err != nil {
		return "", fmt.Errorf("Failed to connect to cluster leader: %w", err)
	}

	defer func() { _ = client.Close() }()
	err = client.Remove(ctx, info.ID)
	if err != nil {
		return "", fmt.Errorf("Failed to leave the cluster: %w", err)
	}

	return address, nil
}

// Handover looks for a non-voter member that can be promoted to replace a the
// member with the given address, which is shutting down. It returns the
// address of such member along with an updated list of nodes, with the ne role
// set.
//
// It should be called only by the current leader.
func Handover(state *state.State, gateway *Gateway, address string) (string, []db.RaftNode, error) {
	nodes, err := gateway.currentRaftNodes()
	if err != nil {
		return "", nil, fmt.Errorf("Get current raft nodes: %w", err)
	}

	var nodeID uint64
	for _, node := range nodes {
		if node.Address == address {
			nodeID = node.ID
		}
	}

	if nodeID == 0 {
		return "", nil, fmt.Errorf("No dqlite node has address %s: %w", address, err)
	}

	roles, err := newRolesChanges(state, gateway, nodes, nil)
	if err != nil {
		return "", nil, err
	}

	role, candidates := roles.Handover(nodeID)
	if role == -1 {
		return "", nil, nil
	}

	for i, node := range nodes {
		if node.Address == candidates[0].Address {
			nodes[i].Role = role
			return node.Address, nodes, nil
		}
	}

	return "", nil, nil
}

// Build an app.RolesChanges object feeded with the current cluster state.
func newRolesChanges(state *state.State, gateway *Gateway, nodes []db.RaftNode, unavailableMembers []string) (*app.RolesChanges, error) {
	var domains map[string]uint64
	err := state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		domains, err = tx.GetNodesFailureDomains(ctx)
		if err != nil {
			return fmt.Errorf("Load failure domains: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	cluster := map[client.NodeInfo]*client.NodeMetadata{}

	for _, node := range nodes {
		if !slices.Contains(unavailableMembers, node.Address) && HasConnectivity(gateway.networkCert, gateway.state().ServerCert(), node.Address) {
			cluster[node.NodeInfo] = &client.NodeMetadata{
				FailureDomain: domains[node.Address],
			}
		} else {
			cluster[node.NodeInfo] = nil
		}
	}

	roles := &app.RolesChanges{
		Config: app.RolesConfig{
			Voters:   int(state.GlobalConfig.MaxVoters()),
			StandBys: int(state.GlobalConfig.MaxStandBy()),
		},
		State: cluster,
	}

	return roles, nil
}

// Purge removes a node entirely from the cluster database.
func Purge(c *db.Cluster, name string) error {
	logger.Debugf("Remove node %s from the database", name)

	return c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the node (if it doesn't exists an error is returned).
		node, err := tx.GetNodeByName(ctx, name)
		if err != nil {
			return fmt.Errorf("Failed to get member %q: %w", name, err)
		}

		err = tx.ClearNode(ctx, node.ID)
		if err != nil {
			return fmt.Errorf("Failed to clear member %q: %w", name, err)
		}

		err = tx.RemoveNode(node.ID)
		if err != nil {
			return fmt.Errorf("Failed to remove member %q: %w", name, err)
		}

		err = cluster.DeleteCertificates(context.Background(), tx.Tx(), name, certificate.TypeServer)
		if err != nil {
			return fmt.Errorf("Failed to remove member %q certificate from trust store: %w", name, err)
		}

		return nil
	})
}

// Count is a convenience for checking the current number of nodes in the
// cluster.
func Count(state *state.State) (int, error) {
	var count int
	err := state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		count, err = tx.GetNodesCount(ctx)
		return err
	})

	return count, err
}

// Enabled is a convenience that returns true if clustering is enabled on this
// node.
func Enabled(node *db.Node) (bool, error) {
	enabled := false
	err := node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		addresses, err := tx.GetRaftNodeAddresses(ctx)
		if err != nil {
			return err
		}

		enabled = len(addresses) > 0
		return nil
	})

	return enabled, err
}

// Check that node-related preconditions are met for bootstrapping or joining a
// cluster.
func membershipCheckNodeStateForBootstrapOrJoin(ctx context.Context, tx *db.NodeTx, address string) error {
	nodes, err := tx.GetRaftNodes(ctx)
	if err != nil {
		return fmt.Errorf("Failed to fetch current raft nodes: %w", err)
	}

	hasClusterAddress := address != ""
	hasRaftNodes := len(nodes) > 0

	// Ensure that we're not in an inconsistent situation, where no cluster address is set, but still there
	// are entries in the raft_nodes table.
	if !hasClusterAddress && hasRaftNodes {
		return fmt.Errorf("Inconsistent state: found leftover entries in raft_nodes")
	}

	if !hasClusterAddress {
		return fmt.Errorf("No cluster.https_address config is set on this member")
	}

	if hasRaftNodes {
		return fmt.Errorf("The member is already part of a cluster")
	}

	return nil
}

// Check that cluster-related preconditions are met for bootstrapping or
// joining a cluster.
func membershipCheckClusterStateForBootstrapOrJoin(ctx context.Context, tx *db.ClusterTx) error {
	members, err := tx.GetNodes(ctx)
	if err != nil {
		return fmt.Errorf("Failed getting cluster members: %w", err)
	}

	if len(members) != 1 {
		return fmt.Errorf("Inconsistent state: Found leftover entries in cluster members")
	}

	return nil
}

// Check that cluster-related preconditions are met for accepting a new node.
func membershipCheckClusterStateForAccept(ctx context.Context, tx *db.ClusterTx, name string, address string, schema int, api int) error {
	members, err := tx.GetNodes(ctx)
	if err != nil {
		return fmt.Errorf("Failed getting cluster members: %w", err)
	}

	if len(members) == 1 && members[0].Address == "0.0.0.0" {
		return fmt.Errorf("Clustering isn't enabled")
	}

	for _, member := range members {
		if member.Name == name {
			return fmt.Errorf("The cluster already has a member with name: %s", name)
		}

		if member.Address == address {
			return fmt.Errorf("The cluster already has a member with address: %s", address)
		}

		if member.Schema != schema {
			return fmt.Errorf("The joining server version doesn't match (expected %s with DB schema %v)", version.Version, schema)
		}

		if member.APIExtensions != api {
			return fmt.Errorf("The joining server version doesn't match (expected %s with API count %v)", version.Version, api)
		}
	}

	return nil
}

// Check that cluster-related preconditions are met for leaving a cluster.
func membershipCheckClusterStateForLeave(ctx context.Context, tx *db.ClusterTx, nodeID int64) error {
	// Check that it has no containers or images.
	message, err := tx.NodeIsEmpty(ctx, nodeID)
	if err != nil {
		return err
	}

	if message != "" {
		return fmt.Errorf(message)
	}

	// Check that it's not the last member.
	members, err := tx.GetNodes(ctx)
	if err != nil {
		return fmt.Errorf("Failed getting cluster members: %w", err)
	}

	if len(members) == 1 {
		return fmt.Errorf("Member is the only member in the cluster")
	}

	return nil
}

// Check that there is no left-over cluster certificate in the var dir of this server.
func membershipCheckNoLeftoverClusterCert(dir string) error {
	// Ensure that there's no leftover cluster certificate.
	for _, basename := range []string{"cluster.crt", "cluster.key", "cluster.ca"} {
		if util.PathExists(filepath.Join(dir, basename)) {
			return fmt.Errorf("Inconsistent state: found leftover cluster certificate")
		}
	}

	return nil
}

// SchemaVersion holds the version of the cluster database schema.
var SchemaVersion = cluster.SchemaVersion
