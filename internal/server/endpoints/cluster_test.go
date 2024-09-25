package endpoints_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// If both a network and a cluster address are set, and they differ, a new
// network TCP socket will be created.
func TestEndpoints_ClusterCreateTCPSocket(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	config.NetworkAddress = "127.0.0.1:12345"
	config.ClusterAddress = "127.0.0.1:54321"
	require.NoError(t, endpoints.Up(config))

	assert.NoError(t, httpGetOverTLSSocket(endpoints.NetworkAddressAndCert()))
	assert.NoError(t, httpGetOverTLSSocket(endpoints.ClusterAddressAndCert()))
}

// When the cluster address is actually covered by the network one, no new port
// is opened.
func TestEndpoints_ClusterUpdateAddressIsCovered(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	config.NetworkAddress = "[::]:12345"
	config.ClusterAddress = ""
	require.NoError(t, endpoints.Up(config))

	require.NoError(t, endpoints.ClusterUpdateAddress("127.0.0.1:12345"))

	assert.NoError(t, httpGetOverTLSSocket(endpoints.NetworkAddressAndCert()))
}
