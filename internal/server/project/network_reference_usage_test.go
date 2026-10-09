//go:build linux && cgo && !agent

package project_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/shared/api"
)

func TestNetworkReferenceUsageCommitIndependentReleaseAndExclusion(t *testing.T) {
	f := newProfileReferenceFixture(t, 2, "shared")
	request := f.request(api.ProfilePut{Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "new", "security.acls": "old-acl"}}}, 0)
	// Inspect the committed union at the real post-commit, pre-application boundary.
	dispatched := 0
	commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, request, func(ctx context.Context, consumer db.ProfileReferenceConsumer) error {
		dispatched++
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			_, profile, err := tx.GetProfile(ctx, "default", "tracked")
			require.NoError(t, err)
			require.Equal(t, "new", profile.Devices["eth0"]["network"])
			usage, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, nil)
			require.NoError(t, err)
			require.Len(t, usage.CurrentInstances, 4)
			require.Len(t, usage.CurrentProfiles, 2)
			require.Len(t, usage.Retained, 12)
			for _, current := range usage.CurrentInstances {
				require.Equal(t, f.newNetwork, current.NetworkID)
				require.Equal(t, "default", current.NetworkProject)
				require.Equal(t, "tenant", current.InstanceProject)
				require.NotEqual(t, current.Key.ProjectID, current.NetworkProjectID)
			}

			old, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{NetworkID: f.oldNetwork, ACLID: f.acl}, nil)
			require.NoError(t, err)
			require.Empty(t, old.CurrentInstances)
			require.Empty(t, old.CurrentProfiles)
			require.Len(t, old.Retained, 4)
			return nil
		})
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, dispatched)
	first := f.identity(t, commit.Token, f.instances[0])
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, first))
		all, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, nil)
		require.NoError(t, err)
		key := project.InstanceNetworkReferenceKey{InstanceID: first.InstanceID, ProjectID: first.ProjectID, Device: "eth0"}
		excluded, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, &key)
		require.NoError(t, err)
		require.Len(t, excluded.CurrentInstances, 2)
		require.Equal(t, all.CurrentProfiles, excluded.CurrentProfiles)
		require.Equal(t, all.Retained, excluded.Retained)
		attemptRows := 0
		for _, row := range excluded.Retained {
			if row.AttemptToken == first.Token {
				attemptRows++
			}
		}
		require.Equal(t, 4, attemptRows)
		key.ProjectID++
		wrongProject, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, &key)
		require.NoError(t, err)
		require.Equal(t, all, wrongProject)
		require.NoError(t, tx.SealProfileReferenceChildren(ctx, first, nil))
		require.NoError(t, tx.MarkProfileReferenceApplied(ctx, first))
		return tx.FinalizeProfileReferenceApply(ctx, first)
	})
	second := f.identity(t, commit.Token, f.instances[1])
	var protected []db.RetainedProfileReference
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		usage, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{NetworkID: f.oldNetwork}, nil)
		require.NoError(t, err)
		require.Len(t, usage.Retained, 4)
		for _, row := range usage.Retained {
			require.Equal(t, second.InstanceID, row.InstanceID)
		}

		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, second))
		all, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, nil)
		require.NoError(t, err)
		protected = all.Retained
		require.Len(t, protected, 10)
		return tx.SealProfileReferenceChildren(ctx, second, nil)
	})
	stale := second
	stale.Sequence++
	err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error { return tx.FinalizeProfileReferenceApply(ctx, stale) })
	require.Error(t, err)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		before, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, nil)
		require.NoError(t, err)
		require.Equal(t, protected, before.Retained)
		return tx.FailProfileReferenceApply(ctx, second, db.ProfileReferenceRolledBack)
	})
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		usage, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, nil)
		require.NoError(t, err)
		require.Equal(t, protected, usage.Retained)
		return nil
	})
	f.complete(t, f.identity(t, commit.Token, second.InstanceID))
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		usage, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{ACLID: f.acl}, nil)
		require.NoError(t, err)
		require.Empty(t, usage.Retained)
		require.Len(t, usage.CurrentInstances, 2)
		require.Len(t, usage.CurrentProfiles, 1)
		for _, current := range usage.CurrentInstances {
			require.Equal(t, f.newNetwork, current.NetworkID)
			require.Equal(t, "default", current.ACLProject)
			require.Equal(t, f.acl, current.ACLID)
		}

		return nil
	})
}

