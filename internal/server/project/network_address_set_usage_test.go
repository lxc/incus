//go:build linux && cgo && !agent

package project_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/shared/api"
)

func readAddressSetOwnerProjection(t *testing.T, ctx context.Context, tx *db.ClusterTx, id int64) (*project.NetworkACLProtection, error) {
	t.Helper()
	p, err := project.ReadNetworkAddressSetProtection(ctx, tx, id)
	require.NoError(t, err)
	require.Len(t, p.Sets, 1)
	require.Len(t, p.Edges, 1)
	for key, set := range p.Sets {
		require.Equal(t, id, key.ProjectID)
		require.Equal(t, int64(set.ID), key.AddressSetID)
		require.Equal(t, p.ACLProtection.ProjectName+"-same", set.Name)
		require.Equal(t, key, p.Edges[0].Target)
	}

	require.Empty(t, p.Protected, "bridge evidence cannot protect an OVN address-set object")
	return p.ACLProtection, nil
}

func TestNetworkAddressSetProtectionProjectDevicesAndWholeOverrides(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "profile-order", true: "local-device"}[local], func(t *testing.T) {
			f := newProfileReferenceFixture(t, 1, "shared")
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				for _, name := range []string{"default", "tenant"} {
					_, err := cluster.CreateNetworkAddressSet(ctx, tx.Tx(), cluster.NetworkAddressSet{Project: name, Name: name + "-same"})
					require.NoError(t, err)
					acl, err := cluster.GetNetworkACLID(ctx, tx.Tx(), name, "old-acl")
					require.NoError(t, err)
					require.NoError(t, cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), acl, &api.NetworkACLPut{Ingress: []api.NetworkACLRule{{Source: "$" + name + "-same"}}}))
				}

				resourceID, err := cluster.GetProjectID(ctx, tx.Tx(), "default")
				require.NoError(t, err)
				var node string
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
				id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "consumer-0", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
				require.NoError(t, err)
				require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(id), "default", []string{"tracked"}))
				before, err := readAddressSetOwnerProjection(t, ctx, tx, resourceID)
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
				after, err := readAddressSetOwnerProjection(t, ctx, tx, resourceID)
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
				// Same-named tenant resources do not receive the shared-default owners.
				tenantID, err := cluster.GetProjectID(ctx, tx.Tx(), "tenant")
				require.NoError(t, err)
				tenant, err := readAddressSetOwnerProjection(t, ctx, tx, tenantID)
				require.NoError(t, err)
				require.Empty(t, tenant.Owners.CurrentInstances)
				return nil
			})
		})
	}
}

func TestNetworkAddressSetProtectionRuleProvenanceAndDirectRoots(t *testing.T) {
	c, cleanup := db.NewTestCluster(t)
	defer cleanup()
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		pid, err := cluster.GetProjectID(ctx, tx.Tx(), "default")
		require.NoError(t, err)
		setID, err := cluster.CreateNetworkAddressSet(ctx, tx.Tx(), cluster.NetworkAddressSet{Project: "default", Name: "same"})
		require.NoError(t, err)
		a, err := cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "a"})
		require.NoError(t, err)
		b, err := cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "b"})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), a, &api.NetworkACLPut{Ingress: []api.NetworkACLRule{{Source: "b"}}}))
		require.NoError(t, cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), b, &api.NetworkACLPut{
			Ingress: []api.NetworkACLRule{{Source: "a, $same, $same,@peer,#internal,192.0.2.1", Destination: "$same", State: "disabled"}},
			Egress:  []api.NetworkACLRule{{Source: "$same", Destination: "$same,2001:db8::1", State: "logged"}},
		}))
		netID, err := tx.CreateNetwork(ctx, "default", "root", "", db.NetworkTypeOVN, map[string]string{"security.acls": "a"})
		require.NoError(t, err)
		key := project.AddressSetUsageKey{ProjectID: pid, AddressSetID: setID}
		edges := []project.ACLAddressSetEdge{
			{Source: project.ACLUsageKey{ProjectID: pid, ACLID: b}, Target: key, Direction: "ingress", Field: "Source", RuleIndex: 0, State: "disabled"},
			{Source: project.ACLUsageKey{ProjectID: pid, ACLID: b}, Target: key, Direction: "ingress", Field: "Source", RuleIndex: 0, State: "disabled"},
			{Source: project.ACLUsageKey{ProjectID: pid, ACLID: b}, Target: key, Direction: "ingress", Field: "Destination", RuleIndex: 0, State: "disabled"},
			{Source: project.ACLUsageKey{ProjectID: pid, ACLID: b}, Target: key, Direction: "egress", Field: "Source", RuleIndex: 0, State: "logged"},
			{Source: project.ACLUsageKey{ProjectID: pid, ACLID: b}, Target: key, Direction: "egress", Field: "Destination", RuleIndex: 0, State: "logged"},
		}

		p, err := project.ReadNetworkAddressSetProtection(ctx, tx, pid)
		require.NoError(t, err)
		require.Equal(t, edges, p.Edges, "all four fields and duplicate occurrences preserve exact provenance")
		require.Empty(t, p.Protected, "subject-only B and its cycle do not activate set edges")
		require.NoError(t, tx.UpdateNetwork(ctx, "default", "root", "", map[string]string{"security.acls": "a,b"}))
		for _, state := range []int{0, 1, 2, 3, 4} {
			_, err = tx.Tx().ExecContext(ctx, `UPDATE networks SET state=? WHERE id=?`, state, netID)
			require.NoError(t, err)
			p, err = project.ReadNetworkAddressSetProtection(ctx, tx, pid)
			require.NoError(t, err)
			require.Equal(t, edges, p.Protected, "direct persisted roots count in every network lifecycle state")
		}

		return nil
	}))
}

