package cluster_test

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cowsql/go-cowsql/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/internal/server/certificate"
	"github.com/lxc/incus/internal/server/cluster"
	clusterConfig "github.com/lxc/incus/internal/server/cluster/config"
	"github.com/lxc/incus/internal/server/db"
	dbCluster "github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/osarch"
	localtls "github.com/lxc/incus/shared/tls"
	"github.com/lxc/incus/shared/util"
)

func TestBootstrap_UnmetPreconditions(t *testing.T) {
	cases := []struct {
		setup func(*membershipFixtures)
		error string
	}{
		{
			func(f *membershipFixtures) {
				f.ClusterAddress("1.2.3.4:666")
				f.RaftNode("5.6.7.8:666")
				filename := filepath.Join(f.state.OS.VarDir, "cluster.crt")
				_ = os.WriteFile(filename, []byte{}, 0644)
			},
			"Inconsistent state: found leftover cluster certificate",
		},
		{
			func(*membershipFixtures) {},
			"No cluster.https_address config is set on this member",
		},
		{
			func(f *membershipFixtures) {
				f.ClusterAddress("1.2.3.4:666")
				f.RaftNode("5.6.7.8:666")
			},
			"The member is already part of a cluster",
		},
		{
			func(f *membershipFixtures) {
				f.RaftNode("5.6.7.8:666")
			},
			"Inconsistent state: found leftover entries in raft_nodes",
		},
		{
			func(f *membershipFixtures) {
				f.ClusterAddress("1.2.3.4:666")
				f.ClusterNode("5.6.7.8:666")
			},
			"Inconsistent state: Found leftover entries in cluster members",
		},
	}

	for _, c := range cases {
		t.Run(c.error, func(t *testing.T) {
			state, cleanup := state.NewTestState(t)
			defer cleanup()

			c.setup(&membershipFixtures{t: t, state: state})

			serverCert := localtls.TestingKeyPair()
			state.ServerCert = func() *localtls.CertInfo { return serverCert }

			gateway := newGateway(t, state.DB.Node, serverCert, state)
			defer func() { _ = gateway.Shutdown() }()

			err := cluster.Bootstrap(state, gateway, "buzz")
			assert.EqualError(t, err, c.error)
		})
	}
}

func TestBootstrap(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	serverCert := localtls.TestingKeyPair()
	state.ServerCert = func() *localtls.CertInfo { return serverCert }

	gateway := newGateway(t, state.DB.Node, serverCert, state)
	defer func() { _ = gateway.Shutdown() }()

	mux := http.NewServeMux()
	server := newServer(serverCert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	f := &membershipFixtures{t: t, state: state}
	f.ClusterAddress(address)

	err := cluster.Bootstrap(state, gateway, "buzz")
	require.NoError(t, err)

	// The node-local database has now an entry in the raft_nodes table
	err = state.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		nodes, err := tx.GetRaftNodes(ctx)
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, uint64(1), nodes[0].ID)
		assert.Equal(t, address, nodes[0].Address)
		return nil
	})
	require.NoError(t, err)

	// The cluster database has now an entry in the nodes table
	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		require.NoError(t, err)
		require.Len(t, members, 1)
		assert.Equal(t, "buzz", members[0].Name)
		assert.Equal(t, address, members[0].Address)
		return nil
	})
	require.NoError(t, err)

	// The cluster certificate is in place.
	assert.True(t, util.PathExists(filepath.Join(state.OS.VarDir, "cluster.crt")))

	trustedCerts := func() map[certificate.Type]map[string]x509.Certificate {
		return nil
	}

	// The dqlite driver is now exposed over the network.
	for path, handler := range gateway.HandlerFuncs(nil, trustedCerts) {
		mux.HandleFunc(path, handler)
	}

	count, err := cluster.Count(state)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	enabled, err := cluster.Enabled(state.DB.Node)
	require.NoError(t, err)
	assert.True(t, enabled)
}