func TestNetworkReferenceUsageSharingChangesDoNotReinterpretRetained(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "shared")
	_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "old-acl", "mtu": "1500"}}}, 0), nil)
	require.NoError(t, err)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		before, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, nil)
		require.NoError(t, err)
		tenantID, err := cluster.GetProjectID(ctx, tx.Tx(), "tenant")
		require.NoError(t, err)
		// Fixture-only setting change demonstrates identity preservation; no apply/recovery is attempted.
		require.NoError(t, cluster.UpdateProject(ctx, tx.Tx(), "tenant", api.ProjectPut{Config: map[string]string{"features.profiles": "false", "features.networks": "true"}}))
		tenantNetwork, err := tx.GetNetworkID(ctx, "tenant", "old")
		require.NoError(t, err)
		tenantACL, err := cluster.GetNetworkACLID(ctx, tx.Tx(), "tenant", "old-acl")
		require.NoError(t, err)
		after, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, nil)
		require.NoError(t, err)
		require.Equal(t, before.Retained, after.Retained)
		require.Len(t, after.CurrentInstances, 2)
		for _, row := range after.CurrentInstances {
			require.Equal(t, tenantNetwork, row.NetworkID)
			require.Equal(t, tenantID, row.NetworkProjectID)
			require.Equal(t, "tenant", row.NetworkProject)
			if row.ACLID != 0 {
				require.Equal(t, tenantACL, row.ACLID)
				require.Equal(t, "tenant", row.ACLProject)
			}
		}
		old, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{NetworkID: f.oldNetwork, ACLID: f.acl}, nil)
		require.NoError(t, err)
		require.Empty(t, old.CurrentInstances)
		require.NotEmpty(t, old.Retained)
		local, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{NetworkID: tenantNetwork, NetworkProjectID: tenantID, ACLID: tenantACL, ACLProjectID: tenantID}, nil)
		require.NoError(t, err)
		require.Len(t, local.CurrentInstances, 1)
		require.Empty(t, local.Retained)
		wrong, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{NetworkID: f.oldNetwork, ACLProjectID: tenantID}, nil)
		require.NoError(t, err)
		require.Empty(t, wrong.CurrentInstances)
		require.Empty(t, wrong.CurrentProfiles)
		require.Empty(t, wrong.Retained)
		return nil
	})
}

func TestNetworkReferenceUsageWholeDeviceOverrides(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprintf("local-%t", local), func(t *testing.T) {
			f := newProfileReferenceFixture(t, 1, "inherited")
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				id, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "override"})
				require.NoError(t, err)
				devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}})
				require.NoError(t, err)
				require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), id, devices))
				order := []string{"tracked", "override"}
				if local {
					order = []string{"override", "tracked"}
					require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instances[0], devices))
				}

				require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), "tenant", order))
				usage, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, nil)
				require.NoError(t, err)
				require.Len(t, usage.CurrentInstances, 1)
				row := usage.CurrentInstances[0]
				require.Equal(t, f.newNetwork, row.NetworkID)
				require.Zero(t, row.ACLID)
				require.Equal(t, map[string]string{"type": "nic", "network": "new"}, row.Config)
				require.Empty(t, usage.Retained)
				// Standalone overridden profile references are still explicit owners.
				old, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{ACLID: f.acl}, nil)
				require.NoError(t, err)
				require.Empty(t, old.CurrentInstances)
				require.Len(t, old.CurrentProfiles, 1)
				if !local {
					require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), "tenant", []string{"override", "tracked"}))
					reversed, err := project.CurrentInstanceNetworkReferences(ctx, tx, db.NetworkReferenceFilter{ACLID: f.acl}, nil)
					require.NoError(t, err)
					require.Len(t, reversed, 1)
					require.Equal(t, f.oldNetwork, reversed[0].NetworkID)
				}

				return nil
			})
		})
	}
}

