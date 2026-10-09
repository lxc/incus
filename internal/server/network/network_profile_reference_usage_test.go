//go:build linux && cgo && !agent

package network

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

type usedByRetainedFixture struct {
	c          *db.Cluster
	s          *state.State
	profileID  int64
	oldNetwork int64
	newNetwork int64
	shadow     int64
	instances  []int64
}

func newUsedByRetainedFixture(t *testing.T) *usedByRetainedFixture {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	f := &usedByRetainedFixture{c: c, s: &state.State{DB: &db.DB{Cluster: c}}}
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		f.oldNetwork, err = tx.CreateNetwork(ctx, "default", "old", "", db.NetworkTypeBridge, nil)
		require.NoError(t, err)
		f.newNetwork, err = tx.CreateNetwork(ctx, "default", "new", "", db.NetworkTypeBridge, nil)
		require.NoError(t, err)
		id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "tenant"})
		require.NoError(t, err)
		require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"features.profiles": "false", "features.networks": "false"}))
		f.shadow, err = tx.CreateNetwork(ctx, "tenant", "old", "", db.NetworkTypeBridge, nil)
		require.NoError(t, err)
		for _, name := range []string{"acl-a", "acl-b"} {
			_, err := cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: name})
			require.NoError(t, err)
		}

		f.profileID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "tracked"})
		require.NoError(t, err)
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "acl-a,acl-b"}})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), f.profileID, devices))
		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		for _, projectName := range []string{"default", "tenant"} {
			id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: projectName, Name: "consumer", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
			require.NoError(t, err)
			require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(id), projectName, []string{"tracked"}))
			f.instances = append(f.instances, id)
			// Establish the fixture's successful creation baseline before any profile transition.
			snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, id)
			require.NoError(t, err)
			require.NoError(t, tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1))
		}

		return nil
	})
	return f
}

func (f *usedByRetainedFixture) tx(t *testing.T, callback func(context.Context, *db.ClusterTx) error) {
	t.Helper()
	require.NoError(t, f.c.Transaction(context.Background(), callback))
}

func (f *usedByRetainedFixture) commit(t *testing.T, target string, dispatch func(context.Context, db.ProfileReferenceConsumer) error) *project.ProfileReferenceCommit {
	t.Helper()
	put := api.ProfilePut{}
	if target != "" {
		put.Devices = map[string]map[string]string{"eth0": {"type": "nic", "network": target}}
	}

	commit, err := project.CommitProfileReferenceUpdate(context.Background(), f.c, project.ProfileReferenceUpdate{Project: "default", Name: "tracked", ProfileID: f.profileID, Profile: put}, dispatch)
	require.NoError(t, err)
	return commit
}

func (f *usedByRetainedFixture) identity(t *testing.T, token string, instanceID int64) db.ProfileReferenceApply {
	t.Helper()
	var identity db.ProfileReferenceApply
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		s, err := tx.ProfileReferenceState(ctx, instanceID)
		require.NoError(t, err)
		identity = db.ProfileReferenceApply{Token: uuid.NewString(), ChangeToken: token, InstanceID: instanceID, ProjectID: s.ProjectID, MemberID: s.MemberID, PlacementRevision: s.PlacementRevision, Sequence: s.DesiredSequence, Owner: "usedby-unit-fixture"}
		return nil
	})
	return identity
}

func (f *usedByRetainedFixture) complete(t *testing.T, identity db.ProfileReferenceApply) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
		require.NoError(t, tx.SealProfileReferenceChildren(ctx, identity, nil))
		require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
		return tx.FinalizeProfileReferenceApply(ctx, identity)
	})
}

func (f *usedByRetainedFixture) retained(t *testing.T) []db.ProfileReferenceUsage {
	t.Helper()
	var usage []db.ProfileReferenceUsage
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		usage, err = tx.ProfileReferenceUsage(ctx, f.oldNetwork, 0)
		return err
	})
	return usage
}

func (f *usedByRetainedFixture) assertUsage(t *testing.T, projectName string, id int64, name string, want ...string) {
	t.Helper()
	for _, firstOnly := range []bool{false, true} {
		got, err := UsedBy(f.s, projectName, id, name, "bridge", firstOnly)
		require.NoError(t, err)
		if !firstOnly {
			require.ElementsMatch(t, want, got)
		} else if len(want) == 0 {
			require.Empty(t, got)
		} else {
			require.NotEmpty(t, got)
			for _, url := range got {
				require.Contains(t, want, url)
			}
		}
	}

	n := common{state: f.s, project: projectName, id: id, name: name, netType: "bridge"}
	used, err := n.IsUsed(false)
	require.NoError(t, err)
	require.Equal(t, len(want) > 0, used)
}