func TestNetworkAddressSetProtectionRetainedProvenance(t *testing.T) {
	f := newProfileReferenceFixture(t, 2, "shared")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := cluster.CreateNetworkAddressSet(ctx, tx.Tx(), cluster.NetworkAddressSet{Project: "default", Name: "oldset"})
		require.NoError(t, err)
		return cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), f.acl, &api.NetworkACLPut{Ingress: []api.NetworkACLRule{{Source: "$oldset"}}})
	})
	commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
	require.NoError(t, err)
	identity := f.identity(t, commit.Token, f.instances[0])
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
		pid, err := cluster.GetProjectID(ctx, tx.Tx(), "default")
		require.NoError(t, err)
		expected, err := project.ReadNetworkACLProtection(ctx, tx, pid, project.ACLCurrentExclusion{})
		require.NoError(t, err)
		p, err := project.ReadNetworkAddressSetProtection(ctx, tx, pid)
		require.NoError(t, err)
		require.Equal(t, expected, p.ACLProtection, "set projection preserves every checked owner, consumer, role and attempt")
		require.NotEmpty(t, p.ACLProtection.Owners.Retained)
		roles := map[string]bool{}
		consumers := map[int64]bool{}
		attempt := false
		for _, row := range p.ACLProtection.Owners.Retained {
			require.NotEqual(t, row.ProjectID, row.NetworkProjectID)
			require.Equal(t, "eth0", row.Device)
			roles[row.Role] = true
			if row.ConsumerID > 0 {
				consumers[row.ConsumerID] = true
			}

			attempt = attempt || row.AttemptToken == identity.Token
		}

		require.True(t, attempt)
		require.GreaterOrEqual(t, len(roles), 2)
		require.Len(t, consumers, 2)
		require.Empty(t, p.Protected, "retained bridge owners are evidence, not OVN roots")
		return nil
	})
}

func TestNetworkAddressSetProtectionWrongProjectAndErrors(t *testing.T) {
	c, cleanup := db.NewTestCluster(t)
	defer cleanup()
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		pid, err := cluster.GetProjectID(ctx, tx.Tx(), "default")
		require.NoError(t, err)
		_, err = cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "foreign"})
		require.NoError(t, err)
		_, err = cluster.CreateNetworkAddressSet(ctx, tx.Tx(), cluster.NetworkAddressSet{Project: "foreign", Name: "foreign-only"})
		require.NoError(t, err)
		a, err := cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "a"})
		require.NoError(t, err)
		for _, field := range []string{"Source", "Destination"} {
			rule := api.NetworkACLRule{Source: "$foreign-only"}
			if field == "Destination" {
				rule = api.NetworkACLRule{Destination: "$foreign-only"}
			}

			require.NoError(t, cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), a, &api.NetworkACLPut{Ingress: []api.NetworkACLRule{rule}}))
			p, err := project.ReadNetworkAddressSetProtection(ctx, tx, pid)
			require.ErrorContains(t, err, "Unknown address set rule reference")
			require.Nil(t, p, "same-name foreign resource cannot repair a missing dependency")
		}

		require.NoError(t, cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), a, &api.NetworkACLPut{}))
		for _, id := range []int64{0, -1, pid + 999} {
			p, err := project.ReadNetworkAddressSetProtection(ctx, tx, id)
			require.Error(t, err)
			require.Nil(t, p)
		}

		return nil
	}))
}
