package cluster_test

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cowsql/go-cowsql/client"
	"github.com/cowsql/go-cowsql/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/certificate"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/node"
	"github.com/lxc/incus/v6/internal/server/state"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// A node can unblock other nodes that were waiting for a cluster upgrade to
// complete.
func TestNotifyUpgradeCompleted(t *testing.T) {
	f := heartbeatFixture{t: t}
	defer f.Cleanup()

	gateway0 := f.Bootstrap()
	gateway1 := f.Grow()

	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		gateway1.WaitUpgradeNotification()
		wg.Done()
	}()

	state0 := f.State(gateway0)

	// Populate state.LocalConfig after nodes created above.
	var err error
	var nodeConfig *node.Config
	err = state0.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(t, err)

	state0.LocalConfig = nodeConfig

	serverCert0 := gateway0.ServerCert()
	err = cluster.NotifyUpgradeCompleted(state0, serverCert0, serverCert0)
	require.NoError(t, err)

	wg.Wait()
}

// The task function checks if the node is out of date and runs whatever is in
// INCUS_CLUSTER_UPDATE if so.
func TestMaybeUpdate_Upgrade(t *testing.T) {
	dir, err := os.MkdirTemp("", "")
	require.NoError(t, err)

	defer func() { _ = os.RemoveAll(dir) }()

	// Create a stub upgrade script that just touches a stamp file.
	stamp := filepath.Join(dir, "stamp")
	script := filepath.Join(dir, "cluster-upgrade")
	data := []byte(fmt.Sprintf("#!/bin/sh\ntouch %s\n", stamp))
	err = os.WriteFile(script, data, 0755)
	require.NoError(t, err)

	state, cleanup := state.NewTestState(t)
	defer cleanup()

	_ = state.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		nodes := []db.RaftNode{
			{NodeInfo: client.NodeInfo{ID: 1, Address: "0.0.0.0:666"}},
			{NodeInfo: client.NodeInfo{ID: 2, Address: "1.2.3.4:666"}},
		}

		err := tx.ReplaceRaftNodes(nodes)
		require.NoError(t, err)
		return nil
	})

	_ = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := tx.CreateNode("buzz", "1.2.3.4:666")
		require.NoError(t, err)

		node, err := tx.GetNodeByName(ctx, "buzz")
		require.NoError(t, err)

		version := node.Version()
		version[0]++

		err = tx.SetNodeVersion(id, version)
		require.NoError(t, err)

		return nil
	})

	_ = os.Setenv("INCUS_CLUSTER_UPDATE", script)
	defer func() { _ = os.Unsetenv("INCUS_CLUSTER_UPDATE") }()

	_ = cluster.MaybeUpdate(state)

	_, err = os.Stat(stamp)
	require.NoError(t, err)
}

// If the node is up-to-date, nothing is done.
func TestMaybeUpdate_NothingToDo(t *testing.T) {
	dir, err := os.MkdirTemp("", "")
	require.NoError(t, err)

	defer func() { _ = os.RemoveAll(dir) }()

	// Create a stub upgrade script that just touches a stamp file.
	stamp := filepath.Join(dir, "stamp")
	script := filepath.Join(dir, "cluster-upgrade")
	data := []byte(fmt.Sprintf("#!/bin/sh\ntouch %s\n", stamp))
	err = os.WriteFile(script, data, 0755)
	require.NoError(t, err)

	state, cleanup := state.NewTestState(t)
	defer cleanup()

	_ = os.Setenv("INCUS_CLUSTER_UPDATE", script)
	defer func() { _ = os.Unsetenv("INCUS_CLUSTER_UPDATE") }()

	_ = cluster.MaybeUpdate(state)

	_, err = os.Stat(stamp)
	require.True(t, os.IsNotExist(err))
}

func TestUpgradeMembersWithoutRole(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	serverCert := localtls.TestingKeyPair()
	mux := http.NewServeMux()
	server := newServer(serverCert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, state.DB.Node, address)

	state.ServerCert = func() *localtls.CertInfo { return serverCert }

	gateway := newGateway(t, state.DB.Node, serverCert, state)
	defer func() { _ = gateway.Shutdown() }()

	trustedCerts := func() map[certificate.Type]map[string]x509.Certificate {
		return nil
	}

	for path, handler := range gateway.HandlerFuncs(nil, trustedCerts) {
		mux.HandleFunc(path, handler)
	}

	var err error
	require.NoError(t, state.DB.Cluster.Close())
	store := gateway.NodeStore()
	dial := gateway.DialFunc()
	state.DB.Cluster, err = db.OpenCluster(context.Background(), "db.bin", store, address, "/unused/db/dir", 5*time.Second, driver.WithDialFunc(dial))
	require.NoError(t, err)
	gateway.Cluster = state.DB.Cluster

	// Add a couple of members to the database.
	var members []db.NodeInfo
	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNode("foo", "1.2.3.4")
		require.NoError(t, err)
		_, err = tx.CreateNode("bar", "5.6.7.8")
		require.NoError(t, err)
		members, err = tx.GetNodes(ctx)
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)

	err = cluster.UpgradeMembersWithoutRole(gateway, members)
	require.NoError(t, err)

	// The members have been added to the raft configuration.
	nodes, err := gateway.RaftNodes()
	require.NoError(t, err)

	assert.Len(t, nodes, 3)
	assert.Equal(t, uint64(1), nodes[0].ID)
	assert.Equal(t, address, nodes[0].Address)
	assert.Equal(t, uint64(2), nodes[1].ID)
	assert.Equal(t, "1.2.3.4", nodes[1].Address)
	assert.Equal(t, uint64(3), nodes[2].ID)
	assert.Equal(t, "5.6.7.8", nodes[2].Address)
}