// If pre-conditions are not met, a descriptive error is returned.
func TestAccept_UnmetPreconditions(t *testing.T) {
	cases := []struct {
		name    string
		address string
		schema  int
		api     int
		setup   func(*membershipFixtures)
		error   string
	}{
		{
			"buzz",
			"1.2.3.4:666",
			cluster.SchemaVersion,
			len(version.APIExtensions),
			func(f *membershipFixtures) {},
			"Clustering isn't enabled",
		},
		{
			"rusp",
			"1.2.3.4:666",
			cluster.SchemaVersion,
			len(version.APIExtensions),
			func(f *membershipFixtures) {
				f.ClusterNode("5.6.7.8:666")
			},
			"The cluster already has a member with name: rusp",
		},
		{
			"buzz",
			"5.6.7.8:666",
			cluster.SchemaVersion,
			len(version.APIExtensions),
			func(f *membershipFixtures) {
				f.ClusterNode("5.6.7.8:666")
			},
			"The cluster already has a member with address: 5.6.7.8:666",
		},
		{
			"buzz",
			"1.2.3.4:666",
			cluster.SchemaVersion - 1,
			len(version.APIExtensions),
			func(f *membershipFixtures) {
				f.ClusterNode("5.6.7.8:666")
			},
			fmt.Sprintf("The joining server version doesn't match (expected %s with DB schema %d)", version.Version, cluster.SchemaVersion-1),
		},
		{
			"buzz",
			"1.2.3.4:666",
			cluster.SchemaVersion,
			len(version.APIExtensions) - 1,
			func(f *membershipFixtures) {
				f.ClusterNode("5.6.7.8:666")
			},
			fmt.Sprintf("The joining server version doesn't match (expected %s with API count %d)", version.Version, len(version.APIExtensions)-1),
		},
	}

	for _, c := range cases {
		t.Run(c.error, func(t *testing.T) {
			state, cleanup := state.NewTestState(t)
			defer cleanup()

			serverCert := localtls.TestingKeyPair()
			state.ServerCert = func() *localtls.CertInfo { return serverCert }

			gateway := newGateway(t, state.DB.Node, serverCert, state)
			defer func() { _ = gateway.Shutdown() }()

			c.setup(&membershipFixtures{t: t, state: state})

			_, err := cluster.Accept(state, gateway, c.name, c.address, c.schema, c.api, osarch.ARCH_64BIT_INTEL_X86)
			assert.EqualError(t, err, c.error)
		})
	}
}

// When a node gets accepted, it gets included in the raft nodes.
func TestAccept(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	serverCert := localtls.TestingKeyPair()
	state.ServerCert = func() *localtls.CertInfo { return serverCert }

	gateway := newGateway(t, state.DB.Node, serverCert, state)
	defer func() { _ = gateway.Shutdown() }()

	f := &membershipFixtures{t: t, state: state}
	f.RaftNode("1.2.3.4:666")
	f.ClusterNode("1.2.3.4:666")

	err := state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		state.GlobalConfig, err = clusterConfig.Load(ctx, tx)
		if err != nil {
			return err
		}

		// Get the local node (will be used if clustered).
		state.ServerName, err = tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	require.NoError(t, err)

	nodes, err := cluster.Accept(
		state, gateway, "buzz", "5.6.7.8:666", cluster.SchemaVersion, len(version.APIExtensions), osarch.ARCH_64BIT_INTEL_X86)
	assert.NoError(t, err)
	assert.Len(t, nodes, 2)
	assert.Equal(t, uint64(1), nodes[0].ID)
	assert.Equal(t, uint64(3), nodes[1].ID)
	assert.Equal(t, "1.2.3.4:666", nodes[0].Address)
	assert.Equal(t, "5.6.7.8:666", nodes[1].Address)
}

