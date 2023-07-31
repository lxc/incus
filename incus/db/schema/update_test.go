package schema_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cyphar/incus/incus/db/schema"
	"github.com/cyphar/incus/shared"
)

// A Go source file matching the given prefix is created in the calling
// package.
func TestDotGo(t *testing.T) {
	updates := map[int]schema.Update{
		1: updateCreateTable,
		2: updateInsertValue,
	}

	require.NoError(t, schema.DotGo(updates, "xyz"))
	require.Equal(t, true, shared.PathExists("xyz.go"))
	require.NoError(t, os.Remove("xyz.go"))
}
