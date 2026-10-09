//go:build linux && cgo && !agent

package acl

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/shared/api"
)

type aclGCFixture struct {
	c         *db.Cluster
	projectID int64
	tenantID  int64
	profileID int64
	instances []int64
	networks  map[string]int64
	acls      map[string]int64
}

func newACLGCFixture(t *testing.T, consumers bool) *aclGCFixture {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	f := &aclGCFixture{c: c, networks: map[string]int64{}, acls: map[string]int64{}}
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

func (f *aclGCFixture) tx(t *testing.T, fn func(context.Context, *db.ClusterTx) error) {
	t.Helper()
	require.NoError(t, f.c.Transaction(context.Background(), fn))
}

func (f *aclGCFixture) devices(network string, acl string) map[string]map[string]string {
	devices := map[string]map[string]string{"unchanged": {"type": "nic", "network": "steady", "security.acls": "steady"}}
	if network != "" {
		devices["eth0"] = map[string]string{"type": "nic", "network": network, "security.acls": acl}
	}

	return devices
}

func (f *aclGCFixture) groups(acl string, network string) []ovn.OVNPortGroup {
	groups := append([]ovn.OVNPortGroup{}, OVNACLDirectionalPortGroups(f.acls[acl]).PortGroups()...)
	return append(groups, OVNACLNetworkPortGroupName(f.acls[acl], f.networks[network]))
}

func (f *aclGCFixture) selectPlan(t *testing.T, ignore any, device string, keep []string, inventory []ovn.OVNPortGroup) []ovn.OVNPortGroup {
	t.Helper()
	plan, err := selectUnusedACLPortGroups(context.Background(), f.c, f.projectID, "default", ignore, device, keep, inventory)
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.True(t, slices.IsSorted(plan))
	require.Equal(t, len(plan), len(slices.Compact(slices.Clone(plan))))
	return plan
}

func (f *aclGCFixture) identity(t *testing.T, token string, id int64) db.ProfileReferenceApply {
	t.Helper()
	var result db.ProfileReferenceApply
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		s, err := tx.ProfileReferenceState(ctx, id)
		require.NoError(t, err)
		result = db.ProfileReferenceApply{Token: uuid.NewString(), ChangeToken: token, InstanceID: id, ProjectID: s.ProjectID, MemberID: s.MemberID, PlacementRevision: s.PlacementRevision, Sequence: s.DesiredSequence, Owner: "acl-collector-unit"}
		return nil
	})
	return result
}

