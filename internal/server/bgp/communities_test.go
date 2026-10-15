package bgp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCommunities(t *testing.T) {
	communities, err := ParseCommunities("65000:100, 65000:200,65000:100")
	require.NoError(t, err)
	require.Equal(t, []uint32{65000<<16 | 100, 65000<<16 | 200}, communities)

	_, err = ParseCommunities("65000:100,invalid")
	require.Error(t, err)
}
