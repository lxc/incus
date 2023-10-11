package cluster_test

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cowsql/go-cowsql/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/internal/server/certificate"
	"github.com/lxc/incus/internal/server/cluster"
	clusterConfig "github.com/lxc/incus/internal/server/cluster/config"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/node"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/osarch"
	localtls "github.com/lxc/incus/shared/tls"
)

// After a heartbeat request is completed, the leader updates the heartbeat
// timestamp column, and the serving node updates its cache of raft nodes.
func TestHeartbeat(t *testing.T) {
	f := heartbeatFixture{t: t}
	defer f.Cleanup()

	f.Bootstrap()
	f.Grow()
	f.Grow()

	time.Sleep(1 * time.Second) // Wait for join notifiation triggered heartbeats to complete.

	leader := f.Leader()
	leaderState := f.State(leader)

	// Artificially mark all nodes as down
	err := leaderState.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		require.NoError(t, err)
		for _, member := range members {
			err := tx.SetNodeHeartbeat(member.Address, time.Now().Add(-time.Minute))
			require.NoError(t, err)
		}

		return nil
	})
	require.NoError(t, err)

	// Perform the heartbeat requests.
	leader.Cluster = leaderState.DB.Cluster
	heartbeat, _ := cluster.HeartbeatTask(leader)
	ctx := context.Background()
	heartbeat(ctx)

	// The heartbeat timestamps of all nodes got updated
	err = leaderState.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		require.NoError(t, err)

		offlineThreshold, err := tx.GetNodeOfflineThreshold(ctx)
		require.NoError(t, err)

		for _, member := range members {
			assert.False(t, member.IsOffline(offlineThreshold))
		}

		return nil
	})
	require.NoError(t, err)
}

// Helper for testing heartbeat-related code.
type heartbeatFixture struct {
	t        *testing.T
	gateways map[int]*cluster.Gateway              // node index to gateway
	states   map[*cluster.Gateway]*state.State     // gateway to its state handle
	servers  map[*cluster.Gateway]*httptest.Server // gateway to its HTTP server
	cleanups []func()
}

// Bootstrap the first node of the cluster.
func (f *heartbeatFixture) Bootstrap() *cluster.Gateway {
	f.t.Logf("create bootstrap node for test cluster")
	state, gateway, _ := f.node()

	err := cluster.Bootstrap(state, gateway, "buzz")
	require.NoError(f.t, err)

	return gateway
}

// Grow adds a new node to the cluster.
func (f *heartbeatFixture) Grow() *cluster.Gateway {
	// Figure out the current leader
	f.t.Logf("adding another node to the test cluster")
	target := f.Leader()
	targetState := f.states[target]

	state, gateway, address := f.node()
	name := address

	nodes, err := cluster.Accept(
		targetState, target, name, address, cluster.SchemaVersion, len(version.APIExtensions), osarch.ARCH_64BIT_INTEL_X86)
	require.NoError(f.t, err)

	err = cluster.Join(state, gateway, target.NetworkCert(), target.ServerCert(), name, nodes)
	require.NoError(f.t, err)

	return gateway
}

// Return the leader gateway in the cluster.
func (f *heartbeatFixture) Leader() *cluster.Gateway {
	timeout := time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		for _, gateway := range f.gateways {
			isLeader, err := gateway.IsLeader()
			if err != nil {
				f.t.Errorf("failed to check leadership: %v", err)
			}

			if isLeader {
				return gateway
			}
		}

		select {
		case <-ctx.Done():
			f.t.Errorf("no leader was elected within %s", timeout)
		default:
		}

		// Wait a bit for election to take place
		time.Sleep(10 * time.Millisecond)
	}
}

// Return a follower gateway in the cluster.
func (f *heartbeatFixture) Follower() *cluster.Gateway {
	timeout := time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		for _, gateway := range f.gateways {
			isLeader, err := gateway.IsLeader()
			if err != nil {
				f.t.Errorf("failed to check leadership: %v", err)
			}

			if !isLeader {
				return gateway
			}
		}

		select {
		case <-ctx.Done():
			f.t.Errorf("no node running as follower")
		default:
		}

		// Wait a bit for election to take place
		time.Sleep(10 * time.Millisecond)
	}
}

// Return the cluster index of the given gateway.
func (f *heartbeatFixture) Index(gateway *cluster.Gateway) int {
	for i := range f.gateways {
		if f.gateways[i] == gateway {
			return i
		}
	}
	return -1
}

// Return the state associated with the given gateway.
func (f *heartbeatFixture) State(gateway *cluster.Gateway) *state.State {
	return f.states[gateway]
}

// Return the HTTP server associated with the given gateway.
func (f *heartbeatFixture) Server(gateway *cluster.Gateway) *httptest.Server {
	return f.servers[gateway]
}

// Creates a new node, without either bootstrapping or joining it.
//
// Return the associated gateway and network address.
func (f *heartbeatFixture) node() (*state.State, *cluster.Gateway, string) {
	if f.gateways == nil {
		f.gateways = make(map[int]*cluster.Gateway)
		f.states = make(map[*cluster.Gateway]*state.State)
		f.servers = make(map[*cluster.Gateway]*httptest.Server)
	}

	state, cleanup := state.NewTestState(f.t)
	f.cleanups = append(f.cleanups, cleanup)

	serverCert := localtls.TestingKeyPair()
	state.ServerCert = func() *localtls.CertInfo { return serverCert }

	gateway := newGateway(f.t, state.DB.Node, serverCert, state)
	f.cleanups = append(f.cleanups, func() { _ = gateway.Shutdown() })

	mux := http.NewServeMux()
	server := newServer(serverCert, mux)

	trustedCerts := func() map[certificate.Type]map[string]x509.Certificate {
		return nil
	}

	for path, handler := range gateway.HandlerFuncs(nil, trustedCerts) {
		mux.HandleFunc(path, handler)
	}

	address := server.Listener.Addr().String()
	mf := &membershipFixtures{t: f.t, state: state}
	mf.ClusterAddress(address)

	var err error
	require.NoError(f.t, state.DB.Cluster.Close())
	store := gateway.NodeStore()
	dial := gateway.DialFunc()
	state.DB.Cluster, err = db.OpenCluster(context.Background(), "db.bin", store, address, "/unused/db/dir", 5*time.Second, driver.WithDialFunc(dial))
	require.NoError(f.t, err)

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
	require.NoError(f.t, err)

	err = state.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		state.LocalConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(f.t, err)

	f.gateways[len(f.gateways)] = gateway
	f.states[gateway] = state
	f.servers[gateway] = server

	return state, gateway, address
}

func (f *heartbeatFixture) Cleanup() {
	// Run the cleanups in reverse order
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}

	for _, server := range f.servers {
		server.Close()
	}
}