func (f *aclGCFixture) claim(t *testing.T, identity db.ProfileReferenceApply, target string) {
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

func (f *aclGCFixture) commit(t *testing.T, target string, dispatch func(context.Context, db.ProfileReferenceConsumer) error) *project.ProfileReferenceCommit {
	t.Helper()
	network := ""
	if target != "" {
		network = "new"
	}

	commit, err := project.CommitProfileReferenceUpdate(context.Background(), f.c, project.ProfileReferenceUpdate{Project: "default", Name: "tracked", ProfileID: f.profileID, Profile: api.ProfilePut{Devices: f.devices(network, target)}}, dispatch)
	require.NoError(t, err)
	return commit
}

// aclGCInstance supplies the actual legacy interface shape; only stable identity methods are needed.
type aclGCInstance struct {
	instance.Instance
	id          int
	projectName string
}

func (i *aclGCInstance) ID() int              { return i.id }
func (i *aclGCInstance) Project() api.Project { return api.Project{Name: i.projectName} }

func TestACLGCPostCommitRetentionAndIndependentRelease(t *testing.T) {
	for _, target := range []string{"", "b"} {
		t.Run("target-"+target, func(t *testing.T) {
			f := newACLGCFixture(t, true)
			old := f.groups("a", "old")
			inventory := append(slices.Clone(old), f.groups("steady", "steady")...)
			inventory = append(inventory, f.groups("b", "new")...)
			want := []ovn.OVNPortGroup{}
			if target == "" {
				want = f.groups("b", "new")
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
				plan := f.selectPlan(t, nil, "", nil, inventory)
				require.ElementsMatch(t, want, plan, "postcommit old-only ACL directional/network groups must remain protected")
				return nil
			})
			require.Equal(t, 2, calls)
			for index, id := range f.instances {
				identity := f.identity(t, commit.Token, id)
				f.claim(t, identity, target)
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.MarkProfileReferenceApplied(ctx, identity)
				})
				name := "default"
				if index == 1 {
					name = "tenant"
				}

				ignore := &aclGCInstance{id: int(id), projectName: name}
				require.ElementsMatch(t, want, f.selectPlan(t, ignore, "eth0", nil, inventory), "marked but not finalized retains same-owner protection")
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.FinalizeProfileReferenceApply(ctx, identity)
				})
				if index == 0 {
					require.ElementsMatch(t, want, f.selectPlan(t, nil, "", nil, inventory), "second independent consumer still protects old objects")
					f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
						return tx.FinalizeProfileReferenceApply(ctx, identity)
					})
					require.ElementsMatch(t, want, f.selectPlan(t, nil, "", nil, inventory), "duplicate completion cannot release sibling")
				}
			}
			want = append(want, old...)
			require.ElementsMatch(t, want, f.selectPlan(t, nil, "", nil, inventory), "exact final release selects only now-unused objects")
			// Foreign same-named ACL/network identities never exchange protection after release.
			foreign := f.groups("tenant", "tenant")
			require.Empty(t, f.selectPlan(t, nil, "", nil, foreign))
		})
	}
}

func TestACLGCFailedAndStaleCompletionRetainsProtection(t *testing.T) {
	for _, fault := range []string{"unknown", "rolled-back", "before-dispatch", "token", "member", "placement", "sequence", "rollback"} {
		t.Run(fault, func(t *testing.T) {
			f := newACLGCFixture(t, true)
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

			require.Empty(t, f.selectPlan(t, &aclGCInstance{id: int(second.InstanceID), projectName: "tenant"}, "eth0", nil, f.groups("a", "old")))
			if fault != "unknown" {
				if fault == "rolled-back" || fault == "before-dispatch" {
					second = f.identity(t, commit.Token, f.instances[1])
					f.claim(t, second, "b")
					f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error { return tx.MarkProfileReferenceApplied(ctx, second) })
				}

				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.FinalizeProfileReferenceApply(ctx, second)
				})
				require.ElementsMatch(t, f.groups("a", "old"), f.selectPlan(t, nil, "", nil, f.groups("a", "old")))
			}
		})
	}
}

func (f *aclGCFixture) rules(t *testing.T, name string, ingress []api.NetworkACLRule, egress []api.NetworkACLRule) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), f.acls[name], &api.NetworkACLPut{Ingress: ingress, Egress: egress})
	})
}

func (f *aclGCFixture) networkACLs(t *testing.T, name string, value string) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateNetwork(ctx, "default", name, "", map[string]string{"security.acls": value})
	})
}

