//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/osarch"
)

// Add a new raft node.
func TestNodeAdd(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)
	assert.Equal(t, int64(2), id)

	nodes, err := tx.GetNodes(context.Background())
	require.NoError(t, err)
	require.Len(t, nodes, 2)

	node, err := tx.GetNodeByAddress(context.Background(), "1.2.3.4:666")
	require.NoError(t, err)
	assert.Equal(t, "buzz", node.Name)
	assert.Equal(t, "1.2.3.4:666", node.Address)
	assert.Equal(t, cluster.SchemaVersion, node.Schema)
	assert.Equal(t, len(version.APIExtensions), node.APIExtensions)
	assert.Equal(t, [2]int{cluster.SchemaVersion, len(version.APIExtensions)}, node.Version())
	assert.False(t, node.IsOffline(20*time.Second))

	node, err = tx.GetNodeByName(context.Background(), "buzz")
	require.NoError(t, err)
	assert.Equal(t, "buzz", node.Name)
}

func TestGetNodesCount(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	count, err := tx.GetNodesCount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count) // There's always at least one node.

	_, err = tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	count, err = tx.GetNodesCount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestNodeIsOutdated_SingleNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	outdated, err := tx.NodeIsOutdated(context.Background())
	require.NoError(t, err)

	assert.False(t, outdated)
}

func TestNodeIsOutdated_AllNodesAtSameVersion(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	outdated, err := tx.NodeIsOutdated(context.Background())
	require.NoError(t, err)

	assert.False(t, outdated)
}

func TestNodeIsOutdated_OneNodeWithHigherVersion(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	version := [2]int{cluster.SchemaVersion + 1, len(version.APIExtensions)}
	err = tx.SetNodeVersion(id, version)
	require.NoError(t, err)

	outdated, err := tx.NodeIsOutdated(context.Background())
	require.NoError(t, err)

	assert.True(t, outdated)
}

func TestNodeIsOutdated_OneNodeWithLowerVersion(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	version := [2]int{cluster.SchemaVersion, len(version.APIExtensions) - 1}
	err = tx.SetNodeVersion(id, version)
	require.NoError(t, err)

	outdated, err := tx.NodeIsOutdated(context.Background())
	require.NoError(t, err)

	assert.False(t, outdated)
}

func TestGetLocalNodeName(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	name, err := tx.GetLocalNodeName(context.Background())
	require.NoError(t, err)

	// The default node 1 has a conventional name 'none'.
	assert.Equal(t, "none", name)
}

// Rename a node.
func TestRenameNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)
	err = tx.RenameNode(context.Background(), "buzz", "rusp")
	require.NoError(t, err)
	node, err := tx.GetNodeByName(context.Background(), "rusp")
	require.NoError(t, err)
	assert.Equal(t, "rusp", node.Name)

	_, err = tx.CreateNode("buzz", "5.6.7.8:666")
	require.NoError(t, err)
	err = tx.RenameNode(context.Background(), "rusp", "buzz")
	assert.Equal(t, db.ErrAlreadyDefined, err)
}

// Remove a new raft node.
func TestRemoveNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	id, err := tx.CreateNode("rusp", "5.6.7.8:666")
	require.NoError(t, err)

	err = tx.RemoveNode(id)
	require.NoError(t, err)

	_, err = tx.GetNodeByName(context.Background(), "buzz")
	assert.NoError(t, err)

	_, err = tx.GetNodeByName(context.Background(), "rusp")
	assert.True(t, response.IsNotFoundError(err))
}

// Mark a node has pending.
func TestSetNodePendingFlag(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	// Add the pending flag
	err = tx.SetNodePendingFlag(id, true)
	require.NoError(t, err)

	// Pending nodes are skipped from regular listing
	_, err = tx.GetNodeByName(context.Background(), "buzz")
	assert.True(t, response.IsNotFoundError(err))
	nodes, err := tx.GetNodes(context.Background())
	require.NoError(t, err)
	assert.Len(t, nodes, 1)

	// But the key be retrieved with GetPendingNodeByAddress
	node, err := tx.GetPendingNodeByAddress(context.Background(), "1.2.3.4:666")
	require.NoError(t, err)
	assert.Equal(t, id, node.ID)

	// Remove the pending flag
	err = tx.SetNodePendingFlag(id, false)
	require.NoError(t, err)
	node, err = tx.GetNodeByName(context.Background(), "buzz")
	require.NoError(t, err)
	assert.Equal(t, id, node.ID)
}