func TestUsedByRetainedCommitAndIndependentRelease(t *testing.T) {
	for _, target := range []string{"", "new", "old"} {
		t.Run("target-"+target, func(t *testing.T) {
			f := newUsedByRetainedFixture(t)
			consumers := []string{"/1.0/instances/consumer", "/1.0/instances/consumer?project=tenant"}
			want := append([]string(nil), consumers...)
			if target == "old" {
				want = append(want, "/1.0/profiles/tracked")
			}

			applied := 0
			commit := f.commit(t, target, func(ctx context.Context, consumer db.ProfileReferenceConsumer) error {
				// The real commit has returned to dispatch, before this injected applier runs.
				if applied == 0 {
					f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
						_, profile, err := tx.GetProfile(ctx, "default", "tracked")
						require.NoError(t, err)
						require.Equal(t, target, profile.Devices["eth0"]["network"])
						return nil
					})
					current := 0
					require.NoError(t, UsedByInstanceDevices(f.s, "default", "old", "bridge", func(inst db.InstanceArgs, name string, config map[string]string) error {
						current++
						return nil
					}))
					if target != "old" {
						require.Zero(t, current)
					} else {
						require.Equal(t, 2, current)
					}

					f.assertUsage(t, "default", f.oldNetwork, "old", want...)
					require.Greater(t, len(f.retained(t)), 2)
				}

				applied++
				return nil
			})
			require.Equal(t, 2, applied)
			f.complete(t, f.identity(t, commit.Token, f.instances[0]))
			for _, row := range f.retained(t) {
				require.Equal(t, f.instances[1], row.InstanceID)
			}

			if target != "old" {
				f.assertUsage(t, "default", f.oldNetwork, "old", consumers[1])
			} else {
				f.assertUsage(t, "default", f.oldNetwork, "old", want...)
			}

			f.complete(t, f.identity(t, commit.Token, f.instances[1]))
			require.Empty(t, f.retained(t))
			if target != "old" {
				f.assertUsage(t, "default", f.oldNetwork, "old")
			} else {
				f.assertUsage(t, "default", f.oldNetwork, "old", want...)
			}

			if target == "new" {
				f.assertUsage(t, "default", f.newNetwork, "new", "/1.0/profiles/tracked", consumers[0], consumers[1])
			}
		})
	}
}

func TestUsedByRetainedStaleRollbackAndUnknown(t *testing.T) {
	for _, outcome := range []db.ProfileReferenceFailure{db.ProfileReferenceRolledBack, db.ProfileReferenceUnknown} {
		t.Run(map[db.ProfileReferenceFailure]string{db.ProfileReferenceRolledBack: "rollback", db.ProfileReferenceUnknown: "unknown"}[outcome], func(t *testing.T) {
			f := newUsedByRetainedFixture(t)
			commit := f.commit(t, "", nil)
			identity := f.identity(t, commit.Token, f.instances[0])
			beforeClaim := f.retained(t)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
				return tx.SealProfileReferenceChildren(ctx, identity, nil)
			})
			protected := f.retained(t)
			require.Greater(t, len(protected), len(beforeClaim))
			want := []string{"/1.0/instances/consumer", "/1.0/instances/consumer?project=tenant"}
			f.assertUsage(t, "default", f.oldNetwork, "old", want...)
			require.Equal(t, protected, f.retained(t))
			stale := identity
			stale.Sequence++
			err := f.c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
				return tx.FinalizeProfileReferenceApply(ctx, stale)
			})
			require.Error(t, err)
			require.Equal(t, protected, f.retained(t))
			f.assertUsage(t, "default", f.oldNetwork, "old", want...)
			rollback := errors.New("injected completion transaction rollback")
			err = f.c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
				require.NoError(t, tx.FinalizeProfileReferenceApply(ctx, identity))
				return rollback
			})
			require.ErrorIs(t, err, rollback)
			require.Equal(t, protected, f.retained(t))
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.FailProfileReferenceApply(ctx, identity, outcome)
			})
			require.Equal(t, protected, f.retained(t))
			f.assertUsage(t, "default", f.oldNetwork, "old", want...)
			if outcome == db.ProfileReferenceRolledBack {
				f.complete(t, f.identity(t, commit.Token, f.instances[0]))
				f.assertUsage(t, "default", f.oldNetwork, "old", want[1])
				f.complete(t, f.identity(t, commit.Token, f.instances[1]))
				require.Empty(t, f.retained(t))
				f.assertUsage(t, "default", f.oldNetwork, "old")
			} else {
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					s, err := tx.ProfileReferenceState(ctx, identity.InstanceID)
					require.NoError(t, err)
					require.Equal(t, identity.Token, s.ActiveToken)
					return nil
				})
			}
		})
	}
}