func TestACLGCDependencyAndKeepTruthTable(t *testing.T) {
	f := newACLGCFixture(t, false)
	f.rules(t, "a", []api.NetworkACLRule{{Source: "b,#internal,#external,@internal,@external,@peer,$set,10.0.0.1,10.0.0.0/24,10.0.0.1-10.0.0.3,2001:db8::1,192.0.2.7/24,2001:db8::7/64", Destination: "c", State: "disabled"}}, nil)
	f.rules(t, "b", nil, []api.NetworkACLRule{{Destination: "c", Source: "self", State: "logged"}})
	f.rules(t, "cycle-x", []api.NetworkACLRule{{Source: "cycle-y"}}, nil)
	f.rules(t, "cycle-y", []api.NetworkACLRule{{Source: "cycle-x"}}, nil)
	f.rules(t, "self", []api.NetworkACLRule{{Source: "self"}}, nil)
	inventory := []ovn.OVNPortGroup{}
	for _, name := range []string{"a", "b", "c", "cycle-x", "cycle-y", "self"} {
		inventory = append(inventory, f.groups(name, "old")...)
	}

	for _, tc := range []struct {
		name        string
		direct      string
		keep        []string
		directional []string
		pairs       []string
	}{
		{"unrooted", "", nil, nil, nil},
		{"network-config", "a", nil, []string{"a", "b"}, []string{"a"}},
		{"keep", "", []string{"a", "a"}, []string{"a", "b"}, []string{"a"}},
		{"independent-subject", "a,b", nil, []string{"a", "b", "c"}, []string{"a", "b"}},
		{"rooted-cycle", "cycle-x", nil, []string{"cycle-x", "cycle-y"}, []string{"cycle-x"}},
		{"self", "self", nil, []string{"self"}, []string{"self"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.networkACLs(t, "old", tc.direct)
			protected := map[ovn.OVNPortGroup]bool{}
			for _, name := range tc.directional {
				for _, pg := range OVNACLDirectionalPortGroups(f.acls[name]).PortGroups() {
					protected[pg] = true
				}
			}
			for _, name := range tc.pairs {
				protected[OVNACLNetworkPortGroupName(f.acls[name], f.networks["old"])] = true
			}

			want := []ovn.OVNPortGroup{}
			for _, pg := range inventory {
				if !protected[pg] {
					want = append(want, pg)
				}
			}
			require.ElementsMatch(t, want, f.selectPlan(t, nil, "", tc.keep, inventory))
		})
	}
	// Keep preserves every valid per-network group for exactly A, without prefix collisions.
	f.networkACLs(t, "old", "")
	var collision int64
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		for i := 0; i < 20; i++ {
			id, err := cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: fmt.Sprintf("unused-%d", i)})
			require.NoError(t, err)
			if fmt.Sprint(id) == fmt.Sprint(f.acls["a"])+"0" {
				collision = id
				break
			}
		}
		return nil
	})
	require.Positive(t, collision)
	inventory = append(f.groups("a", "old"), OVNACLNetworkPortGroupName(f.acls["a"], f.networks["new"]))
	inventory = append(inventory, OVNACLDirectionalPortGroups(collision).PortGroups()...)
	inventory = append(inventory, OVNACLNetworkPortGroupName(f.acls["b"], f.networks["old"]))
	want := append(OVNACLDirectionalPortGroups(collision).PortGroups(), OVNACLNetworkPortGroupName(f.acls["b"], f.networks["old"]))
	require.ElementsMatch(t, want, f.selectPlan(t, nil, "", []string{"a"}, inventory))
}

func TestACLGCExactExclusionsAndLegacyNetwork(t *testing.T) {
	f := newACLGCFixture(t, false)
	f.networkACLs(t, "old", "a")
	groups := f.groups("a", "old")
	require.Empty(t, f.selectPlan(t, &api.Network{Name: "old"}, "", nil, groups))
	key := project.NetworkUsageKey{ProjectID: f.projectID, NetworkID: f.networks["old"]}
	require.ElementsMatch(t, groups, f.selectPlan(t, key, "", nil, groups))
	var id int64
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		id, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "standalone"})
		require.NoError(t, err)
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "a"}, "eth1": {"type": "nic", "network": "old", "security.acls": "b"}})
		require.NoError(t, err)
		return cluster.UpdateProfileDevices(ctx, tx.Tx(), id, devices)
	})
	require.Empty(t, f.selectPlan(t, key, "", nil, groups), "network exclusion must not hide standalone profile")
	f.networkACLs(t, "old", "")
	ignore := cluster.Profile{ID: int(id), Project: "default", Name: "standalone"}
	inventory := append(groups, f.groups("b", "old")...)
	require.ElementsMatch(t, groups, f.selectPlan(t, ignore, "eth0", nil, inventory), "other device remains protected")
	require.Empty(t, f.selectPlan(t, ignore, "removed-device", nil, inventory))
	// A legacy request for a deleted name cannot suppress a replacement's config root.
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "replacement"})
		require.NoError(t, err)
		oldID, err := tx.CreateNetwork(ctx, "default", "replacement", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		require.NoError(t, tx.DeleteNetwork(ctx, "default", "replacement"))
		// Keep an intervening row so even SQLite's row-ID reuse cannot conflate the identities.
		_, err = tx.CreateNetwork(ctx, "default", "intervening", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		newID, err := tx.CreateNetwork(ctx, "default", "replacement", "", db.NetworkTypeOVN, map[string]string{"security.acls": "c"})
		require.NoError(t, err)
		require.NotEqual(t, oldID, newID)
		return nil
	})
	require.Empty(t, f.selectPlan(t, &api.Network{Name: "replacement"}, "", nil, OVNACLDirectionalPortGroups(f.acls["c"]).PortGroups()))
}