// Update the heartbeat of a node.
func TestSetNodeHeartbeat(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	err = tx.SetNodeHeartbeat("1.2.3.4:666", time.Now().Add(-time.Minute))
	require.NoError(t, err)

	nodes, err := tx.GetNodes(context.Background())
	require.NoError(t, err)
	require.Len(t, nodes, 2)

	node := nodes[1]
	assert.True(t, node.IsOffline(20*time.Second))
}

// A node is considered empty only if it has no instances.
func TestNodeIsEmpty_Instances(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	message, err := tx.NodeIsEmpty(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "", message)

	_, err = tx.Tx().Exec(`
INSERT INTO instances (id, node_id, name, architecture, type, project_id, description) VALUES (1, ?, 'foo', 1, 1, 1, '')
`, id)
	require.NoError(t, err)

	message, err = tx.NodeIsEmpty(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "Node still has the following instances: foo", message)

	err = tx.ClearNode(context.Background(), id)
	require.NoError(t, err)

	message, err = tx.NodeIsEmpty(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "", message)
}

// A node is considered empty only if it has no images that are available only
// on that node.
func TestNodeIsEmpty_Images(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	_, err = tx.Tx().Exec(`
INSERT INTO images (id, fingerprint, filename, size, architecture, upload_date, project_id)
  VALUES (1, 'abc', 'foo', 123, 1, ?, 1)`, time.Now())
	require.NoError(t, err)

	_, err = tx.Tx().Exec(`
INSERT INTO images_nodes(image_id, node_id) VALUES(1, ?)`, id)
	require.NoError(t, err)

	message, err := tx.NodeIsEmpty(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "Node still has the following images: abc", message)

	// Insert a new image entry for node 1 (the default node).
	_, err = tx.Tx().Exec(`
INSERT INTO images_nodes(image_id, node_id) VALUES(1, 1)`)
	require.NoError(t, err)

	message, err = tx.NodeIsEmpty(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "", message)
}

// A node is considered empty only if it has no custom volumes on it.
func TestNodeIsEmpty_CustomVolumes(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	_, err = tx.Tx().Exec(`
INSERT INTO storage_pools (id, name, driver, description) VALUES (1, 'local', 'zfs', '')`)
	require.NoError(t, err)

	_, err = tx.Tx().Exec(`
INSERT INTO storage_volumes(name, storage_pool_id, node_id, type, project_id, description)
  VALUES ('data', 1, ?, ?, 1, '')`, id, db.StoragePoolVolumeTypeCustom)
	require.NoError(t, err)

	message, err := tx.NodeIsEmpty(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "Node still has the following custom volumes: data", message)
}

// If there are 2 online nodes, return the address of the one with the least
// number of instances.
func TestGetNodeWithLeastInstances(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	// Add an instance to the default node (ID 1)
	_, err = tx.Tx().Exec(`
INSERT INTO instances (id, node_id, name, architecture, type, project_id, description) VALUES (1, 1, 'foo', 1, 1, 1, '')
`)
	require.NoError(t, err)

	allMembers, err := tx.GetNodes(context.Background())
	require.NoError(t, err)

	members, err := tx.GetCandidateMembers(context.Background(), allMembers, nil, "", nil, time.Duration(db.DefaultOfflineThreshold)*time.Second)
	require.NoError(t, err)
	require.Len(t, members, 2)

	member, err := tx.GetNodeWithLeastInstances(context.Background(), members)
	require.NoError(t, err)
	assert.Equal(t, "buzz", member.Name)
}

// If there are nodes, and one of them is offline, return the name of the
// online node, even if the offline one has more instances.
func TestGetNodeWithLeastInstances_OfflineNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	// Add an instance to the newly created node.
	_, err = tx.Tx().Exec(`
INSERT INTO instances (id, node_id, name, architecture, type, project_id, description) VALUES (1, ?, 'foo', 1, 1, 1, '')
`, id)
	require.NoError(t, err)

	// Mark the default node has offline.
	err = tx.SetNodeHeartbeat("0.0.0.0", time.Now().Add(-time.Minute))
	require.NoError(t, err)

	allMembers, err := tx.GetNodes(context.Background())
	require.NoError(t, err)

	members, err := tx.GetCandidateMembers(context.Background(), allMembers, nil, "", nil, time.Duration(db.DefaultOfflineThreshold)*time.Second)
	require.NoError(t, err)
	require.Len(t, members, 1)

	member, err := tx.GetNodeWithLeastInstances(context.Background(), members)
	require.NoError(t, err)
	assert.Equal(t, "buzz", member.Name)
}

