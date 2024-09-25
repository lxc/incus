package main

import (
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

// allocatePort asks the kernel for a free open port that is ready to use.
func allocatePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return -1, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return -1, err
	}

	return l.Addr().(*net.TCPAddr).Port, l.Close()
}

// A node which is already configured for networking can be converted to a
// single-node cluster.
func TestCluster_Bootstrap(t *testing.T) {
	daemon, cleanup := newTestDaemon(t)
	defer cleanup()

	// Simulate what happens when running "init", where a PUT /1.0
	// request is issued to set both core.https_address and
	// cluster.https_address to the same value.
	f := clusterFixture{t: t}
	f.EnableNetworkingWithClusterAddress(daemon, "")

	client := f.ClientUnix(daemon)

	cluster := api.ClusterPut{}
	cluster.ServerName = "buzz"
	cluster.Enabled = true
	op, err := client.UpdateCluster(cluster, "")
	require.NoError(t, err)
	require.NoError(t, op.Wait())

	server, _, err := client.GetServer()
	require.NoError(t, err)
	assert.True(t, client.IsClustered())
	assert.Equal(t, "buzz", server.Environment.ServerName)
}

// Check the cluster API on a non-clustered server.
func TestCluster_Get(t *testing.T) {
	daemon, cleanup := newTestDaemon(t)
	defer cleanup()

	c, err := incus.ConnectIncusUnix(daemon.os.GetUnixSocket(), nil)
	require.NoError(t, err)

	cluster, _, err := c.GetCluster()
	require.NoError(t, err)
	assert.Equal(t, "", cluster.ServerName)
	assert.False(t, cluster.Enabled)
}

// A node can be renamed.
func TestCluster_RenameNode(t *testing.T) {
	daemon, cleanup := newTestDaemon(t)
	defer cleanup()

	f := clusterFixture{t: t}
	f.EnableNetworking(daemon, "")

	client := f.ClientUnix(daemon)

	cluster := api.ClusterPut{}
	cluster.ServerName = "buzz"
	cluster.Enabled = true
	op, err := client.UpdateCluster(cluster, "")
	require.NoError(t, err)
	require.NoError(t, op.Wait())

	node := api.ClusterMemberPost{ServerName: "rusp"}
	err = client.RenameClusterMember("buzz", node)
	require.NoError(t, err)

	_, _, err = client.GetClusterMember("rusp")
	require.NoError(t, err)
}

// Test helper for cluster-related APIs.
type clusterFixture struct {
	t       *testing.T
	clients map[*Daemon]incus.InstanceServer
}

// Enable networking in the given daemon. The password is optional and can be
// an empty string.
func (f *clusterFixture) EnableNetworking(daemon *Daemon, password string) {
	port, err := allocatePort()
	require.NoError(f.t, err)

	address := fmt.Sprintf("127.0.0.1:%d", port)

	client := f.ClientUnix(daemon)
	server, _, err := client.GetServer()
	require.NoError(f.t, err)
	serverPut := server.Writable()
	serverPut.Config["core.https_address"] = address

	require.NoError(f.t, client.UpdateServer(serverPut, ""))
}

// Enable networking in the given daemon, and set cluster.https_address to the
// same value as core.https address. The password is optional and can be an
// empty string.
func (f *clusterFixture) EnableNetworkingWithClusterAddress(daemon *Daemon, password string) {
	port, err := allocatePort()
	require.NoError(f.t, err)

	address := fmt.Sprintf("127.0.0.1:%d", port)

	client := f.ClientUnix(daemon)
	server, _, err := client.GetServer()
	require.NoError(f.t, err)
	serverPut := server.Writable()
	serverPut.Config["core.https_address"] = address
	serverPut.Config["cluster.https_address"] = address

	require.NoError(f.t, client.UpdateServer(serverPut, ""))
}

// Get a client for the given daemon connected via UNIX socket, creating one if
// needed.
func (f *clusterFixture) ClientUnix(daemon *Daemon) incus.InstanceServer {
	if f.clients == nil {
		f.clients = make(map[*Daemon]incus.InstanceServer)
	}

	client, ok := f.clients[daemon]
	if !ok {
		var err error
		client, err = incus.ConnectIncusUnix(daemon.os.GetUnixSocket(), nil)
		require.NoError(f.t, err)
	}

	return client
}
