package main

import (
	"context"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/shared/api"
)

func TestOVNNICNewInstanceConfig(t *testing.T) {
	config := map[string]string{
		"volatile.eth0.last_state.ovn.host":     `{"InstanceID":42}`,
		"volatile.eth0.host_name":               "original-host",
		"volatile.eth0.last_state.created":      "true",
		"volatile.eth0.hwaddr":                  "00:16:3e:00:00:01",
		"volatile.eth1.last_state.ovn.physical": "malformed-original",
		"volatile.eth1.last_state.vf.id":        "7",
		"volatile.eth1.last_state.vf.parent":    "original-parent",
		"volatile.sibling.host_name":            "sibling-host",
		"volatile.uuid":                         "target-uuid",
		"user.keep":                             "user-value",
	}

	original := maps.Clone(config)
	result := ovnNICNewInstanceConfig(config)
	require.Equal(t, map[string]string{
		"volatile.eth0.hwaddr":       "00:16:3e:00:00:01",
		"volatile.sibling.host_name": "sibling-host",
		"volatile.uuid":              "target-uuid",
		"user.keep":                  "user-value",
	}, result)
	require.Equal(t, original, config, "normalizing a new record cannot retire the source allocation")
	require.Equal(t, result, ovnNICNewInstanceConfig(result))
	copyConfig := ovnNICNewCopyConfig(config, "target-uuid", true)
	require.NotContains(t, copyConfig, "volatile.uuid", "first-refresh creation cannot share the source's port identity")
	require.Equal(t, "user-value", copyConfig["user.keep"])
	require.Equal(t, "target-uuid", ovnNICNewCopyConfig(config, "other-source", true)["volatile.uuid"], "a distinct requested identity is preserved")
	require.Equal(t, "target-uuid", ovnNICNewCopyConfig(config, "target-uuid", false)["volatile.uuid"], "internal stopped moves preserve the source identity")
	require.Equal(t, original, config)
}

type ovnProjectMoveInstance struct {
	instance.Instance
	running bool
	devices deviceConfig.Devices
}

func (i *ovnProjectMoveInstance) IsRunning() bool                       { return i.running }
func (i *ovnProjectMoveInstance) Project() api.Project                  { return api.Project{Name: "default"} }
func (i *ovnProjectMoveInstance) ExpandedDevices() deviceConfig.Devices { return i.devices }

func TestOVNNICLiveProjectMoveAdmission(t *testing.T) {
	s := newWorkloadMaintenanceBoundaryState(t, db.ClusterMemberStateCreated)
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNetwork(ctx, "default", "project-move-ovn", "", db.NetworkTypeOVN, nil)
		return err
	})
	require.NoError(t, err)

	for _, test := range []struct {
		name, project          string
		live, running, refused bool
		nic                    deviceConfig.Device
	}{
		{"managed-ovn-live", "other", true, true, true, deviceConfig.Device{"type": "nic", "network": "project-move-ovn"}},
		{"explicit-ovn-live", "other", true, true, true, deviceConfig.Device{"type": "nic", "nictype": "ovn"}},
		{"bridged-live", "other", true, true, false, deviceConfig.Device{"type": "nic", "nictype": "bridged", "parent": "br0"}},
		{"ovn-stopped", "other", false, false, false, deviceConfig.Device{"type": "nic", "network": "project-move-ovn"}},
		{"ovn-same-project", "default", true, true, false, deviceConfig.Device{"type": "nic", "network": "project-move-ovn"}},
		{"ovn-member-only", "", true, true, false, deviceConfig.Device{"type": "nic", "network": "project-move-ovn"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			inst := &ovnProjectMoveInstance{running: test.running, devices: deviceConfig.Devices{"eth0": test.nic}}
			original := inst.devices.Clone()
			// The embedded interface is nil: Stop, capture or dispatch would panic.
			err := instanceOVNLiveProjectMoveAdmission(s, inst, api.InstancePost{Live: test.live, Project: test.project})
			if test.refused {
				require.EqualError(t, err, `Instance must be stopped to move across projects with OVN NIC "eth0"`)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, original, inst.devices)
			require.Equal(t, test.running, inst.running)
		})
	}
}