func TestUsedByRetainedResourceAndInstanceProjects(t *testing.T) {
	f := newUsedByRetainedFixture(t)
	f.commit(t, "", nil)
	f.assertUsage(t, "default", f.oldNetwork, "old", "/1.0/instances/consumer", "/1.0/instances/consumer?project=tenant")
	f.assertUsage(t, "tenant", f.shadow, "old")
	for _, input := range []struct {
		project string
		id      int64
		name    string
	}{
		{"tenant", f.oldNetwork, "old"},
		{"default", f.shadow, "old"},
		{"default", f.oldNetwork, "new"},
		{"default", f.oldNetwork, "missing"},
		{"missing", f.oldNetwork, "old"},
		{"default", f.oldNetwork + f.newNetwork + f.shadow, "old"},
	} {
		for _, firstOnly := range []bool{false, true} {
			usage, err := UsedBy(f.s, input.project, input.id, input.name, "bridge", firstOnly)
			require.Error(t, err)
			require.Empty(t, usage)
		}

		n := common{state: f.s, project: input.project, id: input.id, name: input.name, netType: "bridge"}
		used, err := n.IsUsed(false)
		require.Error(t, err)
		require.False(t, used)
	}
	// Zero is the unmanaged API caller's sentinel, never a wildcard retained query.
	for _, id := range []int64{0, -1} {
		f.assertUsage(t, "default", id, "old")
	}

	n := common{state: f.s, project: "default", id: f.oldNetwork, name: "old", netType: "bridge"}
	used, err := n.IsUsed(true)
	require.NoError(t, err)
	require.False(t, used) // Instance-only callbacks intentionally remain current-only.
}

func TestUsedByRetainedPreservesStandaloneAndOverriddenProfiles(t *testing.T) {
	f := newUsedByRetainedFixture(t)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "standalone"})
		require.NoError(t, err)
		old, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "old"}})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), id, old))
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}, "eth1": {"type": "nic", "network": "new"}})
		require.NoError(t, err)
		for _, instanceID := range f.instances {
			require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), instanceID, devices))
		}

		return nil
	})
	f.assertUsage(t, "default", f.oldNetwork, "old", "/1.0/profiles/tracked", "/1.0/profiles/standalone")
	f.assertUsage(t, "default", f.newNetwork, "new", "/1.0/instances/consumer", "/1.0/instances/consumer?project=tenant")
}

func (f *usedByRetainedFixture) peer(t *testing.T) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := cluster.CreateNetworkPeer(ctx, tx.Tx(), cluster.NetworkPeer{NetworkID: f.oldNetwork, Name: "linked", Type: cluster.NetworkPeerTypeLocal, TargetNetworkID: sql.NullInt64{Int64: f.newNetwork, Valid: true}})
		return err
	})
}

func TestUsedByRetainedPreservesPeersAndUplinks(t *testing.T) {
	f := newUsedByRetainedFixture(t)
	f.commit(t, "", nil)
	f.peer(t)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, key := range []string{"network", "parent"} {
			_, err := tx.CreateNetwork(ctx, "tenant", key+"-user", "", db.NetworkTypePhysical, map[string]string{key: "old"})
			require.NoError(t, err)
			require.NoError(t, tx.NetworkCreated("tenant", key+"-user"))
		}

		return nil
	})
	f.assertUsage(t, "default", f.oldNetwork, "old", "/1.0/networks/new", "/1.0/networks/network-user?project=tenant", "/1.0/networks/parent-user?project=tenant", "/1.0/instances/consumer", "/1.0/instances/consumer?project=tenant")
}

func TestUsedByRetainedReadFailureAndUnmanagedParents(t *testing.T) {
	f := newUsedByRetainedFixture(t)
	f.commit(t, "", nil)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "unmanaged"})
		require.NoError(t, err)
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "nictype": "macvlan", "parent": "host0", "vlan": "42"}})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), id, devices))
		require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instances[0], devices))
		// Fault only this isolated fixture's retained query, without touching real cluster state.
		_, err = tx.Tx().ExecContext(ctx, `ALTER TABLE profiles_reference_usage RENAME TO unavailable_profile_usage`)
		return err
	})
	for _, firstOnly := range []bool{false, true} {
		usage, err := UsedBy(f.s, "default", f.oldNetwork, "old", "bridge", firstOnly)
		require.ErrorContains(t, err, "Failed getting retained network usage")
		require.Empty(t, usage)
	}

	n := common{state: f.s, project: "default", id: f.oldNetwork, name: "old", netType: "bridge"}
	used, err := n.IsUsed(false)
	require.Error(t, err)
	require.False(t, used)
	f.assertUsage(t, "default", 0, "host0.42", "/1.0/profiles/unmanaged", "/1.0/instances/consumer")
	f.assertUsage(t, "default", 0, "absent-host")
	// Existing positive peer evidence can short-circuit a later retained read failure.
	f.peer(t)
	usage, err := UsedBy(f.s, "default", f.oldNetwork, "old", "bridge", true)
	require.NoError(t, err)
	require.Equal(t, []string{"/1.0/networks/new"}, usage)
	used, err = n.IsUsed(false)
	require.NoError(t, err)
	require.True(t, used)
}
