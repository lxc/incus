package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/config"
)

// If the givne values contain invalid keys, they are ignored.
func TestSafeLoad_IgnoreInvalidKeys(t *testing.T) {
	schema := config.Schema{"bar": {}}
	values := map[string]string{
		"foo": "garbage",
		"bar": "x",
	}

	m, err := config.SafeLoad(schema, values)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"bar": "x"}, m.Dump())
}
