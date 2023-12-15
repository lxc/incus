//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/shared/api"
)

// The GetNetworksLocalConfigs method returns only node-specific config values.
func TestGetNetworksLocalConfigs(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	err := cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "incusbr0", "", db.NetworkTypeBridge, map[string]string{
			"dns.mode":                   "none",
			"bridge.external_interfaces": "vlan0",
		})

		return err
	})
	require.NoError(t, err)

	var config map[string]map[string]string

	err = cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		config, err = tx.GetNetworksLocalConfig(ctx)
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, config, map[string]map[string]string{
		"incusbr0": {"bridge.external_interfaces": "vlan0"},
	})
}

func TestCreatePendingNetwork(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)
	_, err = tx.CreateNode("rusp", "5.6.7.8:666")
	require.NoError(t, err)

	config := map[string]string{"bridge.external_interfaces": "foo"}
	err = tx.CreatePendingNetwork(context.Background(), "buzz", api.ProjectDefaultName, "network1", db.NetworkTypeBridge, config)
	require.NoError(t, err)

	networkID, err := tx.GetNetworkID(context.Background(), api.ProjectDefaultName, "network1")
	require.NoError(t, err)
	assert.True(t, networkID > 0)

	config = map[string]string{"bridge.external_interfaces": "bar"}
	err = tx.CreatePendingNetwork(context.Background(), "rusp", api.ProjectDefaultName, "network1", db.NetworkTypeBridge, config)
	require.NoError(t, err)

	// The initial node (whose name is 'none' by default) is missing.
	_, err = tx.NetworkNodeConfigs(context.Background(), networkID)
	require.EqualError(t, err, "Network not defined on nodes: none")

	config = map[string]string{"bridge.external_interfaces": "egg"}
	err = tx.CreatePendingNetwork(context.Background(), "none", api.ProjectDefaultName, "network1", db.NetworkTypeBridge, config)
	require.NoError(t, err)

	// Now the storage is defined on all nodes.
	configs, err := tx.NetworkNodeConfigs(context.Background(), networkID)
	require.NoError(t, err)
	assert.Len(t, configs, 3)
	assert.Equal(t, map[string]string{"bridge.external_interfaces": "foo"}, configs["buzz"])
	assert.Equal(t, map[string]string{"bridge.external_interfaces": "bar"}, configs["rusp"])
	assert.Equal(t, map[string]string{"bridge.external_interfaces": "egg"}, configs["none"])
}

// If an entry for the given network and node already exists, an error is
// returned.
func TestNetworksCreatePending_AlreadyDefined(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	err = tx.CreatePendingNetwork(context.Background(), "buzz", api.ProjectDefaultName, "network1", db.NetworkTypeBridge, map[string]string{})
	require.NoError(t, err)

	err = tx.CreatePendingNetwork(context.Background(), "buzz", api.ProjectDefaultName, "network1", db.NetworkTypeBridge, map[string]string{})
	require.Equal(t, db.ErrAlreadyDefined, err)
}

// If no node with the given name is found, an error is returned.
func TestNetworksCreatePending_NonExistingNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	err := tx.CreatePendingNetwork(context.Background(), "buzz", api.ProjectDefaultName, "network1", db.NetworkTypeBridge, map[string]string{})
	require.True(t, response.IsNotFoundError(err))
}
