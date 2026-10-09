//go:build linux && cgo && !agent

package project_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/shared/api"
)

func TestNetworkACLProtectionOwnersAndExclusions(t *testing.T) {
	f := newProfileReferenceFixture(t, 2, "shared")
	// Real bridge consumers exercise the shared projection; OVN completion is covered in acl tests.
	commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "old-acl", "mtu": "1500"}}}, 0), nil)
	require.NoError(t, err)
	identity := f.identity(t, commit.Token, f.instances[0])
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
		resourceID, err := cluster.GetProjectID(ctx, tx.Tx(), "default")
		require.NoError(t, err)
		all, err := project.ReadNetworkACLProtection(ctx, tx, resourceID, project.ACLCurrentExclusion{})
		require.NoError(t, err)
		require.Len(t, all.Owners.CurrentInstances, 4)
		require.Len(t, all.Owners.CurrentProfiles, 2)
		require.NotEmpty(t, all.Owners.Retained)
		key := project.InstanceNetworkReferenceKey{InstanceID: identity.InstanceID, ProjectID: identity.ProjectID, Device: "eth0"}
		excluded, err := project.ReadNetworkACLProtection(ctx, tx, resourceID, project.ACLCurrentExclusion{Kind: "instance", Instance: &key})
		require.NoError(t, err)
		require.Len(t, excluded.Owners.CurrentInstances, 2)
		require.Equal(t, all.Owners.CurrentProfiles, excluded.Owners.CurrentProfiles)
		require.Equal(t, all.Owners.Retained, excluded.Owners.Retained)
		consumer, attempt, networkOnly := false, false, false
		for _, row := range excluded.Owners.Retained {
			consumer = consumer || row.ConsumerID > 0
			attempt = attempt || row.AttemptToken == identity.Token
			networkOnly = networkOnly || row.ACLID == 0
			require.NotEqual(t, row.ProjectID, row.NetworkProjectID)
		}

		require.True(t, consumer)
		require.True(t, attempt)
		require.True(t, networkOnly)
		profile := project.ProfileNetworkReferenceKey{ProjectID: resourceID, ProfileID: f.profileID, Device: "eth0"}
		profileExcluded, err := project.ReadNetworkACLProtection(ctx, tx, resourceID, project.ACLCurrentExclusion{Kind: "profile", Profile: &profile})
		require.NoError(t, err)
		require.Empty(t, profileExcluded.Owners.CurrentProfiles)
		require.Equal(t, all.Owners.CurrentInstances, profileExcluded.Owners.CurrentInstances)
		require.Equal(t, all.Owners.Retained, profileExcluded.Owners.Retained)
		network := project.NetworkUsageKey{ProjectID: resourceID, NetworkID: f.oldNetwork}
		networkExcluded, err := project.ReadNetworkACLProtection(ctx, tx, resourceID, project.ACLCurrentExclusion{Kind: "network", Network: &network})
		require.NoError(t, err)
		require.Equal(t, all.Owners, networkExcluded.Owners, "network exclusion cannot suppress any current or retained NIC owner")
		// Config snapshots are independent copies, not aliases into retained or DB state.
		excluded.Owners.CurrentInstances[0].Config["network"] = "mutated-copy"
		again, err := project.ReadNetworkACLProtection(ctx, tx, resourceID, project.ACLCurrentExclusion{})
		require.NoError(t, err)
		require.Equal(t, all, again)
		return nil
	})
}

