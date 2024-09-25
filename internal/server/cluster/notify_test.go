package cluster_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/node"
	"github.com/lxc/incus/v6/internal/server/state"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/api"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// The returned notifier connects to all nodes.
func TestNewNotifier(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	cert := localtls.TestingKeyPair()

	f := notifyFixtures{t: t, state: state}
	defer f.Nodes(cert, 3)()

	// Populate state.LocalConfig after nodes created above.
	var err error
	var nodeConfig *node.Config
	err = state.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(t, err)

	state.LocalConfig = nodeConfig

	notifier, err := cluster.NewNotifier(state, cert, cert, cluster.NotifyAll)
	require.NoError(t, err)

	peers := make(chan string, 2)
	hook := func(client incus.InstanceServer) error {
		server, _, err := client.GetServer()
		require.NoError(t, err)
		peers <- server.Config["cluster.https_address"]
		return nil
	}

	assert.NoError(t, notifier(hook))

	addresses := make([]string, 2)
	for i := range addresses {
		select {
		case addresses[i] = <-peers:
		default:
		}
	}
	require.NoError(t, err)
	for i := range addresses {
		assert.True(t, slices.Contains(addresses, f.Address(i+1)))
	}
}

// Creating a new notifier fails if the policy is set to NotifyAll and one of
// the nodes is down.
func TestNewNotify_NotifyAllError(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	cert := localtls.TestingKeyPair()

	f := notifyFixtures{t: t, state: state}
	defer f.Nodes(cert, 3)()

	f.Down(1)

	// Populate state.LocalConfig after nodes created above.
	var err error
	var nodeConfig *node.Config
	err = state.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(t, err)

	state.LocalConfig = nodeConfig

	notifier, err := cluster.NewNotifier(state, cert, cert, cluster.NotifyAll)
	assert.Nil(t, notifier)
	require.Error(t, err)
	assert.Regexp(t, "peer node .+ is down", err.Error())
}

// Creating a new notifier does not fail if the policy is set to NotifyAlive
// and one of the nodes is down, however dead nodes are ignored.
func TestNewNotify_NotifyAlive(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	cert := localtls.TestingKeyPair()

	f := notifyFixtures{t: t, state: state}
	defer f.Nodes(cert, 3)()

	f.Down(1)

	// Populate state.LocalConfig after nodes created above.
	var err error
	var nodeConfig *node.Config
	err = state.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(t, err)

	state.LocalConfig = nodeConfig

	notifier, err := cluster.NewNotifier(state, cert, cert, cluster.NotifyAlive)
	assert.NoError(t, err)

	i := 0
	hook := func(client incus.InstanceServer) error {
		i++
		return nil
	}

	assert.NoError(t, notifier(hook))
	assert.Equal(t, 1, i)
}

// Helper for setting fixtures for Notify tests.
type notifyFixtures struct {
	t       *testing.T
	state   *state.State
	servers []*httptest.Server
}

// Spawn the given number of fake nodes, save in them in the database and
// return a cleanup function.
//
// The address of the first node spawned will be saved as local
// cluster.https_address.
func (h *notifyFixtures) Nodes(cert *localtls.CertInfo, n int) func() {
	servers := make([]*httptest.Server, n)
	for i := 0; i < n; i++ {
		servers[i] = newRestServer(cert)
	}

	// Insert new entries in the nodes table of the cluster database.
	err := h.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		for i := 0; i < n; i++ {
			name := strconv.Itoa(i)
			address := servers[i].Listener.Addr().String()
			var err error
			if i == 0 {
				err = tx.BootstrapNode(name, address)
			} else {
				_, err = tx.CreateNode(name, address)
			}

			require.NoError(h.t, err)
		}

		return nil
	})
	require.NoError(h.t, err)

	// Set the address in the config table of the node database.
	err = h.state.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		config, err := node.ConfigLoad(ctx, tx)
		require.NoError(h.t, err)
		address := servers[0].Listener.Addr().String()
		values := map[string]string{"cluster.https_address": address}
		_, err = config.Patch(values)
		require.NoError(h.t, err)
		return nil
	})
	require.NoError(h.t, err)

	cleanup := func() {
		for _, server := range servers {
			server.Close()
		}
	}

	h.servers = servers

	return cleanup
}

// Return the network address of the i-th node.
func (h *notifyFixtures) Address(i int) string {
	var address string
	err := h.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		require.NoError(h.t, err)
		address = members[i].Address
		return nil
	})
	require.NoError(h.t, err)
	return address
}

// Mark the i'th node as down.
func (h *notifyFixtures) Down(i int) {
	err := h.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		require.NoError(h.t, err)
		err = tx.SetNodeHeartbeat(members[i].Address, time.Now().Add(-time.Minute))
		require.NoError(h.t, err)
		return nil
	})
	require.NoError(h.t, err)
	h.servers[i].Close()
}

// Returns a minimal stub for the REST API server, just realistic
// enough to make incus.ConnectIncus succeed.
func newRestServer(cert *localtls.CertInfo) *httptest.Server {
	mux := http.NewServeMux()

	server := httptest.NewUnstartedServer(mux)
	server.TLS = localUtil.ServerTLSConfig(cert)
	server.StartTLS()

	mux.HandleFunc("/1.0/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		config := map[string]string{"cluster.https_address": server.Listener.Addr().String()}
		metadata := api.ServerPut{Config: config}
		_ = localUtil.WriteJSON(w, api.ResponseRaw{Metadata: metadata}, nil)
	})

	return server
}
