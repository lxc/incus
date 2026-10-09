//go:build linux && cgo && !agent

package addressset

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/shared/api"
)

type addressSetGCFixture struct {
	c         *db.Cluster
	projectID int64
	tenantID  int64
	profileID int64
	instances []int64
	networks  map[string]int64
	acls      map[string]int64
	sets      map[string]int64
}

func newAddressSetGCFixture(t *testing.T, consumers bool) *addressSetGCFixture {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	f := &addressSetGCFixture{c: c, networks: map[string]int64{}, acls: map[string]int64{}, sets: map[string]int64{}}
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		f.projectID, err = cluster.GetProjectID(ctx, tx.Tx(), "default")
		require.NoError(t, err)
		f.tenantID, err = cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "tenant"})
		require.NoError(t, err)
		require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), f.tenantID, map[string]string{"features.profiles": "false", "features.networks": "true", "restricted": "true", "restricted.networks.access": "old,new,steady,bridge"}))
		for _, name := range []string{"old", "new", "steady", "bridge"} {
			kind := db.NetworkTypeOVN
			if name == "bridge" {
				kind = db.NetworkTypeBridge
			}

			f.networks[name], err = tx.CreateNetwork(ctx, "default", name, "", kind, nil)
			require.NoError(t, err)
		}

		f.networks["tenant"], err = tx.CreateNetwork(ctx, "tenant", "old", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		for _, name := range []string{"a", "b", "c", "steady", "cycle-x", "cycle-y", "self"} {
			f.acls[name], err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: name})
			require.NoError(t, err)
		}

		f.acls["tenant"], err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "tenant", Name: "a"})
		require.NoError(t, err)
		for _, name := range []string{"oldset", "newset", "steadyset", "unused"} {
			f.sets[name], err = cluster.CreateNetworkAddressSet(ctx, tx.Tx(), cluster.NetworkAddressSet{Project: "default", Name: name})
			require.NoError(t, err)
		}

		f.sets["tenant"], err = cluster.CreateNetworkAddressSet(ctx, tx.Tx(), cluster.NetworkAddressSet{Project: "tenant", Name: "tenant-oldset"})
		require.NoError(t, err)
		for acl, set := range map[string]string{"a": "oldset", "b": "newset", "steady": "steadyset", "c": "unused"} {
			require.NoError(t, cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), f.acls[acl], &api.NetworkACLPut{Ingress: []api.NetworkACLRule{{Source: "$" + set}}}))
		}

		if !consumers {
			return nil
		}

		f.profileID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "tracked"})
		require.NoError(t, err)
		devices, err := cluster.APIToDevices(f.devices("old", "a"))
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), f.profileID, devices))
		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		for _, name := range []string{"default", "tenant"} {
			id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: name, Name: "consumer", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
			require.NoError(t, err)
			require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(id), name, []string{"tracked"}))
			f.instances = append(f.instances, id)
			// The normal fixture establishes an explicitly successful creation baseline.
			snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, id)
			require.NoError(t, err)
			require.NoError(t, tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1))
		}

		return nil
	})
	return f
}

func (f *addressSetGCFixture) tx(t *testing.T, fn func(context.Context, *db.ClusterTx) error) {
	t.Helper()
	require.NoError(t, f.c.Transaction(context.Background(), fn))
}

func (f *addressSetGCFixture) devices(network string, acl string) map[string]map[string]string {
	devices := map[string]map[string]string{"unchanged": {"type": "nic", "network": "steady", "security.acls": "steady"}}
	if network != "" {
		devices["eth0"] = map[string]string{"type": "nic", "network": network, "security.acls": acl}
	}

	return devices
}

func (f *addressSetGCFixture) identity(t *testing.T, token string, id int64) db.ProfileReferenceApply {
	t.Helper()
	var result db.ProfileReferenceApply
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		s, err := tx.ProfileReferenceState(ctx, id)
		require.NoError(t, err)
		result = db.ProfileReferenceApply{Token: uuid.NewString(), ChangeToken: token, InstanceID: id, ProjectID: s.ProjectID, MemberID: s.MemberID, PlacementRevision: s.PlacementRevision, Sequence: s.DesiredSequence, Owner: "addressset-collector-unit"}
		return nil
	})
	return result
}