func TestNetworkACLProtectionTaggedExclusionValidation(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "shared")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		resourceID, err := cluster.GetProjectID(ctx, tx.Tx(), "default")
		require.NoError(t, err)
		ownerID, err := cluster.GetProjectID(ctx, tx.Tx(), "tenant")
		require.NoError(t, err)
		instance := project.InstanceNetworkReferenceKey{InstanceID: f.instances[0], ProjectID: ownerID, Device: "eth0"}
		profile := project.ProfileNetworkReferenceKey{ProfileID: f.profileID, ProjectID: resourceID, Device: "eth0"}
		network := project.NetworkUsageKey{ProjectID: resourceID, NetworkID: f.oldNetwork}
		for _, e := range []project.ACLCurrentExclusion{
			{Kind: "unknown"},
			{Instance: &instance},
			{Kind: "instance"},
			{Kind: "instance", Instance: &instance, Profile: &profile},
			{Kind: "network", Profile: &profile},
			{Kind: "profile", Network: &network},
			{Kind: "instance", Instance: &project.InstanceNetworkReferenceKey{InstanceID: f.instances[0], ProjectID: resourceID, Device: "eth0"}},
			{Kind: "instance", Instance: &project.InstanceNetworkReferenceKey{InstanceID: -1, ProjectID: ownerID, Device: "eth0"}},
			{Kind: "profile", Profile: &project.ProfileNetworkReferenceKey{ProfileID: f.profileID, ProjectID: ownerID, Device: "eth0"}},
			{Kind: "profile", Profile: &project.ProfileNetworkReferenceKey{ProfileID: f.profileID, ProjectID: resourceID}},
			{Kind: "network", Network: &project.NetworkUsageKey{ProjectID: ownerID, NetworkID: f.oldNetwork}},
			{Kind: "network", Network: &project.NetworkUsageKey{ProjectID: resourceID, NetworkID: 999999}},
		} {
			p, err := project.ReadNetworkACLProtection(ctx, tx, resourceID, e)
			require.Error(t, err)
			require.Nil(t, p)
		}

		instance.Device = "already-removed"
		all, err := project.ReadNetworkACLProtection(ctx, tx, resourceID, project.ACLCurrentExclusion{})
		require.NoError(t, err)
		p, err := project.ReadNetworkACLProtection(ctx, tx, resourceID, project.ACLCurrentExclusion{Kind: "instance", Instance: &instance})
		require.NoError(t, err)
		require.Equal(t, all, p)
		return nil
	})
}

func TestNetworkACLProtectionProjectDevicesAndWholeOverrides(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "profile-order", true: "local-device"}[local], func(t *testing.T) {
			f := newProfileReferenceFixture(t, 1, "shared")
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				resourceID, err := cluster.GetProjectID(ctx, tx.Tx(), "default")
				require.NoError(t, err)
				var node string
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
				id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "consumer-0", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
				require.NoError(t, err)
				require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(id), "default", []string{"tracked"}))
				before, err := project.ReadNetworkACLProtection(ctx, tx, resourceID, project.ACLCurrentExclusion{})
				require.NoError(t, err)
				keys := map[project.InstanceNetworkReferenceKey]bool{}
				for _, row := range before.Owners.CurrentInstances {
					require.Equal(t, "consumer-0", row.InstanceName)
					require.Equal(t, "bridge", row.NetworkType)
					keys[row.Key] = true
				}

				require.Len(t, keys, 2)
				override, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "override"})
				require.NoError(t, err)
				devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}, "eth1": {"type": "nic", "network": "old", "security.acls": "old-acl"}})
				require.NoError(t, err)
				require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), override, devices))
				order := []string{"tracked", "override"}
				if local {
					order = []string{"override", "tracked"}
					require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instances[0], devices))
				}

				require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), "tenant", order))
				after, err := project.ReadNetworkACLProtection(ctx, tx, resourceID, project.ACLCurrentExclusion{})
				require.NoError(t, err)
				rows := 0
				for _, row := range after.Owners.CurrentInstances {
					if row.Key.InstanceID != f.instances[0] {
						continue
					}

					rows++
					if row.Device == "eth0" {
						require.Zero(t, row.ACLID)
						require.Equal(t, f.newNetwork, row.NetworkID)
					}

					if row.Device == "eth1" {
						require.Equal(t, f.oldNetwork, row.NetworkID)
					}
				}
				require.Equal(t, 3, rows)
				exclude := project.InstanceNetworkReferenceKey{InstanceID: f.instances[0], ProjectID: after.Owners.CurrentInstances[0].Key.ProjectID, Device: "eth0"}
				for _, row := range after.Owners.CurrentInstances {
					if row.Key.InstanceID == f.instances[0] {
						exclude.ProjectID = row.Key.ProjectID
					}
				}
				excluded, err := project.ReadNetworkACLProtection(ctx, tx, resourceID, project.ACLCurrentExclusion{Kind: "instance", Instance: &exclude})
				require.NoError(t, err)
				want := []project.CurrentInstanceNetworkReference{}
				for _, row := range after.Owners.CurrentInstances {
					if row.Key != exclude {
						want = append(want, row)
					}
				}
				require.Equal(t, want, excluded.Owners.CurrentInstances, "exact exclusion preserves another device and same-named sibling owner")
				require.Len(t, excluded.Owners.CurrentInstances, 4)
				require.Equal(t, after.Owners.CurrentProfiles, excluded.Owners.CurrentProfiles)
				// Same-named tenant resources do not receive the shared-default owners.
				tenantID, err := cluster.GetProjectID(ctx, tx.Tx(), "tenant")
				require.NoError(t, err)
				tenant, err := project.ReadNetworkACLProtection(ctx, tx, tenantID, project.ACLCurrentExclusion{})
				require.NoError(t, err)
				require.Empty(t, tenant.Owners.CurrentInstances)
				return nil
			})
		})
	}
}

