package endpoints_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cyphar/incus/shared"
)

// If no socket-based activation is detected, a new local unix socket will be
// created.
func TestEndpoints_DevLxdCreateUnixSocket(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	require.NoError(t, endpoints.Up(config))

	path := endpoints.DevLxdSocketPath()
	assert.NoError(t, httpGetOverUnixSocket(path))

	// The unix socket file gets removed after shutdown.
	cleanup()
	assert.Equal(t, false, shared.PathExists(path))
}
