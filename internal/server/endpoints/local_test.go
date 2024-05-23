package endpoints_test

import (
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/shared/util"
)

// If no socket-based activation is detected, a new local unix socket will be
// created.
func TestEndpoints_LocalCreateUnixSocket(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	require.NoError(t, endpoints.Up(config))

	path := endpoints.LocalSocketPath()
	assert.NoError(t, httpGetOverUnixSocket(path))

	// The unix socket file gets removed after shutdown.
	cleanup()
	assert.Equal(t, false, util.PathExists(path))
}

// If socket-based activation is detected, it will be used for binding the API
// Endpoints' unix socket.
func TestEndpoints_LocalSocketBasedActivation(t *testing.T) {
	listener := newUnixListener(t)
	defer func() { _ = listener.Close() }() // This will also remove the underlying file

	file, err := listener.File()
	require.NoError(t, err)
	defer func() { _ = file.Close() }()

	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	setupSocketBasedActivation(endpoints, file)

	require.NoError(t, endpoints.Up(config))

	assertNoSocketBasedActivation(t)

	path := endpoints.LocalSocketPath()
	assert.NoError(t, httpGetOverUnixSocket(path))

	// The unix socket file does not get removed after shutdown (thanks to
	// this change in Go 1.6:
	//
	// https://github.com/golang/go/commit/a4fd325c178ea29f554d69de4f2c3ffa09b53874
	//
	// which prevents listeners created from file descriptors from removing
	// their socket files on close).
	cleanup()
	assert.Equal(t, true, util.PathExists(path))
}

// If a custom group for the unix socket is specified, but no such one exists,
// an error is returned.
func TestEndpoints_LocalUnknownUnixGroup(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	config.LocalUnixSocketGroup = "xquibaz"
	err := endpoints.Up(config)

	assert.EqualError(
		t, err, "Local endpoint: cannot get group ID of 'xquibaz': group: unknown group xquibaz")
}

// If another endpoint is already listening on the unix socket, an error is returned.
func TestEndpoints_LocalAlreadyRunning(t *testing.T) {
	endpoints1, config1, cleanup1 := newEndpoints(t)
	defer cleanup1()

	require.NoError(t, endpoints1.Up(config1))

	endpoints2, config2, cleanup2 := newEndpoints(t)
	config2.Dir = config1.Dir
	config2.UnixSocket = config1.UnixSocket
	defer cleanup2()

	err := endpoints2.Up(config2)
	assert.EqualError(t, err, "Local endpoint: Incus is already running")
}

// Create a UnixListener using a random and unique file name.
func newUnixListener(t *testing.T) *net.UnixListener {
	file, err := os.CreateTemp("", "incus-endpoints-test")
	require.NoError(t, err)

	path := file.Name()
	require.NoError(t, file.Close())
	err = os.Remove(path)
	require.NoError(t, err)

	addr, err := net.ResolveUnixAddr("unix", path)
	require.NoError(t, err)

	listener, err := net.ListenUnix("unix", addr)

	require.NoError(t, err)
	return listener
}
