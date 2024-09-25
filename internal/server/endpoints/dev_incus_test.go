package endpoints_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/shared/util"
)

// If no socket-based activation is detected, a new local unix socket will be
// created.
func TestEndpoints_DevIncusCreateUnixSocket(t *testing.T) {
	endpoints, config, cleanup := newEndpoints(t)
	defer cleanup()

	require.NoError(t, endpoints.Up(config))

	path := endpoints.DevIncusSocketPath()
	assert.NoError(t, httpGetOverUnixSocket(path))

	// The unix socket file gets removed after shutdown.
	cleanup()
	assert.Equal(t, false, util.PathExists(path))
}