func TestACLGCInvalidInputsAndWholePlanFailure(t *testing.T) {
	f := newACLGCFixture(t, true)
	good := OVNACLDirectionalPortGroups(f.acls["c"]).All
	for _, bad := range []ovn.OVNPortGroup{"incus_acl0_all", "incus_acl01_all", "incus_acl+1_all", "incus_acl-1_all", "incus_acl9223372036854775808_all", "incus_acl1_net0", "incus_acl1_net01", OVNACLNetworkPortGroupName(f.acls["c"], f.networks["bridge"])} {
		t.Run(string(bad), func(t *testing.T) {
			for _, inventory := range [][]ovn.OVNPortGroup{{good, bad}, {bad, good}} {
				plan, err := selectUnusedACLPortGroups(context.Background(), f.c, f.projectID, "default", nil, "", nil, inventory)
				require.Error(t, err)
				require.Nil(t, plan, "one malformed/stale object invalidates the entire mixed plan")
			}
		})
	}

	for _, tc := range []struct {
		name   string
		ignore any
		device string
	}{
		{"nil-device", nil, "eth0"},
		{"typed-nil-network", (*api.Network)(nil), ""},
		{"typed-nil-instance", (*aclGCInstance)(nil), "eth0"},
		{"unsupported", 3, ""},
		{"empty-network", &api.Network{}, ""},
		{"network-device", &api.Network{Name: "old"}, "eth0"},
		{"zero-instance", &aclGCInstance{projectName: "default"}, "eth0"},
		{"stale-instance", &aclGCInstance{id: 999999, projectName: "default"}, "eth0"},
		{"wrong-instance-project", &aclGCInstance{id: int(f.instances[0]), projectName: "tenant"}, "eth0"},
		{"empty-instance-device", &aclGCInstance{id: int(f.instances[0]), projectName: "default"}, ""},
		{"zero-profile", cluster.Profile{Project: "default"}, "eth0"},
		{"wrong-profile-project", cluster.Profile{ID: int(f.profileID), Project: "tenant"}, "eth0"},
		{"empty-profile-device", cluster.Profile{ID: int(f.profileID), Project: "default"}, ""},
		{"bad-network-key", project.NetworkUsageKey{ProjectID: f.projectID, NetworkID: 0}, ""},
		{"foreign-network-key", project.NetworkUsageKey{ProjectID: f.tenantID, NetworkID: f.networks["tenant"]}, ""},
		{"typed-network-device", project.NetworkUsageKey{ProjectID: f.projectID, NetworkID: f.networks["old"]}, "eth0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := selectUnusedACLPortGroups(context.Background(), f.c, f.projectID, "default", tc.ignore, tc.device, nil, nil)
			require.Error(t, err)
			require.Nil(t, plan)
		})
	}

	for _, tc := range []struct {
		id   int64
		name string
		keep []string
	}{{0, "default", nil}, {f.projectID + 999, "default", nil}, {f.projectID, "missing", nil}, {f.projectID, "default", []string{"missing"}}} {
		plan, err := selectUnusedACLPortGroups(context.Background(), f.c, tc.id, tc.name, nil, "", tc.keep, nil)
		require.Error(t, err)
		require.Nil(t, plan)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan, err := selectUnusedACLPortGroups(ctx, f.c, f.projectID, "default", nil, "", nil, []ovn.OVNPortGroup{good})
	require.Error(t, err)
	require.Nil(t, plan)
	// A real mapper failure in the isolated database must not escape a partial deletion plan.
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, `DROP TABLE networks_acls_config`)
		return err
	})
	plan, err = selectUnusedACLPortGroups(context.Background(), f.c, f.projectID, "default", nil, "", nil, []ovn.OVNPortGroup{good})
	require.Error(t, err)
	require.Nil(t, plan)
}

