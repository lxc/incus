package drivers

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnsureClientPrefix(t *testing.T) {
	for client, expected := range map[string]string{
		"admin":                        "client.admin",
		"csi-rbd-provisioner.1":        "client.csi-rbd-provisioner.1",
		"client.admin":                 "client.admin",
		"client.csi-rbd-provisioner.1": "client.csi-rbd-provisioner.1",
	} {
		t.Run(client, func(t *testing.T) {
			require.Equal(t, expected, EnsureClientPrefix(client))
		})
	}
}
