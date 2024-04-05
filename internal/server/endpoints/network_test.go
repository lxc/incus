package endpoints_test

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	localtls "github.com/lxc/incus/v6/shared/tls"
)

// If no socket-based activation is detected, and a network address is set, a
// new network TCP socket will be created.
func TestEndpoints_NetworkCreateTCPSocket(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	config.NetworkAddress = "127.0.0.1:0"
	require.NoError(t, endpoints.Up(config))

	assert.NoError(t, httpGetOverTLSSocket(endpoints.NetworkAddressAndCert()))
}

// It's possible to replace the TLS certificate used by the network endpoint.
func TestEndpoints_NetworkUpdateCert(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	config.NetworkAddress = "127.0.0.1:0"
	require.NoError(t, endpoints.Up(config))

	oldCert := config.Cert
	newCert := localtls.TestingAltKeyPair()

	endpoints.NetworkUpdateCert(newCert)

	address := endpoints.NetworkAddress()
	assert.NoError(t, httpGetOverTLSSocket(address, newCert))

	// The old cert does not work anymore
	assert.Error(t, httpGetOverTLSSocket(address, oldCert))
}

// If socket-based activation is detected, it will be used for binding the API
// Endpoints' unix socket.
func TestEndpoints_NetworkSocketBasedActivation(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	listener := newTCPListener(t)
	defer func() { _ = listener.Close() }()

	file, err := listener.File()
	require.NoError(t, err)

	setupSocketBasedActivation(endpoints, file)

	require.NoError(t, endpoints.Up(config))

	assertNoSocketBasedActivation(t)

	assert.NoError(t, httpGetOverTLSSocket(endpoints.NetworkAddressAndCert()))
}

// When the network address is updated, any previous network socket gets
// closed.
func TestEndpoints_NetworkUpdateAddress(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	config.NetworkAddress = "127.0.0.1:0"
	require.NoError(t, endpoints.Up(config))

	// Use "localhost" instead of "127.0.0.1" just to make the address
	// different and actually trigger an endpoint change.
	require.NoError(t, endpoints.NetworkUpdateAddress("localhost:0"))

	assert.NoError(t, httpGetOverTLSSocket(endpoints.NetworkAddressAndCert()))
}

// Create a TCPListener using a random port.
func newTCPListener(t *testing.T) *net.TCPListener {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	require.NoError(t, err)

	listener, err := net.ListenTCP("tcp", addr)

	require.NoError(t, err)
	return listener
}

// Create IPv4 0.0.0.0 listener using random port
// and ensure it is not accessible via IPv6 request.
func TestEndpoints_NetworkCreateTCPSocketIPv4(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	config.NetworkAddress = "0.0.0.0:0"
	require.NoError(t, endpoints.Up(config))

	address, certificate := endpoints.NetworkAddressAndCert()
	parts := strings.Split(address, ":")
	ipv6Address := fmt.Sprintf("[::1]:%s", parts[1])
	ipv4Address := fmt.Sprintf("127.0.0.1:%s", parts[1])

	// Check accessibility over IPv4 request
	assert.NoError(t, httpGetOverTLSSocket(ipv4Address, certificate))

	// Check accessibility over IPv6 request
	assert.Error(t, httpGetOverTLSSocket(ipv6Address, certificate))
}
