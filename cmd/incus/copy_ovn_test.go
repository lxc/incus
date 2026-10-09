package main

import (
	"errors"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/shared/api"
)

func TestCopyRefreshForMove(t *testing.T) {
	calls := 0
	var lookupErr error = api.StatusErrorf(404, "missing target")
	lookup := func() error { calls++; return lookupErr }
	refresh, err := copyRefreshForMove(true, true, lookup)
	require.NoError(t, err)
	require.False(t, refresh, "the first move copy must preserve the source UUID")
	require.Equal(t, 1, calls)
	lookupErr = nil
	refresh, err = copyRefreshForMove(true, true, lookup)
	require.NoError(t, err)
	require.True(t, refresh, "subsequent rounds refresh the existing target")
	lookupErr = errors.New("target lookup failed")
	_, err = copyRefreshForMove(true, true, lookup)
	require.ErrorIs(t, err, lookupErr)
	calls = 0
	refresh, err = copyRefreshForMove(false, true, lookup)
	require.NoError(t, err)
	require.True(t, refresh, "ordinary first refresh copies still request a fresh identity")
	require.Zero(t, calls)
	refresh, err = copyRefreshForMove(true, false, lookup)
	require.NoError(t, err)
	require.False(t, refresh)
	require.Zero(t, calls)
}

func TestRefreshCopyConfigPreservesTargetOVNIdentity(t *testing.T) {
	source := map[string]string{
		"volatile.uuid": "source-uuid", "volatile.eth0.last_state.ovn.host": "source-claim",
		"volatile.eth0.host_name": "source-host", "volatile.eth0.last_state.mtu": "1400",
		"volatile.eth0.hwaddr": "00:16:3e:00:00:01", "volatile.eth1.host_name": "bridged-host",
		"volatile.uuid.generation": "source-generation", "user.config": "refreshed",
	}

	original := maps.Clone(source)
	for _, target := range []map[string]string{
		{"volatile.uuid": "fresh-target-uuid", "user.config": "old"},
		{"volatile.uuid": "existing-target-uuid", "volatile.eth0.last_state.ovn.host": "target-claim", "volatile.eth0.host_name": "target-host"},
	} {
		before := maps.Clone(target)
		result := refreshCopyConfig(source, target)
		require.Equal(t, target["volatile.uuid"], result["volatile.uuid"])
		require.Equal(t, target["volatile.eth0.last_state.ovn.host"], result["volatile.eth0.last_state.ovn.host"])
		require.Equal(t, target["volatile.eth0.host_name"], result["volatile.eth0.host_name"])
		require.NotContains(t, result, "volatile.eth0.last_state.mtu")
		require.Equal(t, "refreshed", result["user.config"])
		require.Equal(t, source["volatile.eth0.hwaddr"], result["volatile.eth0.hwaddr"])
		require.Equal(t, "bridged-host", result["volatile.eth1.host_name"])
		require.Equal(t, "source-generation", result["volatile.uuid.generation"])
		require.Equal(t, before, target)
	}

	require.Equal(t, original, source)
}
