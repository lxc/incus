package node_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/node"
)

// The server configuration is initially empty.
func TestConfigLoad_Initial(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	config, err := node.ConfigLoad(context.Background(), tx)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{}, config.Dump())
}

// If the database contains invalid keys, they are ignored.
func TestConfigLoad_IgnoreInvalidKeys(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	err := tx.UpdateConfig(map[string]string{
		"foo":                "garbage",
		"core.https_address": "127.0.0.1:666",
	})
	require.NoError(t, err)

	config, err := node.ConfigLoad(context.Background(), tx)

	require.NoError(t, err)
	values := map[string]string{"core.https_address": "127.0.0.1:666"}
	assert.Equal(t, values, config.Dump())
}

// Triggers can be specified to execute custom code on config key changes.
func TestConfigLoad_Triggers(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	config, err := node.ConfigLoad(context.Background(), tx)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{}, config.Dump())
}

// If some previously set values are missing from the ones passed to Replace(),
// they are deleted from the configuration.
func TestConfig_ReplaceDeleteValues(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	config, err := node.ConfigLoad(context.Background(), tx)
	require.NoError(t, err)

	changed, err := config.Replace(map[string]string{"core.https_address": "127.0.0.1:666"})
	assert.NoError(t, err)
	assert.Equal(t, map[string]string{"core.https_address": "127.0.0.1:666"}, changed)

	changed, err = config.Replace(map[string]string{})
	assert.NoError(t, err)
	assert.Equal(t, map[string]string{"core.https_address": ""}, changed)

	assert.Equal(t, "", config.HTTPSAddress())

	values, err := tx.Config(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{}, values)
}

// If some previously set values are missing from the ones passed to Patch(),
// they are kept as they are.
func TestConfig_PatchKeepsValues(t *testing.T) {
	tx, cleanup := db.NewTestNodeTx(t)
	defer cleanup()

	config, err := node.ConfigLoad(context.Background(), tx)
	require.NoError(t, err)

	_, err = config.Replace(map[string]string{"core.https_address": "127.0.0.1:666"})
	assert.NoError(t, err)

	_, err = config.Patch(map[string]string{})
	assert.NoError(t, err)

	assert.Equal(t, "127.0.0.1:666", config.HTTPSAddress())

	values, err := tx.Config(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"core.https_address": "127.0.0.1:666"}, values)
}

// The core.https_address config key is fetched from the db with a new
// transaction.
func TestHTTPSAddress(t *testing.T) {
	nodeDB, cleanup := db.NewTestNode(t)
	defer cleanup()

	var err error
	var config *node.Config
	err = nodeDB.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		config, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(t, err)

	require.NoError(t, err)
	assert.Equal(t, "", config.HTTPSAddress())

	err = nodeDB.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		config, err = node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		_, err = config.Replace(map[string]string{"core.https_address": "127.0.0.1:666"})
		return err
	})
	require.NoError(t, err)

	err = nodeDB.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		config, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(t, err)

	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:666", config.HTTPSAddress())
}

// The cluster.https_address config key is fetched from the db with a new
// transaction.
func TestClusterAddress(t *testing.T) {
	nodeDB, cleanup := db.NewTestNode(t)
	defer cleanup()

	var err error
	var nodeConfig *node.Config
	err = nodeDB.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, "", nodeConfig.ClusterAddress())

	err = nodeDB.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err = node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		_, err = nodeConfig.Replace(map[string]string{"cluster.https_address": "127.0.0.1:666"})
		return err
	})
	require.NoError(t, err)

	err = nodeDB.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1:666", nodeConfig.ClusterAddress())
}
