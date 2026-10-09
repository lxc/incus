package device

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOVNUndoUsesNewAllocationAfterInstanceFieldsRevert(t *testing.T) {
	current := map[string]string{"host_name": "new-veth", "last_state.vf.id": "7"}
	nic := nicOVN{deviceCommon: deviceCommon{volatileGet: func() map[string]string { return current }}}
	undo := nic.OVNUndoVolatile()
	// The outer instance has restored its old config before cleaning up the attempted replacement.
	current = map[string]string{"host_name": "old-veth", "last_state.vf.id": "3"}
	err := undo(func() error {
		require.Equal(t, "new-veth", nic.volatileGet()["host_name"])
		require.Equal(t, "7", nic.volatileGet()["last_state.vf.id"])
		return context.DeadlineExceeded
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, "old-veth", nic.volatileGet()["host_name"])
	require.Equal(t, "3", nic.volatileGet()["last_state.vf.id"])
}