func (f *addressSetGCFixture) claim(t *testing.T, identity db.ProfileReferenceApply, target string) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
		// Full baseline+target union includes the unchanged OVN NIC, with real DB reservations.
		names := []string{"old", "steady"}
		if target != "" {
			names = append(names, "new")
		}

		children := []db.ProfileReferenceChild{}
		for _, name := range names {
			child := db.ProfileReferenceChild{NetworkID: f.networks[name], ProjectID: f.projectID, Name: name, Token: uuid.NewString()}
			require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, "default", name, child.Token, "nic"))
			children = append(children, child)
		}

		require.NoError(t, tx.SealProfileReferenceChildren(ctx, identity, children))
		var count int
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM profiles_reference_children WHERE attempt_token=?`, identity.Token).Scan(&count))
		require.Equal(t, len(names), count)
		return nil
	})
}

func (f *addressSetGCFixture) commit(t *testing.T, target string, dispatch func(context.Context, db.ProfileReferenceConsumer) error) *project.ProfileReferenceCommit {
	t.Helper()
	network := ""
	if target != "" {
		network = "new"
	}

	commit, err := project.CommitProfileReferenceUpdate(context.Background(), f.c, project.ProfileReferenceUpdate{Project: "default", Name: "tracked", ProfileID: f.profileID, Profile: api.ProfilePut{Devices: f.devices(network, target)}}, dispatch)
	require.NoError(t, err)
	return commit
}

func (f *addressSetGCFixture) selectPlan(t *testing.T, request addressSetGCRequest) []addressSetGCObject {
	t.Helper()
	plan, err := selectUnusedAddressSets(context.Background(), f.c, "default", request)
	require.NoError(t, err)
	require.NotNil(t, plan)
	ids := []int64{}
	for _, obj := range plan {
		require.Equal(t, f.projectID, obj.Key.ProjectID)
		require.Equal(t, f.sets[obj.Name], obj.Key.AddressSetID)
		ids = append(ids, obj.Key.AddressSetID)
	}

	require.True(t, slices.IsSorted(ids))
	require.Equal(t, len(ids), len(slices.Compact(slices.Clone(ids))))
	return plan
}

func (f *addressSetGCFixture) assertPlans(t *testing.T, unused ...string) {
	t.Helper()
	want := []addressSetGCObject{}
	for _, name := range unused {
		want = append(want, addressSetGCObject{Key: project.AddressSetUsageKey{ProjectID: f.projectID, AddressSetID: f.sets[name]}, Name: name})
	}

	require.ElementsMatch(t, want, f.selectPlan(t, addressSetGCRequest{Mode: addressSetGCAll}), "exact retained-set protection in all-project mode")
	require.ElementsMatch(t, want, f.selectPlan(t, addressSetGCRequest{Mode: addressSetGCACLs, ACLNames: []string{"a", "b", "c", "steady", "a"}}), "ViaACLs filters never exclude owners")
	for _, name := range []string{"oldset", "newset", "steadyset", "unused"} {
		plan := f.selectPlan(t, addressSetGCRequest{Mode: addressSetGCOne, SetName: name})
		if slices.Contains(unused, name) {
			require.Equal(t, []addressSetGCObject{{Key: project.AddressSetUsageKey{ProjectID: f.projectID, AddressSetID: f.sets[name]}, Name: name}}, plan)
		} else {
			require.Empty(t, plan, "exact retained-set assertion: %s must stay protected", name)
		}
	}
}

func TestAddressSetGCPostCommitRetentionAndIndependentRelease(t *testing.T) {
	for _, target := range []string{"", "b"} {
		t.Run("target-"+target, func(t *testing.T) {
			f := newAddressSetGCFixture(t, true)
			want := []string{"unused"}
			if target == "" {
				want = append(want, "newset")
			}

			calls := 0
			commit := f.commit(t, target, func(ctx context.Context, consumer db.ProfileReferenceConsumer) error {
				calls++
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					_, stored, err := tx.GetProfile(ctx, "default", "tracked")
					require.NoError(t, err)
					require.Equal(t, target, stored.Devices["eth0"]["security.acls"])
					return nil
				})
				f.assertPlans(t, want...)
				return nil
			})
			require.Equal(t, 2, calls)
			for index, id := range f.instances {
				identity := f.identity(t, commit.Token, id)
				f.claim(t, identity, target)
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.MarkProfileReferenceApplied(ctx, identity)
				})
				f.assertPlans(t, want...) // Marking alone does not release retained protection.
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.FinalizeProfileReferenceApply(ctx, identity)
				})
				if index == 0 {
					f.assertPlans(t, want...)
					f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
						return tx.FinalizeProfileReferenceApply(ctx, identity)
					})
					f.assertPlans(t, want...) // Duplicate completion cannot release sibling.
				}
			}
			want = append(want, "oldset")
			f.assertPlans(t, want...)
			plan, err := selectUnusedAddressSets(context.Background(), f.c, "tenant", addressSetGCRequest{Mode: addressSetGCAll})
			require.NoError(t, err)
			require.Equal(t, []addressSetGCObject{{Key: project.AddressSetUsageKey{ProjectID: f.tenantID, AddressSetID: f.sets["tenant"]}, Name: "tenant-oldset"}}, plan)
		})
	}
}

func TestAddressSetGCFailedAndStaleCompletionRetainsProtection(t *testing.T) {
	for _, fault := range []string{"unknown", "rolled-back", "before-dispatch", "token", "member", "placement", "sequence", "rollback"} {
		t.Run(fault, func(t *testing.T) {
			f := newAddressSetGCFixture(t, true)
			commit := f.commit(t, "b", nil)
			// Release the first consumer so the exact failing second owner is discriminating.
			first := f.identity(t, commit.Token, f.instances[0])
			f.claim(t, first, "b")
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				require.NoError(t, tx.MarkProfileReferenceApplied(ctx, first))
				return tx.FinalizeProfileReferenceApply(ctx, first)
			})
			second := f.identity(t, commit.Token, f.instances[1])
			f.claim(t, second, "b")
			if fault == "unknown" || fault == "rolled-back" || fault == "before-dispatch" {
				outcome := db.ProfileReferenceUnknown
				if fault == "rolled-back" {
					outcome = db.ProfileReferenceRolledBack
				}

				if fault == "before-dispatch" {
					outcome = db.ProfileReferenceBeforeDispatch
				}

				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.FailProfileReferenceApply(ctx, second, outcome)
				})
			} else {
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error { return tx.MarkProfileReferenceApplied(ctx, second) })
				wrong := second
				switch fault {
				case "token":
					wrong.Token = uuid.NewString()
				case "member":
					wrong.MemberID++
				case "placement":
					wrong.PlacementRevision++
				case "sequence":
					wrong.Sequence++
				}

				err := f.c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					err := tx.FinalizeProfileReferenceApply(ctx, wrong)
					if fault == "rollback" {
						require.NoError(t, err)
						return errors.New("fixture rollback after successful finalization")
					}

					return err
				})
				require.Error(t, err)
			}

			f.assertPlans(t, "unused")
			if fault != "unknown" {
				if fault == "rolled-back" || fault == "before-dispatch" {
					second = f.identity(t, commit.Token, f.instances[1])
					f.claim(t, second, "b")
					f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error { return tx.MarkProfileReferenceApplied(ctx, second) })
				}

				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.FinalizeProfileReferenceApply(ctx, second)
				})
				f.assertPlans(t, "unused", "oldset")
			}
		})
	}
}

func (f *addressSetGCFixture) rules(t *testing.T, name string, ingress []api.NetworkACLRule, egress []api.NetworkACLRule) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), f.acls[name], &api.NetworkACLPut{Ingress: ingress, Egress: egress})
	})
}

func (f *addressSetGCFixture) networkACLs(t *testing.T, name string, value string) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateNetwork(ctx, "default", name, "", map[string]string{"security.acls": value})
	})
}

func TestAddressSetGCSharedCurrentAndRetainedOwners(t *testing.T) {
	f := newAddressSetGCFixture(t, true)
	// The requested ACL is only a filter; another current ACL protects the same exact set.
	f.rules(t, "c", nil, []api.NetworkACLRule{{Destination: "$oldset,$unused", State: "disabled"}})
	f.networkACLs(t, "new", "c")
	commit := f.commit(t, "b", nil)
	for _, id := range f.instances {
		identity := f.identity(t, commit.Token, id)
		f.claim(t, identity, "b")
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
			return tx.FinalizeProfileReferenceApply(ctx, identity)
		})
	}

	require.Empty(t, f.selectPlan(t, addressSetGCRequest{Mode: addressSetGCACLs, ACLNames: []string{"a", "c", "a"}}))
	f.networkACLs(t, "new", "")
	plan := f.selectPlan(t, addressSetGCRequest{Mode: addressSetGCACLs, ACLNames: []string{"a", "c", "a"}})
	require.Len(t, plan, 2)
	require.ElementsMatch(t, []string{"oldset", "unused"}, []string{plan[0].Name, plan[1].Name})
}

func TestAddressSetGCDirectSubjectAndBridgeRoots(t *testing.T) {
	f := newAddressSetGCFixture(t, false)
	f.rules(t, "a", []api.NetworkACLRule{{Source: "b,$oldset", Destination: "$oldset", State: "disabled"}}, nil)
	f.rules(t, "b", nil, []api.NetworkACLRule{{Source: "$newset", Destination: "a,$newset", State: "logged"}})
	f.rules(t, "self", []api.NetworkACLRule{{Source: "self,$unused"}}, nil)
	for _, tc := range []struct {
		name, direct string
		unused       []string
	}{
		{"unrooted", "", []string{"oldset", "newset", "steadyset", "unused"}},
		{"subject-only", "a", []string{"newset", "steadyset", "unused"}},
		{"independent-subject", "a,b", []string{"steadyset", "unused"}},
		{"reverse-cycle", "b", []string{"oldset", "steadyset", "unused"}},
		{"self", "self", []string{"oldset", "newset", "steadyset"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.networkACLs(t, "old", tc.direct)
			f.assertPlans(t, tc.unused...)
		})
	}

	f.networkACLs(t, "old", "")
	f.networkACLs(t, "bridge", "a")
	var profileID int64
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		profileID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "standalone"})
		require.NoError(t, err)
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "bridge", "security.acls": "a"}})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), profileID, devices))
		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "bridge-consumer", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
		require.NoError(t, err)
		return cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(id), "default", []string{"standalone"})
	})
	f.assertPlans(t, "oldset", "newset", "steadyset", "unused")
	// A separate standalone OVN profile is itself a direct root, without an instance.
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "ovn-only"})
		require.NoError(t, err)
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "a"}})
		require.NoError(t, err)
		return cluster.UpdateProfileDevices(ctx, tx.Tx(), id, devices)
	})
	f.assertPlans(t, "newset", "steadyset", "unused")
}

func TestAddressSetGCInvalidModesNamesAndWholePlanFailure(t *testing.T) {
	f := newAddressSetGCFixture(t, false)
	for _, request := range []addressSetGCRequest{
		{},
		{Mode: "unknown"},
		{Mode: addressSetGCOne},
		{Mode: addressSetGCOne, SetName: "unused", ACLNames: []string{}},
		{Mode: addressSetGCACLs, SetName: "unused"},
		{Mode: addressSetGCAll, SetName: "unused"},
		{Mode: addressSetGCAll, ACLNames: []string{}},
		{Mode: addressSetGCOne, SetName: "missing"},
		{Mode: addressSetGCACLs, ACLNames: []string{""}},
		{Mode: addressSetGCACLs, ACLNames: []string{"c", "missing"}},
		{Mode: addressSetGCACLs, ACLNames: []string{"missing", "c"}},
	} {
		plan, err := selectUnusedAddressSets(context.Background(), f.c, "default", request)
		require.Error(t, err)
		require.Nil(t, plan)
	}

	for _, name := range []string{"", "missing"} {
		plan, err := selectUnusedAddressSets(context.Background(), f.c, name, addressSetGCRequest{Mode: addressSetGCACLs})
		require.Error(t, err)
		require.Nil(t, plan)
	}
	// A valid candidate before/after a broken set dependency cannot escape a partial plan.
	for _, bad := range []string{"$missing", "$", "$ oldset", "$$oldset"} {
		f.rules(t, "b", nil, []api.NetworkACLRule{{Source: bad}})
		for _, names := range [][]string{{"c", "b"}, {"b", "c"}, nil} {
			plan, err := selectUnusedAddressSets(context.Background(), f.c, "default", addressSetGCRequest{Mode: addressSetGCACLs, ACLNames: names})
			require.Error(t, err)
			require.Nil(t, plan)
		}
	}
	f.rules(t, "b", nil, nil)
	// Once deleted, an explicitly requested known name is stale; there is no backend orphan lookup.
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.DeleteNetworkACL(ctx, tx.Tx(), int(f.acls["b"]))
	})
	for _, names := range [][]string{{"c", "b"}, {"b", "c"}} {
		plan, err := selectUnusedAddressSets(context.Background(), f.c, "default", addressSetGCRequest{Mode: addressSetGCACLs, ACLNames: names})
		require.Error(t, err)
		require.Nil(t, plan)
	}

	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.DeleteNetworkAddressSet(ctx, tx.Tx(), "default", "newset")
	})
	plan, err := selectUnusedAddressSets(context.Background(), f.c, "default", addressSetGCRequest{Mode: addressSetGCOne, SetName: "newset"})
	require.Error(t, err)
	require.Nil(t, plan)
}

func TestAddressSetGCEmptyContextAndMapperErrors(t *testing.T) {
	f := newAddressSetGCFixture(t, false)
	require.Empty(t, f.selectPlan(t, addressSetGCRequest{Mode: addressSetGCACLs}))
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "empty"})
		return err
	})
	for _, request := range []addressSetGCRequest{{Mode: addressSetGCACLs}, {Mode: addressSetGCAll}} {
		plan, err := selectUnusedAddressSets(context.Background(), f.c, "empty", request)
		require.NoError(t, err)
		require.NotNil(t, plan)
		require.Empty(t, plan)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		plan, err = selectUnusedAddressSets(ctx, f.c, "empty", request)
		require.Error(t, err)
		require.Nil(t, plan)
	}

	plan, err := selectUnusedAddressSets(nil, f.c, "default", addressSetGCRequest{Mode: addressSetGCAll}) //nolint:staticcheck // SA1012: Intentional nil-context rejection input.
	require.Error(t, err)
	require.Nil(t, plan)
	plan, err = selectUnusedAddressSets(context.Background(), nil, "default", addressSetGCRequest{Mode: addressSetGCAll})
	require.Error(t, err)
	require.Nil(t, plan)
	// Real mapper error in the isolated database, even with an empty ACL candidate list.
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, `UPDATE networks_address_sets SET addresses='invalid-json' WHERE id=?`, f.sets["unused"])
		return err
	})
	for _, request := range []addressSetGCRequest{{Mode: addressSetGCAll}, {Mode: addressSetGCACLs}, {Mode: addressSetGCOne, SetName: "oldset"}} {
		plan, err = selectUnusedAddressSets(context.Background(), f.c, "default", request)
		require.Error(t, err)
		require.Nil(t, plan)
	}

	require.NoError(t, f.c.Close())
	plan, err = selectUnusedAddressSets(context.Background(), f.c, "default", addressSetGCRequest{Mode: addressSetGCACLs})
	require.Error(t, err)
	require.Nil(t, plan)
}

func TestAddressSetGCGlobalNameConstraintAndWrongProjectLookup(t *testing.T) {
	f := newAddressSetGCFixture(t, false)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		// The existing schema's global UNIQUE(name) prevents a simultaneous same-name fixture.
		_, err := cluster.CreateNetworkAddressSet(ctx, tx.Tx(), cluster.NetworkAddressSet{Project: "tenant", Name: "oldset"})
		require.Error(t, err)
		return nil
	})
	plan, err := selectUnusedAddressSets(context.Background(), f.c, "tenant", addressSetGCRequest{Mode: addressSetGCOne, SetName: "oldset"})
	require.Error(t, err)
	require.Nil(t, plan, "an existing default-project name is not a tenant identity")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), f.acls["tenant"], &api.NetworkACLPut{Ingress: []api.NetworkACLRule{{Source: "$oldset"}}})
	})
	for _, request := range []addressSetGCRequest{{Mode: addressSetGCAll}, {Mode: addressSetGCACLs}, {Mode: addressSetGCACLs, ACLNames: []string{"a"}}} {
		plan, err = selectUnusedAddressSets(context.Background(), f.c, "tenant", request)
		require.ErrorContains(t, err, "Unknown address set rule reference")
		require.Nil(t, plan)
	}
}