func TestACLGCForeignUnknownAndEmptyCatalog(t *testing.T) {
	f := newACLGCFixture(t, false)
	good := OVNACLDirectionalPortGroups(f.acls["c"]).All
	inventory := []ovn.OVNPortGroup{good, good, "incus_net1", "incus_acl1_future", "unrelated", OVNACLDirectionalPortGroups(f.acls["tenant"]).All, OVNACLNetworkPortGroupName(f.acls["c"], f.networks["tenant"])}
	require.Equal(t, []ovn.OVNPortGroup{good}, f.selectPlan(t, nil, "", nil, inventory))
	var emptyProject int64
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		emptyProject, err = cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "empty"})
		return err
	})
	plan, err := selectUnusedACLPortGroups(context.Background(), f.c, emptyProject, "empty", cluster.Profile{}, "eth0", nil, nil)
	require.Error(t, err)
	require.Nil(t, plan)
	plan, err = selectUnusedACLPortGroups(context.Background(), f.c, emptyProject+1, "empty", nil, "", nil, nil)
	require.Error(t, err)
	require.Nil(t, plan)
	plan, err = selectUnusedACLPortGroups(context.Background(), f.c, emptyProject, "empty", nil, "", nil, nil)
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.Empty(t, plan)
}

func TestACLGCBridgeOwnersDoNotCreateOVNRoots(t *testing.T) {
	f := newACLGCFixture(t, false)
	f.networkACLs(t, "bridge", "a")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		profileID, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "bridge-only"})
		require.NoError(t, err)
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "bridge", "security.acls": "a"}})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), profileID, devices))
		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "bridge-only", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(id), "default", []string{"bridge-only"}))
		return nil
	})
	groups := OVNACLDirectionalPortGroups(f.acls["a"]).PortGroups()
	require.ElementsMatch(t, groups, f.selectPlan(t, nil, "", nil, groups), "bridge config/profile/instance ownership is not an OVN root")
	// A direct root protects only its exact network pair; keep intentionally protects both pairs.
	f.networkACLs(t, "old", "a")
	old := OVNACLNetworkPortGroupName(f.acls["a"], f.networks["old"])
	other := OVNACLNetworkPortGroupName(f.acls["a"], f.networks["new"])
	require.Equal(t, []ovn.OVNPortGroup{other}, f.selectPlan(t, nil, "", nil, []ovn.OVNPortGroup{old, other}))
	require.Empty(t, f.selectPlan(t, nil, "", []string{"a"}, []ovn.OVNPortGroup{old, other}))
}

