package instance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOVNNICClaimConfigValidation(t *testing.T) {
	for _, key := range []string{"volatile.eth0.last_state.ovn.host", "volatile.eth0.last_state.ovn.physical"} {
		validator, err := ConfigKeyChecker(key, "")
		require.NoError(t, err)
		require.NoError(t, validator(""))
		require.NoError(t, validator(`{"Version":1,"NetworkID":3,"SourceNodeID":2,"InstanceID":4,"InstanceUUID":"original-uuid","DeviceName":"eth0","Alias":"original-host"}`))
		for _, value := range []string{"broken", "{}", "null", `{"Version":2,"NetworkID":3,"SourceNodeID":2,"InstanceID":4,"InstanceUUID":"original-uuid","DeviceName":"eth0"}`} {
			require.Error(t, validator(value), value)
		}
	}
}