func TestNetworkReferenceUsageSameNamedBridgeInstances(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "shared")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "consumer-0", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(id), "default", []string{"tracked"}))
		// No accounting baseline exists for this ordinary current-only instance.
		usage, err := project.CurrentInstanceNetworkReferences(ctx, tx, db.NetworkReferenceFilter{ACLID: f.acl}, nil)
		require.NoError(t, err)
		require.Len(t, usage, 2)
		keys := map[project.InstanceNetworkReferenceKey]bool{}
		for _, row := range usage {
			require.Equal(t, "consumer-0", row.InstanceName)
			require.Equal(t, "bridge", row.NetworkType)
			keys[row.Key] = true
		}

		require.Len(t, keys, 2)
		excluded, err := project.CurrentInstanceNetworkReferences(ctx, tx, db.NetworkReferenceFilter{ACLID: f.acl}, &usage[0].Key)
		require.NoError(t, err)
		require.Len(t, excluded, 1)
		require.Equal(t, usage[1].Key, excluded[0].Key)
		return nil
	})
}

func TestNetworkReferenceUsageResolvesEachDeviceAndStandaloneProfile(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "shared")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		tenantID, err := cluster.GetProjectID(ctx, tx.Tx(), "tenant")
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProject(ctx, tx.Tx(), "tenant", api.ProjectPut{Config: map[string]string{"features.profiles": "true", "features.networks": "true", "restricted": "true", "restricted.networks.access": "old"}}))
		private, err := tx.CreateNetwork(ctx, "tenant", "private", "", db.NetworkTypeBridge, nil)
		require.NoError(t, err)
		_, err = tx.CreateNetwork(ctx, "default", "private", "", db.NetworkTypeBridge, nil)
		require.NoError(t, err)
		tenantACL, err := cluster.GetNetworkACLID(ctx, tx.Tx(), "tenant", "old-acl")
		require.NoError(t, err)
		profileID, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "tenant", Name: "mixed"})
		require.NoError(t, err)
		devices, err := cluster.APIToDevices(map[string]map[string]string{
			"shared":  {"type": "nic", "network": "old", "security.acls": "old-acl"},
			"private": {"type": "nic", "network": "private", "security.acls": "old-acl"},
			"raw":     {"type": "nic", "nictype": "bridged", "parent": "unmanaged"},
			"disk":    {"type": "disk", "path": "/", "pool": "unused"},
		})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), profileID, devices))
		require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), "tenant", []string{"mixed"}))
		usage, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, nil)
		require.NoError(t, err)
		require.Len(t, usage.CurrentInstances, 4)
		for _, row := range usage.CurrentInstances {
			switch row.Device {
			case "shared":
				require.Equal(t, f.oldNetwork, row.NetworkID)
				require.Equal(t, "default", row.NetworkProject)
				if row.ACLID != 0 {
					require.Equal(t, f.acl, row.ACLID)
				}

			case "private":
				require.Equal(t, private, row.NetworkID)
				require.Equal(t, "tenant", row.NetworkProject)
				if row.ACLID != 0 {
					require.Equal(t, tenantACL, row.ACLID)
				}

			default:
				t.Fatalf("Unexpected managed device %q", row.Device)
			}
		}
		// Removing the consumer does not remove the standalone profile references.
		require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), "tenant", nil))
		standalone, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{ACLID: tenantACL, NetworkID: private}, nil)
		require.NoError(t, err)
		require.Empty(t, standalone.CurrentInstances)
		require.Len(t, standalone.CurrentProfiles, 1)
		require.Equal(t, profileID, standalone.CurrentProfiles[0].ProfileID)
		require.Equal(t, tenantID, standalone.CurrentProfiles[0].ProjectID)
		shared, err := project.CurrentProfileNetworkReferences(ctx, tx, db.NetworkReferenceFilter{ACLID: f.acl})
		require.NoError(t, err)
		require.Len(t, shared, 2)
		for _, row := range shared {
			require.Equal(t, "default", row.NetworkProject)
		}

		return nil
	})
}

func TestNetworkReferenceUsageReadErrorReturnsNoPartialUnion(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "default")
	_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
	require.NoError(t, err)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		retained, err := tx.RetainedProfileReferenceUsage(ctx, db.NetworkReferenceFilter{})
		require.NoError(t, err)
		require.NotEmpty(t, retained)
		devices, err := cluster.APIToDevices(map[string]map[string]string{"broken": {"type": "nic", "network": "missing"}})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instances[0], devices))
		usage, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{}, nil)
		require.Error(t, err)
		require.Nil(t, usage)
		invalid, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{NetworkID: -1}, nil)
		require.Error(t, err)
		require.Nil(t, invalid)
		return nil
	})
}