func TestNormalNetworkACLRetirementPlanReservationAndProtection(t *testing.T) {
	for _, control := range []string{"postcommit-unset", "child-parent", "current-root", "retained", "wrong-token", "wrong-kind", "parent-change"} {
		t.Run(control, func(t *testing.T) {
			f := newACLGCFixture(t, control == "retained")
			root, token := uuid.NewString(), uuid.NewString()
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				err := tx.BeginOVNReferenceActivation(ctx)
				if err != nil {
					return err
				}

				return tx.BindOVNReferenceRoot(ctx, root)
			})
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				kind := "update"
				if control == "wrong-kind" {
					kind = "nic"
				}

				err := tx.AcquireOVNNetworkOperation(ctx, "default", "old", token, kind)
				if err != nil {
					return err
				}

				if control == "child-parent" {
					return tx.UpdateNetwork(ctx, "default", "old", "", map[string]string{"parent": "new"})
				}

				if control == "current-root" {
					return tx.UpdateNetwork(ctx, "default", "old", "", map[string]string{"security.acls": "a"})
				}

				return nil
			})
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				selectedToken := token
				parent := int64(0)
				if control == "wrong-token" {
					selectedToken = uuid.NewString()
				}

				if control == "parent-change" || control == "child-parent" {
					parent = f.networks["new"]
				}

				plan, err := networkACLRetirementPlan(ctx, tx, root, "default", "old", f.networks["old"], parent, selectedToken, []string{"a"}, nil)
				if control == "wrong-token" || control == "wrong-kind" || control == "parent-change" {
					require.Error(t, err)
					return nil
				}

				require.NoError(t, err)
				require.Equal(t, root, plan.Root)
				require.Equal(t, token, plan.Token)
				if control == "postcommit-unset" || control == "child-parent" {
					require.Equal(t, []int64{f.acls["a"]}, plan.ACLIDs)
				} else {
					require.Empty(t, plan.ACLIDs)
				}

				// The same reservation protects the selection-to-effect interval at actual publication.
				err = project.ValidateDeviceNetworkReferences(ctx, tx, "default", deviceConfig.NewDevices(f.devices("old", "a")))
				require.Error(t, err)
				require.Error(t, tx.AcquireOVNPeerOperation(ctx, uuid.NewString(), "acl-config", false))
				return nil
			})
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.ReleaseOVNNetworkOperation(ctx, "default", "old", token)
			})
		})
	}
}

func TestACLGCDeletedIdentityOrphans(t *testing.T) {
	f := newACLGCFixture(t, false)
	good := OVNACLDirectionalPortGroups(f.acls["c"]).All
	deletedACL := OVNACLDirectionalPortGroups(999999).Ingress
	deletedNetwork := OVNACLNetworkPortGroupName(f.acls["c"], 999999)
	// Orphans of a deleted ACL or network are collectable; they no longer fail unrelated NIC removal.
	for _, inventory := range [][]ovn.OVNPortGroup{{good, deletedACL, deletedNetwork}, {deletedNetwork, deletedACL, good}} {
		plan := f.selectPlan(t, nil, "", nil, inventory)
		require.ElementsMatch(t, []ovn.OVNPortGroup{good, deletedACL, deletedNetwork}, plan)
	}

	// A foreign ACL's network group whose network is gone is not adopted by this project.
	foreign := OVNACLNetworkPortGroupName(f.acls["tenant"], 999999)
	require.ElementsMatch(t, []ovn.OVNPortGroup{good}, f.selectPlan(t, nil, "", nil, []ovn.OVNPortGroup{good, foreign}))
}

func TestACLOwnPortGroups(t *testing.T) {
	names := append(OVNACLDirectionalPortGroups(7).PortGroups(), OVNACLNetworkPortGroupName(7, 3), OVNACLNetworkPortGroupName(70, 3), OVNACLDirectionalPortGroups(17).All, "incus_acl07_all", "incus_acl7_future", "incus_net7", "unrelated")
	want := append(OVNACLDirectionalPortGroups(7).PortGroups(), OVNACLNetworkPortGroupName(7, 3))
	slices.Sort(want)
	require.Equal(t, want, aclOwnPortGroups(7, names))
	require.Empty(t, aclOwnPortGroups(8, names))
}
