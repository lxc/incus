package util_test

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/util"
)

// The connection returned by the dialer is paired with the one returned by the
// Accept() method of the listener.
func TestInMemoryNetwork(t *testing.T) {
	listener, dialer := util.InMemoryNetwork()
	client := dialer()
	server, err := listener.Accept()
	require.NoError(t, err)

	go func() {
		_, err := client.Write([]byte("hello"))
		require.NoError(t, err)
	}()

	buffer := make([]byte, 5)
	n, err := server.Read(buffer)
	require.NoError(t, err)

	assert.Equal(t, 5, n)
	assert.Equal(t, []byte("hello"), buffer)

	// Closing the server makes all further client reads and
	// writes fail.
	err = server.Close()
	assert.NoError(t, err)
	_, err = client.Read(buffer)
	assert.Equal(t, io.EOF, err)
	_, err = client.Write([]byte("hello"))
	assert.EqualError(t, err, "io: read/write on closed pipe")
}