func TestNetworkACLProtectionCatalogStatesSubjectsAndErrors(t *testing.T) {
	f := newProfileReferenceFixture(t, 0, "default")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.GetProjectID(ctx, tx.Tx(), "default")
		require.NoError(t, err)
		other, err := cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "subject"})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), f.acl, &api.NetworkACLPut{Ingress: []api.NetworkACLRule{{Source: "subject,@peer,@internal,@external,#internal,#external,$set,192.0.2.7/24,2001:db8::7/64,192.0.2.1-192.0.2.3", Destination: "unresolved-opposite-field", State: "disabled"}}, Egress: []api.NetworkACLRule{{Destination: "subject", Source: "unresolved-opposite-field", State: "logged"}}}))
		require.NoError(t, tx.UpdateNetwork(ctx, "default", "old", "", map[string]string{"security.acls": "old-acl"}))
		for _, state := range []int{0, 1, 2, 3, 4} {
			// Isolated state perturbation verifies absence of a Created-only catalog filter.
			_, err := tx.Tx().ExecContext(ctx, `UPDATE networks SET state=? WHERE id=?`, state, f.oldNetwork)
			require.NoError(t, err)
			p, err := project.ReadNetworkACLProtection(ctx, tx, id, project.ACLCurrentExclusion{})
			require.NoError(t, err)
			require.Len(t, p.NetworkRoots, 1)
			require.Equal(t, project.NetworkACLRoot{Network: project.NetworkUsageKey{ProjectID: id, NetworkID: f.oldNetwork}, ACL: project.ACLUsageKey{ProjectID: id, ACLID: f.acl}}, p.NetworkRoots[0])
			require.Len(t, p.Subjects, 2)
			for _, edge := range p.Subjects {
				require.Equal(t, project.ACLUsageKey{ProjectID: id, ACLID: f.acl}, edge.Source)
				require.Equal(t, project.ACLUsageKey{ProjectID: id, ACLID: other}, edge.Target)
				require.Zero(t, edge.RuleIndex)
				require.Contains(t, []string{"disabled", "logged"}, edge.State)
			}

			key := project.NetworkUsageKey{ProjectID: id, NetworkID: f.oldNetwork}
			excluded, err := project.ReadNetworkACLProtection(ctx, tx, id, project.ACLCurrentExclusion{Kind: "network", Network: &key})
			require.NoError(t, err)
			require.Empty(t, excluded.NetworkRoots)
			require.Equal(t, p.Owners, excluded.Owners)
		}

		require.NoError(t, cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), f.acl, &api.NetworkACLPut{Ingress: []api.NetworkACLRule{{Source: "missing-subject"}}}))
		p, err := project.ReadNetworkACLProtection(ctx, tx, id, project.ACLCurrentExclusion{})
		require.Error(t, err)
		require.Nil(t, p)
		return nil
	})
}

func TestNetworkACLProtectionRetainedTypeMismatchFailsClosed(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "default")
	_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
	require.NoError(t, err)
	rollback := errors.New("rollback fixture-only corruption")
	err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.GetProjectID(ctx, tx.Tx(), "default")
		require.NoError(t, err)
		// No retained rows are fabricated; this corruption-only test changes the live catalog type.
		_, err = tx.Tx().ExecContext(ctx, `UPDATE networks SET type=? WHERE id=?`, db.NetworkTypeOVN, f.oldNetwork)
		require.NoError(t, err)
		p, err := project.ReadNetworkACLProtection(ctx, tx, id, project.ACLCurrentExclusion{})
		require.ErrorContains(t, err, "type changed")
		require.Nil(t, p)
		return rollback
	})
	require.ErrorIs(t, err, rollback)
}