// If there are 2 online nodes, and an instance is pending on one of them,
// return the address of the other one number of instances.
func TestGetNodeWithLeastInstances_Pending(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	// Add a pending instance to the default node (ID 1)
	_, err = tx.Tx().Exec(`
INSERT INTO operations (id, uuid, node_id, type, project_id) VALUES (1, 'abc', 1, ?, 1)
`, operationtype.InstanceCreate)
	require.NoError(t, err)

	allMembers, err := tx.GetNodes(context.Background())
	require.NoError(t, err)

	members, err := tx.GetCandidateMembers(context.Background(), allMembers, nil, "", nil, time.Duration(db.DefaultOfflineThreshold)*time.Second)
	require.NoError(t, err)
	require.Len(t, members, 2)

	member, err := tx.GetNodeWithLeastInstances(context.Background(), members)
	require.NoError(t, err)
	assert.Equal(t, "buzz", member.Name)
}

// If specific architectures were selected, return only nodes with those
// architectures.
func TestGetNodeWithLeastInstances_Architecture(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	localArch, err := osarch.ArchitectureGetLocalID()
	require.NoError(t, err)

	testArch := osarch.ARCH_64BIT_S390_BIG_ENDIAN
	if localArch == testArch {
		testArch = osarch.ARCH_64BIT_INTEL_X86
	}

	_, err = tx.CreateNodeWithArch("buzz", "1.2.3.4:666", testArch)
	require.NoError(t, err)

	// Add an instance to the default node (ID 1)
	_, err = tx.Tx().Exec(`
INSERT INTO instances (id, node_id, name, architecture, type, project_id, description) VALUES (1, 1, 'foo', 1, 1, 1, '')
`)
	require.NoError(t, err)

	allMembers, err := tx.GetNodes(context.Background())
	require.NoError(t, err)

	members, err := tx.GetCandidateMembers(context.Background(), allMembers, []int{localArch}, "", nil, time.Duration(db.DefaultOfflineThreshold)*time.Second)
	require.NoError(t, err)
	require.Len(t, members, 1)

	// The local member is returned despite it has more instances.
	member, err := tx.GetNodeWithLeastInstances(context.Background(), members)
	require.NoError(t, err)
	assert.Equal(t, "none", member.Name)
}

func TestUpdateNodeFailureDomain(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateNode("buzz", "1.2.3.4:666")
	require.NoError(t, err)

	domain, err := tx.GetNodeFailureDomain(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "default", domain)

	assert.NoError(t, tx.UpdateNodeFailureDomain(context.Background(), id, "foo"))

	domain, err = tx.GetNodeFailureDomain(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "foo", domain)

	domains, err := tx.GetNodesFailureDomains(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]uint64{"0.0.0.0": 0, "1.2.3.4:666": 1}, domains)

	assert.NoError(t, tx.UpdateNodeFailureDomain(context.Background(), id, "default"))

	domains, err = tx.GetNodesFailureDomains(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]uint64{"0.0.0.0": 0, "1.2.3.4:666": 0}, domains)
}

func TestGetNodeWithLeastInstances_DefaultArch(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	localArch, err := osarch.ArchitectureGetLocalID()
	require.NoError(t, err)

	testArch := osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN
	if localArch == testArch {
		testArch = osarch.ARCH_64BIT_INTEL_X86
	}

	id, err := tx.CreateNodeWithArch("buzz", "1.2.3.4:666", testArch)
	require.NoError(t, err)

	// Add an instance to the newly created node.
	_, err = tx.Tx().Exec(`
INSERT INTO instances (id, node_id, name, architecture, type, project_id, description) VALUES (1, ?, 'foo', 1, 1, 1, '')
`, id)
	require.NoError(t, err)

	allMembers, err := tx.GetNodes(context.Background())
	require.NoError(t, err)

	members, err := tx.GetCandidateMembers(context.Background(), allMembers, []int{testArch}, "", nil, time.Duration(db.DefaultOfflineThreshold)*time.Second)
	require.NoError(t, err)
	require.Len(t, members, 1)

	member, err := tx.GetNodeWithLeastInstances(context.Background(), members)
	require.NoError(t, err)
	assert.Equal(t, "buzz", member.Name)
}