func TestJoin(t *testing.T) {
	// Setup a target node running as leader of a cluster.
	targetCert := localtls.TestingKeyPair()
	targetMux := http.NewServeMux()
	targetServer := newServer(targetCert, targetMux)
	defer targetServer.Close()

	targetState, cleanup := state.NewTestState(t)
	defer cleanup()

	targetState.ServerCert = func() *localtls.CertInfo { return targetCert }

	targetGateway := newGateway(t, targetState.DB.Node, targetCert, targetState)
	defer func() { _ = targetGateway.Shutdown() }()

	altServerCert := localtls.TestingAltKeyPair()
	trustedAltServerCert, _ := x509.ParseCertificate(altServerCert.KeyPair().Certificate[0])

	trustedCerts := func() map[certificate.Type]map[string]x509.Certificate {
		return map[certificate.Type]map[string]x509.Certificate{
			certificate.TypeServer: {
				altServerCert.Fingerprint(): *trustedAltServerCert,
			},
		}
	}

	for path, handler := range targetGateway.HandlerFuncs(nil, trustedCerts) {
		targetMux.HandleFunc(path, handler)
	}

	targetAddress := targetServer.Listener.Addr().String()

	require.NoError(t, targetState.DB.Cluster.Close())

	targetStore := targetGateway.NodeStore()
	targetDialFunc := targetGateway.DialFunc()

	var err error
	targetState.DB.Cluster, err = db.OpenCluster(context.Background(), "db.bin", targetStore, targetAddress, "/unused/db/dir", 10*time.Second, driver.WithDialFunc(targetDialFunc))
	targetState.ServerCert = func() *localtls.CertInfo { return targetCert }
	require.NoError(t, err)

	err = targetState.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		targetState.GlobalConfig, err = clusterConfig.Load(ctx, tx)
		if err != nil {
			return err
		}

		// Get the local node (will be used if clustered).
		targetState.ServerName, err = tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	require.NoError(t, err)

	// PreparedStmts is a global variable and will be overwritten by the OpenCluster call below, so save it here.
	targetStmts := dbCluster.PreparedStmts

	targetF := &membershipFixtures{t: t, state: targetState}
	targetF.ClusterAddress(targetAddress)

	err = cluster.Bootstrap(targetState, targetGateway, "buzz")
	require.NoError(t, err)
	_, err = targetState.DB.Cluster.GetNetworks(api.ProjectDefaultName)
	require.NoError(t, err)

	// Setup a joining node
	mux := http.NewServeMux()
	server := newServer(targetCert, mux)
	defer server.Close()

	state, cleanup := state.NewTestState(t)
	defer cleanup()

	state.ServerCert = func() *localtls.CertInfo { return altServerCert }

	gateway := newGateway(t, state.DB.Node, targetCert, state)

	defer func() { _ = gateway.Shutdown() }()

	for path, handler := range gateway.HandlerFuncs(nil, trustedCerts) {
		mux.HandleFunc(path, handler)
	}

	address := server.Listener.Addr().String()

	require.NoError(t, state.DB.Cluster.Close())

	store := gateway.NodeStore()
	dialFunc := gateway.DialFunc()

	state.DB.Cluster, err = db.OpenCluster(context.Background(), "db.bin", store, address, "/unused/db/dir", 5*time.Second, driver.WithDialFunc(dialFunc))
	require.NoError(t, err)

	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		state.GlobalConfig, err = clusterConfig.Load(ctx, tx)
		if err != nil {
			return err
		}

		// Get the local node (will be used if clustered).
		state.ServerName, err = tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	require.NoError(t, err)

	// Save the other instance of PreparedStmts here.
	sourceStmts := dbCluster.PreparedStmts

	f := &membershipFixtures{t: t, state: state}
	f.ClusterAddress(address)

	// Accept the joining node.
	dbCluster.PreparedStmts = targetStmts
	raftNodes, err := cluster.Accept(
		targetState, targetGateway, "rusp", address, cluster.SchemaVersion, len(version.APIExtensions), osarch.ARCH_64BIT_INTEL_X86)
	require.NoError(t, err)

	// Actually join the cluster.
	dbCluster.PreparedStmts = sourceStmts
	err = cluster.Join(state, gateway, targetCert, altServerCert, "rusp", raftNodes)
	require.NoError(t, err)

	// The leader now returns an updated list of raft nodes.
	// The new node is not included to ensure distributed consensus.
	raftNodes, err = targetGateway.RaftNodes()
	require.NoError(t, err)
	assert.Len(t, raftNodes, 2)
	assert.Equal(t, uint64(1), raftNodes[0].ID)
	assert.Equal(t, targetAddress, raftNodes[0].Address)
	assert.Equal(t, db.RaftVoter, raftNodes[0].Role)
	assert.Equal(t, uint64(2), raftNodes[1].ID)
	assert.Equal(t, address, raftNodes[1].Address)
	assert.Equal(t, db.RaftStandBy, raftNodes[1].Role)

	// The Count function returns the number of nodes.
	count, err := cluster.Count(state)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Leave the cluster.
	leaving, err := cluster.Leave(state, targetGateway, "rusp", false /* force */)
	require.NoError(t, err)
	assert.Equal(t, address, leaving)
	dbCluster.PreparedStmts = targetStmts
	err = cluster.Purge(targetState.DB.Cluster, "rusp")
	require.NoError(t, err)

	// The node has gone from the cluster db.
	err = targetState.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		require.NoError(t, err)
		assert.Len(t, members, 1)
		return nil
	})
	require.NoError(t, err)

	// The node has gone from the raft cluster.
	members, err := targetGateway.RaftNodes()
	require.NoError(t, err)
	assert.Len(t, members, 1)
}

// Helper for setting fixtures for Bootstrap tests.
type membershipFixtures struct {
	t     *testing.T
	state *state.State
}

// Set core.https_address to the given value.
func (h *membershipFixtures) CoreAddress(address string) {
	err := h.state.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		config := map[string]string{
			"core.https_address": address,
		}

		return tx.UpdateConfig(config)
	})
	require.NoError(h.t, err)
}

// Set cluster.https_address to the given value.
func (h *membershipFixtures) ClusterAddress(address string) {
	err := h.state.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		config := map[string]string{
			"cluster.https_address": address,
		}

		return tx.UpdateConfig(config)
	})
	require.NoError(h.t, err)
}

// Add the given address to the raft_nodes table.
func (h *membershipFixtures) RaftNode(address string) {
	err := h.state.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		_, err := tx.CreateRaftNode(address, "rusp")
		return err
	})
	require.NoError(h.t, err)
}

// Get the current list of the raft nodes in the raft_nodes table.
func (h *membershipFixtures) RaftNodes() []db.RaftNode {
	var nodes []db.RaftNode
	err := h.state.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		nodes, err = tx.GetRaftNodes(ctx)
		return err
	})
	require.NoError(h.t, err)
	return nodes
}

// Add the given address to the nodes table of the cluster database.
func (h *membershipFixtures) ClusterNode(address string) {
	err := h.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNode("rusp", address)
		return err
	})
	require.NoError(h.t, err)
}
