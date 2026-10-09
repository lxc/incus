//go:build linux && cgo && !agent

package acl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

type aclReferenceFixture struct {
	c          *db.Cluster
	s          *state.State
	profileID  int64
	generation int64
	oldNetwork int64
	newNetwork int64
	oldACL     int64
	shadowACL  int64
	instances  []int64
}

func newACLReferenceFixture(t *testing.T) *aclReferenceFixture {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	f := &aclReferenceFixture{c: c, s: &state.State{DB: &db.DB{Cluster: c}}}
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		f.oldNetwork, err = tx.CreateNetwork(ctx, "default", "old", "", db.NetworkTypeBridge, nil)
		require.NoError(t, err)
		f.newNetwork, err = tx.CreateNetwork(ctx, "default", "new", "", db.NetworkTypeBridge, nil)
		require.NoError(t, err)
		id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "tenant"})
		require.NoError(t, err)
		require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"features.profiles": "false", "features.networks": "true", "restricted": "true", "restricted.networks.access": "old,new"}))
		_, err = tx.CreateNetwork(ctx, "tenant", "old", "", db.NetworkTypeBridge, nil)
		require.NoError(t, err)
		for _, projectName := range []string{"default", "tenant"} {
			for _, name := range []string{"acl-a", "acl-b"} {
				id, err := cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: projectName, Name: name})
				require.NoError(t, err)
				if name == "acl-a" {
					if projectName == "default" {
						f.oldACL = id
					} else {
						f.shadowACL = id
					}
				}
			}
		}
		f.profileID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "tracked"})
		require.NoError(t, err)
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "acl-a,acl-a,acl-b"}})
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

func (f *aclReferenceFixture) tx(t *testing.T, callback func(context.Context, *db.ClusterTx) error) {
	t.Helper()
	require.NoError(t, f.c.Transaction(context.Background(), callback))
}

func (f *aclReferenceFixture) commit(t *testing.T, target string, dispatch func(context.Context, db.ProfileReferenceConsumer) error) *project.ProfileReferenceCommit {
	t.Helper()
	put := api.ProfilePut{}
	if target != "" {
		put.Devices = map[string]map[string]string{"eth0": {"type": "nic", "network": "new", "security.acls": target}}
	}

	commit, err := project.CommitProfileReferenceUpdate(context.Background(), f.c, project.ProfileReferenceUpdate{Project: "default", Name: "tracked", ProfileID: f.profileID, Generation: f.generation, Profile: put}, dispatch)
	require.NoError(t, err)
	f.generation = commit.Generation
	return commit
}

func (f *aclReferenceFixture) identity(t *testing.T, token string, instanceID int64) db.ProfileReferenceApply {
	t.Helper()
	var identity db.ProfileReferenceApply
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		s, err := tx.ProfileReferenceState(ctx, instanceID)
		require.NoError(t, err)
		identity = db.ProfileReferenceApply{Token: uuid.NewString(), ChangeToken: token, InstanceID: instanceID, ProjectID: s.ProjectID, MemberID: s.MemberID, PlacementRevision: s.PlacementRevision, Sequence: s.DesiredSequence, Owner: "acl-usage-unit-fixture"}
		return nil
	})
	return identity
}

func (f *aclReferenceFixture) complete(t *testing.T, identity db.ProfileReferenceApply) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
		require.NoError(t, tx.SealProfileReferenceChildren(ctx, identity, nil))
		require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
		return tx.FinalizeProfileReferenceApply(ctx, identity)
	})
}

func (f *aclReferenceFixture) retained(t *testing.T) []db.ProfileReferenceUsage {
	t.Helper()
	var usage []db.ProfileReferenceUsage
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		usage, err = tx.ProfileReferenceUsage(ctx, 0, 0)
		return err
	})
	return usage
}

func (f *aclReferenceFixture) load(t *testing.T, projectName string, name string) *common {
	t.Helper()
	acl, err := LoadByName(f.s, projectName, name)
	require.NoError(t, err)
	commonACL, ok := acl.(*common)
	require.True(t, ok)
	return commonACL
}

func (f *aclReferenceFixture) rows(t *testing.T) []cluster.NetworkACL {
	t.Helper()
	var rows []cluster.NetworkACL
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		rows, err = cluster.GetNetworkACLs(ctx, tx.Tx())
		return err
	})
	return rows
}

func (f *aclReferenceFixture) assertUsage(t *testing.T, projectName string, name string, want ...string) {
	t.Helper()
	a := f.load(t, projectName, name)
	for _, firstOnly := range []bool{false, true} {
		got, err := a.usedBy(firstOnly)
		require.NoError(t, err)
		if !firstOnly {
			require.ElementsMatch(t, want, got)
		} else if len(want) == 0 {
			require.Empty(t, got)
		} else {
			require.Len(t, got, 1)
			require.Contains(t, want, got[0])
		}
	}
	got, err := a.UsedBy()
	require.NoError(t, err)
	require.ElementsMatch(t, want, got)
	used, err := a.isUsed()
	require.NoError(t, err)
	require.Equal(t, len(want) > 0, used)
}

func (f *aclReferenceFixture) assertBlocked(t *testing.T, a *common) {
	t.Helper()
	before := f.rows(t)
	retained := f.retained(t)
	require.ErrorContains(t, a.Delete(), "Cannot delete an ACL that is in use")
	require.ErrorContains(t, a.Rename("renamed"), "Cannot rename an ACL that is in use")
	require.Equal(t, before, f.rows(t))
	require.Equal(t, retained, f.retained(t))
}

func (f *aclReferenceFixture) assertMutable(t *testing.T, a *common) {
	t.Helper()
	id := a.ID()
	require.NoError(t, a.Rename("renamed"))
	loaded := f.load(t, a.Project(), "renamed")
	require.Equal(t, id, loaded.ID())
	require.NoError(t, loaded.Delete())
	_, err := LoadByName(f.s, a.Project(), "renamed")
	require.Error(t, err)
	for _, row := range f.rows(t) {
		require.NotEqual(t, id, int64(row.ID))
	}
}

func TestACLReferencePostCommitAndIndependentRelease(t *testing.T) {
	for _, target := range []string{"", "acl-b", "acl-a"} {
		t.Run("target-"+target, func(t *testing.T) {
			f := newACLReferenceFixture(t)
			consumers := []string{"/1.0/instances/consumer", "/1.0/instances/consumer?project=tenant"}
			want := append([]string(nil), consumers...)
			if target == "acl-a" {
				want = append(want, "/1.0/profiles/tracked")
			}

			a := f.load(t, "default", "acl-a")
			applied := 0
			commit := f.commit(t, target, func(ctx context.Context, consumer db.ProfileReferenceConsumer) error {
				// Observe actual commit before the injected application callback acknowledges anything.
				if applied == 0 {
					f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
						_, profile, err := tx.GetProfile(ctx, "default", "tracked")
						require.NoError(t, err)
						require.Equal(t, target, profile.Devices["eth0"]["security.acls"])
						usage, err := project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{ACLID: f.oldACL}, nil)
						require.NoError(t, err)
						if target != "acl-a" {
							require.Empty(t, usage.CurrentInstances)
							require.Empty(t, usage.CurrentProfiles)
						}

						require.GreaterOrEqual(t, len(usage.Retained), 2)
						return nil
					})
					f.assertUsage(t, "default", "acl-a", want...)
					f.assertBlocked(t, a)
				}

				applied++
				return nil
			})
			require.Equal(t, 2, applied)
			f.complete(t, f.identity(t, commit.Token, f.instances[0]))
			if target != "acl-a" {
				f.assertUsage(t, "default", "acl-a", consumers[1])
			} else {
				f.assertUsage(t, "default", "acl-a", want...)
			}

			f.assertBlocked(t, a)
			f.complete(t, f.identity(t, commit.Token, f.instances[1]))
			require.Empty(t, f.retained(t))
			if target != "acl-a" {
				f.assertUsage(t, "default", "acl-a")
				f.assertMutable(t, a)
			} else {
				f.assertUsage(t, "default", "acl-a", want...)
				f.assertBlocked(t, a)
			}
		})
	}
}

func TestACLReferenceSharedDefaultCurrentAndRetained(t *testing.T) {
	f := newACLReferenceFixture(t)
	// Shared-default NICs must never be attributed to a same-named tenant ACL.
	f.assertUsage(t, "tenant", "acl-a")
	want := []string{"/1.0/profiles/tracked", "/1.0/instances/consumer", "/1.0/instances/consumer?project=tenant"}
	f.assertUsage(t, "default", "acl-a", want...)
	commit := f.commit(t, "acl-a", nil)
	f.assertUsage(t, "tenant", "acl-a")
	f.assertUsage(t, "default", "acl-a", want...)
	for _, instanceID := range f.instances {
		f.complete(t, f.identity(t, commit.Token, instanceID))
	}

	require.Empty(t, f.retained(t))
	f.assertUsage(t, "tenant", "acl-a")
	f.assertUsage(t, "default", "acl-a", want...)
	f.assertBlocked(t, f.load(t, "default", "acl-a"))
	f.assertMutable(t, f.load(t, "tenant", "acl-a"))
}

func TestACLReferenceStandaloneAndWholeDeviceOverrides(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "profile", true: "local"}[local], func(t *testing.T) {
			f := newACLReferenceFixture(t)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				id, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "tenant", Name: "standalone"})
				require.NoError(t, err)
				devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "acl-a,acl-a"}, "eth1": {"type": "nic", "network": "old", "security.acls": "acl-a"}})
				require.NoError(t, err)
				require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), id, devices))
				id, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "override"})
				require.NoError(t, err)
				devices, err = cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "new", "security.acls": "acl-b"}})
				require.NoError(t, err)
				require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), id, devices))
				for i, projectName := range []string{"default", "tenant"} {
					order := []string{"tracked", "override"}
					if local {
						order = []string{"override", "tracked"}
						require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instances[i], devices))
					}

					require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[i]), projectName, order))
				}

				return nil
			})
			f.assertUsage(t, "tenant", "acl-a")
			f.assertUsage(t, "default", "acl-a", "/1.0/profiles/tracked", "/1.0/profiles/standalone?project=tenant")
			f.assertUsage(t, "default", "acl-b", "/1.0/profiles/tracked", "/1.0/profiles/override", "/1.0/instances/consumer", "/1.0/instances/consumer?project=tenant")
			f.assertBlocked(t, f.load(t, "default", "acl-a"))
		})
	}
}

func TestACLReferencePresentationDedupPreservesOwners(t *testing.T) {
	f := newACLReferenceFixture(t)
	f.commit(t, "acl-a", nil)
	commit := f.commit(t, "acl-a,acl-a,acl-b", nil)
	identity := f.identity(t, commit.Token, f.instances[0])
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
		return tx.SealProfileReferenceChildren(ctx, identity, nil)
	})
	before := f.retained(t)
	consumers := map[int64]bool{}
	attempts := 0
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		usage, err := tx.RetainedProfileReferenceUsage(ctx, db.NetworkReferenceFilter{ACLID: f.oldACL})
		require.NoError(t, err)
		require.Greater(t, len(usage), 4)
		for _, owner := range usage {
			consumers[owner.ConsumerID] = true
			if owner.AttemptToken == identity.Token {
				attempts++
			}
		}
		return nil
	})
	require.GreaterOrEqual(t, len(consumers), 4)
	require.Positive(t, attempts)
	f.assertUsage(t, "default", "acl-a", "/1.0/profiles/tracked", "/1.0/instances/consumer", "/1.0/instances/consumer?project=tenant")
	f.assertBlocked(t, f.load(t, "default", "acl-a"))
	require.Equal(t, before, f.retained(t))
}

func TestACLReferenceStaleFailureAndRollbackPreserveProtection(t *testing.T) {
	for _, outcome := range []db.ProfileReferenceFailure{db.ProfileReferenceRolledBack, db.ProfileReferenceUnknown} {
		t.Run(map[db.ProfileReferenceFailure]string{db.ProfileReferenceRolledBack: "rollback", db.ProfileReferenceUnknown: "unknown"}[outcome], func(t *testing.T) {
			f := newACLReferenceFixture(t)
			commit := f.commit(t, "", nil)
			identity := f.identity(t, commit.Token, f.instances[0])
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
				return tx.SealProfileReferenceChildren(ctx, identity, nil)
			})
			protected := f.retained(t)
			stale := identity
			stale.Sequence++
			err := f.c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
				return tx.FinalizeProfileReferenceApply(ctx, stale)
			})
			require.Error(t, err)
			require.Equal(t, protected, f.retained(t))
			f.assertBlocked(t, f.load(t, "default", "acl-a"))
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
			f.assertUsage(t, "default", "acl-a", "/1.0/instances/consumer", "/1.0/instances/consumer?project=tenant")
			f.assertBlocked(t, f.load(t, "default", "acl-a"))
		})
	}
}

func (f *aclReferenceFixture) networkDependency(t *testing.T) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNetwork(ctx, "default", "network-user", "", db.NetworkTypeBridge, map[string]string{"security.acls": "acl-a,acl-a"})
		require.NoError(t, err)
		return tx.NetworkCreated("default", "network-user")
	})
}

func TestACLReferenceNetworkRulesAndSelfExclusion(t *testing.T) {
	for _, dependency := range []string{"network", "ingress", "egress", "self"} {
		t.Run(dependency, func(t *testing.T) {
			f := newACLReferenceFixture(t)
			commit := f.commit(t, "", nil)
			for _, id := range f.instances {
				f.complete(t, f.identity(t, commit.Token, id))
			}

			a := f.load(t, "default", "acl-a")
			if dependency == "network" {
				f.networkDependency(t)
				f.assertUsage(t, "default", "acl-a", "/1.0/networks/network-user")
			} else {
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					id, err := cluster.GetNetworkACLID(ctx, tx.Tx(), "default", "acl-b")
					require.NoError(t, err)
					put := api.NetworkACLPut{}
					if dependency == "ingress" || dependency == "self" {
						put.Ingress = []api.NetworkACLRule{{Source: "acl-a,acl-a"}}
					}

					if dependency == "egress" || dependency == "self" {
						put.Egress = []api.NetworkACLRule{{Destination: "acl-a,acl-a"}}
					}

					if dependency == "self" {
						id = f.oldACL
					}

					return cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), id, &put)
				})
				if dependency == "self" {
					f.assertUsage(t, "default", "acl-a")
				} else {
					f.assertUsage(t, "default", "acl-a", "/1.0/network-acls/acl-b")
				}
			}
			if dependency == "self" {
				f.assertMutable(t, a)
				return
			}

			f.assertBlocked(t, a)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				if dependency == "network" {
					return tx.DeleteNetwork(ctx, "default", "network-user")
				}

				id, err := cluster.GetNetworkACLID(ctx, tx.Tx(), "default", "acl-b")
				require.NoError(t, err)
				return cluster.UpdateNetworkACLAPI(ctx, tx.Tx(), id, &api.NetworkACLPut{})
			})
			f.assertUsage(t, "default", "acl-a")
			f.assertMutable(t, a)
		})
	}
}

func TestACLReferenceInvalidIdentityAndReaderErrors(t *testing.T) {
	f := newACLReferenceFixture(t)
	f.networkDependency(t)
	for _, input := range []struct {
		project string
		id      int64
		name    string
	}{
		{"default", 0, "acl-a"},
		{"default", -1, "acl-a"},
		{"default", f.shadowACL, "acl-a"},
		{"tenant", f.oldACL, "acl-a"},
		{"default", f.oldACL, "acl-b"},
		{"default", f.oldACL, "missing"},
		{"missing", f.oldACL, "acl-a"},
		{"", f.oldACL, "acl-a"},
		{"default", f.oldACL, ""},
	} {
		a := &common{}
		a.init(f.s, input.id, input.project, &api.NetworkACL{NetworkACLPost: api.NetworkACLPost{Name: input.name}})
		f.assertReadError(t, a)
	}

	for _, fault := range []string{"retained", "current"} {
		t.Run(fault, func(t *testing.T) {
			f := newACLReferenceFixture(t)
			f.networkDependency(t)
			a := f.load(t, "default", "acl-a")
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				if fault == "retained" {
					// Fault only the normal isolated fixture's reader.
					_, err := tx.Tx().ExecContext(ctx, `ALTER TABLE profiles_reference_usage RENAME TO unavailable_profile_usage`)
					return err
				}

				devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "missing", "security.acls": "acl-a"}})
				require.NoError(t, err)
				return cluster.UpdateProfileDevices(ctx, tx.Tx(), f.profileID, devices)
			})
			f.assertReadError(t, a)
		})
	}
}

func (f *aclReferenceFixture) assertReadError(t *testing.T, a *common) {
	t.Helper()
	before := f.rows(t)
	for _, firstOnly := range []bool{false, true} {
		usage, err := a.usedBy(firstOnly)
		require.Error(t, err)
		require.Empty(t, usage)
	}

	usage, err := a.UsedBy()
	require.Error(t, err)
	require.Empty(t, usage)
	used, err := a.isUsed()
	require.Error(t, err)
	require.False(t, used)
	require.Error(t, a.Delete())
	require.Error(t, a.Rename("renamed"))
	require.Equal(t, before, f.rows(t))
}

func TestACLReferenceLegacyCallbacksRemainCurrentOnly(t *testing.T) {
	f := newACLReferenceFixture(t)
	count := func(projectName string) map[string]int {
		counts := map[string]int{}
		require.NoError(t, UsedBy(f.s, projectName, func(ctx context.Context, tx *db.ClusterTx, _ []string, usageType any, _ string, nic map[string]string) error {
			switch usageType.(type) {
			case db.InstanceArgs:
				counts["instance"]++
				require.NotEmpty(t, nic["network"])
			case cluster.Profile:
				counts["profile"]++
				require.NotEmpty(t, nic["network"])
			default:
				t.Fatalf("Unexpected legacy usage %T", usageType)
			}

			return nil
		}, "acl-a"))
		return counts
	}
	// Shared-default NICs belong to the default ACL while callbacks remain current-only.
	require.Equal(t, map[string]int{"instance": 2, "profile": 1}, count("default"))
	require.Empty(t, count("tenant"))
	f.commit(t, "", nil)
	require.Empty(t, count("default"))
	require.Empty(t, count("tenant"))
	require.NotEmpty(t, f.retained(t))
	f.assertUsage(t, "default", "acl-a", "/1.0/instances/consumer", "/1.0/instances/consumer?project=tenant")
}

func TestACLReferenceStaleLoadedObject(t *testing.T) {
	f := newACLReferenceFixture(t)
	f.networkDependency(t)
	stale := f.load(t, "default", "acl-a")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, cluster.RenameNetworkACL(ctx, tx.Tx(), "default", "acl-a", "displaced"))
		_, err := cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "acl-a"})
		return err
	})
	// The saved object must not use either the replacement's NICs or its network dependency.
	f.assertReadError(t, stale)
	replacement := f.load(t, "default", "acl-a")
	require.NotEqual(t, stale.ID(), replacement.ID())
	require.Equal(t, stale.ID(), f.load(t, "default", "displaced").ID())
}

func TestACLCurrentProjectManagedNICs(t *testing.T) {
	for _, tc := range []struct {
		name          string
		config        map[string]string
		sharedProject string
		localProject  string
	}{
		{"restricted-mixed", map[string]string{"features.networks": "true", "restricted": "true", "restricted.networks.access": " shared "}, "default", "tenant"},
		{"ordinary-shared", map[string]string{"features.networks": "false"}, "default", "default"},
		{"unrestricted-local", map[string]string{"features.networks": "true", "restricted.networks.access": "shared"}, "tenant", "tenant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newACLCurrentProjectFixture(t, tc.config)
			for _, query := range []string{"default", "tenant"} {
				t.Run(query, func(t *testing.T) {
					want := []aclCurrentProjectRow{}
					if query == "default" {
						for _, name := range []string{"shared", "local", "over", "drop"} {
							want = append(want, f.row("profile", "default", "template", name, f.template[name]))
						}
					}
					for _, item := range []struct{ device, resourceProject string }{{"shared", tc.sharedProject}, {"local", tc.localProject}} {
						if query == item.resourceProject {
							want = append(want, f.row("profile", "tenant", "standalone", item.device, f.standalone[item.device]))
						}
					}
					for _, item := range []struct{ device, resourceProject string }{{"shared", tc.sharedProject}, {"local", tc.localProject}, {"over", tc.localProject}} {
						if query == item.resourceProject {
							config := f.template[item.device]
							if item.device == "over" {
								config = f.local["over"]
							}

							want = append(want, f.row("instance", "tenant", "consumer", item.device, config))
						}
					}
					got := []aclCurrentProjectRow{}
					require.NoError(t, UsedBy(f.s, query, func(ctx context.Context, tx *db.ClusterTx, matched []string, usage any, device string, config map[string]string) error {
						row := f.callback(t, usage, device, config, matched)
						got = append(got, row)
						return nil
					}, "acl-b", "acl-a"))
					require.ElementsMatch(t, want, got, "exact current callback owners and managed NICs")
					nets := map[string]NetworkACLUsage{}
					require.NoError(t, NetworkUsage(f.s, query, []string{"acl-a", "acl-b"}, nets))
					expected := map[string]NetworkACLUsage{}
					for _, row := range want {
						name := row.Config["network"]
						entry := NetworkACLUsage{ID: f.networks[query+"/"+name], Name: name, Type: map[string]string{"shared": "bridge", "local": "ovn"}[name], Config: f.networkConfigs[query+"/"+name]}
						key := name
						if name == "shared" && row.Kind == "instance" {
							key = name + "/" + row.Project + "/" + row.Name + "/" + row.Device
							entry.InstanceName = row.Name
							entry.DeviceName = row.Device
							entry.InstanceProject = row.Project
						}

						expected[key] = entry
					}

					require.Equal(t, expected, nets, "exact resource project network IDs, types and owners")
				})
			}
		})
	}
}

type aclCurrentProjectRow struct {
	Kind    string
	Project string
	Name    string
	ID      int
	Device  string
	Config  map[string]string
	Matched []string
}

type aclCurrentProjectFixture struct {
	c              *db.Cluster
	s              *state.State
	networks       map[string]int64
	networkConfigs map[string]map[string]string
	template       map[string]map[string]string
	standalone     map[string]map[string]string
	local          map[string]map[string]string
	profiles       map[string]cluster.Profile
	instance       db.InstanceArgs
}

func newACLCurrentProjectFixture(t *testing.T, config map[string]string) *aclCurrentProjectFixture {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	nic := func(network, acls, marker string) map[string]string {
		return map[string]string{"type": "nic", "network": network, "security.acls": acls, "user.marker": marker}
	}

	f := &aclCurrentProjectFixture{c: c, s: &state.State{DB: &db.DB{Cluster: c}}, networks: map[string]int64{}, networkConfigs: map[string]map[string]string{}, profiles: map[string]cluster.Profile{}}
	f.template = map[string]map[string]string{"shared": nic("shared", "acl-a, acl-b", "profile-shared"), "local": nic("local", "acl-a", "profile-local"), "over": nic("shared", "acl-a", "overridden"), "drop": nic("shared", "acl-a", "removed-acl")}
	f.standalone = map[string]map[string]string{"shared": nic("shared", "acl-a, acl-b", "standalone-shared"), "local": nic("local", "acl-a", "standalone-local"), "unmanaged": {"type": "nic", "nictype": "bridged", "parent": "external", "security.acls": "acl-a"}, "empty": {"type": "nic", "network": "", "security.acls": "acl-a"}, "disk": {"type": "disk", "path": "/data", "network": "shared", "security.acls": "acl-a"}}
	f.local = map[string]map[string]string{"over": nic("local", "acl-b", "local-override"), "drop": {"type": "nic", "network": "shared"}, "unmanaged": f.standalone["unmanaged"], "empty": f.standalone["empty"], "disk": f.standalone["disk"]}
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "tenant"})
		require.NoError(t, err)
		config["features.profiles"] = "false"
		require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), id, config))
		for _, owner := range []string{"default", "tenant"} {
			for _, name := range []string{"shared", "local"} {
				kind := db.NetworkTypeBridge
				if name == "local" {
					kind = db.NetworkTypeOVN
				}

				cfg := map[string]string{"user.identity": owner + "/" + name}
				f.networkConfigs[owner+"/"+name] = cfg
				f.networks[owner+"/"+name], err = tx.CreateNetwork(ctx, owner, name, "", kind, cfg)
				require.NoError(t, err)
			}

			for _, name := range []string{"acl-a", "acl-b"} {
				_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: owner, Name: name, Ingress: []api.NetworkACLRule{{Source: "$" + owner + "-set"}}})
				require.NoError(t, err)
			}
		}

		for _, p := range []struct {
			owner, name string
			devices     map[string]map[string]string
		}{{"default", "template", f.template}, {"tenant", "standalone", f.standalone}} {
			profileID, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: p.owner, Name: p.name, Description: "original-" + p.owner})
			require.NoError(t, err)
			devices, err := cluster.APIToDevices(p.devices)
			require.NoError(t, err)
			require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), profileID, devices))
		}

		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "tenant", Name: "consumer", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now(), Description: "original-instance"})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(instanceID), "tenant", []string{"template"}))
		devices, err := cluster.APIToDevices(f.local)
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), instanceID, devices))
		profiles, err := cluster.GetProfiles(ctx, tx.Tx())
		require.NoError(t, err)
		for _, p := range profiles {
			f.profiles[p.Project+"/"+p.Name] = p
		}

		return tx.InstanceList(ctx, func(inst db.InstanceArgs, _ api.Project) error { f.instance = inst; return nil })
	}))
	require.Equal(t, "tenant", f.instance.Project)
	require.Equal(t, "consumer", f.instance.Name)
	return f
}

func (f *aclCurrentProjectFixture) row(kind, owner, name, device string, config map[string]string) aclCurrentProjectRow {
	id := f.instance.ID
	if kind == "profile" {
		id = f.profiles[owner+"/"+name].ID
	}

	matched := []string{"acl-a"}
	switch config["security.acls"] {
	case "acl-a, acl-b":
		matched = []string{"acl-a", "acl-b"}
	case "acl-b":
		matched = []string{"acl-b"}
	}

	return aclCurrentProjectRow{Kind: kind, Project: owner, Name: name, ID: id, Device: device, Config: config, Matched: matched}
}

func (f *aclCurrentProjectFixture) callback(t *testing.T, usage any, device string, config map[string]string, matched []string) aclCurrentProjectRow {
	t.Helper()
	row := aclCurrentProjectRow{Device: device, Config: config, Matched: matched}
	switch u := usage.(type) {
	case cluster.Profile:
		require.Equal(t, f.profiles[u.Project+"/"+u.Name], u, "original profile callback object")
		row.Kind = "profile"
		row.Project = u.Project
		row.Name = u.Name
		row.ID = u.ID
	case db.InstanceArgs:
		require.Equal(t, f.instance, u, "original instance callback object, local devices and profiles")
		row.Kind = "instance"
		row.Project = u.Project
		row.Name = u.Name
		row.ID = u.ID
	default:
		t.Fatalf("Unexpected usage %T", usage)
	}

	return row
}

func TestACLCurrentProjectBranchesAndCallbackErrors(t *testing.T) {
	for _, kind := range []string{"network", "profile", "rule", "instance"} {
		t.Run(kind, func(t *testing.T) {
			c, cleanup := db.NewTestCluster(t)
			t.Cleanup(cleanup)
			s := &state.State{DB: &db.DB{Cluster: c}}
			require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "tenant"})
				require.NoError(t, err)
				require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"features.networks": "true", "features.profiles": "true"}))
				var node string
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
				for _, owner := range []string{"default", "tenant"} {
					for _, name := range []string{"a", "b"} {
						_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: owner, Name: name, Ingress: []api.NetworkACLRule{{Source: name}}, Egress: []api.NetworkACLRule{{Destination: name}}})
						require.NoError(t, err)
					}

					for _, name := range []string{"first", "second"} {
						cfg := map[string]string{"user.owner": owner}
						if kind == "network" {
							cfg["security.acls"] = "a, b"
						}

						_, err = tx.CreateNetwork(ctx, owner, name, "original-"+owner, db.NetworkTypeBridge, cfg)
						require.NoError(t, err)
						require.NoError(t, tx.NetworkCreated(owner, name))
						devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": name, "security.acls": "a, b", "user.owner": owner}})
						require.NoError(t, err)
						switch kind {
						case "profile":
							id, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: owner, Name: name, Description: "original-" + owner})
							require.NoError(t, err)
							require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), id, devices))
						case "instance":
							id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: owner, Name: name, Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now(), Description: "original-" + owner})
							require.NoError(t, err)
							require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), id, devices))
						case "rule":
							_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: owner, Name: name, Description: "original-" + owner, Ingress: []api.NetworkACLRule{{Source: "a, a, b"}}, Egress: []api.NetworkACLRule{{Destination: "b, a"}}})
							require.NoError(t, err)
						}
					}
				}

				return nil
			}))
			for _, owner := range []string{"default", "tenant"} {
				names := []string{}
				require.NoError(t, UsedBy(s, owner, func(ctx context.Context, tx *db.ClusterTx, matched []string, usage any, device string, config map[string]string) error {
					require.Equal(t, []string{"a", "b"}, matched)
					var name string
					switch u := usage.(type) {
					case *api.Network:
						require.Equal(t, "network", kind)
						name = u.Name
						require.Equal(t, "original-"+owner, u.Description)
						require.Equal(t, api.ConfigMap{"security.acls": "a, b", "user.owner": owner}, u.Config)
						require.Empty(t, device)
						require.Nil(t, config)
					case *api.NetworkACL:
						require.Equal(t, "rule", kind)
						name = u.Name
						require.Equal(t, "original-"+owner, u.Description)
						require.Equal(t, []api.NetworkACLRule{{Source: "a, a, b"}}, u.Ingress)
						require.Equal(t, []api.NetworkACLRule{{Destination: "b, a"}}, u.Egress)
						require.Empty(t, device)
						require.Nil(t, config)
					case cluster.Profile:
						require.Equal(t, "profile", kind)
						name = u.Name
						require.Equal(t, owner, u.Project)
						require.Equal(t, "original-"+owner, u.Description)
					case db.InstanceArgs:
						require.Equal(t, "instance", kind)
						name = u.Name
						require.Equal(t, owner, u.Project)
						require.Equal(t, "original-"+owner, u.Description)
					default:
						t.Fatalf("Unexpected callback %T", usage)
					}

					if kind == "profile" || kind == "instance" {
						require.Equal(t, "eth0", device)
						require.Equal(t, map[string]string{"type": "nic", "network": name, "security.acls": "a, b", "user.owner": owner}, config)
					}

					names = append(names, name)
					return nil
				}, "b", "a"))
				require.ElementsMatch(t, []string{"first", "second"}, names, "project-local callbacks and self-rule exclusion")
				for _, stop := range []error{errors.New("callback failure"), db.ErrInstanceListStop} {
					calls := 0
					err := UsedBy(s, owner, func(context.Context, *db.ClusterTx, []string, any, string, map[string]string) error {
						calls++
						return stop
					}, "a", "b")
					require.ErrorIs(t, err, stop)
					require.Equal(t, 1, calls, "stop before second callback")
				}

				calls := 0
				require.NoError(t, UsedBy(s, owner, func(context.Context, *db.ClusterTx, []string, any, string, map[string]string) error {
					calls++
					return nil
				}))
				require.Zero(t, calls, "empty ACL selection")
			}
		})
	}
}

func TestACLProfileSnapshotRetry(t *testing.T) {
	for _, scenario := range []string{"replay", "change", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			c, cleanup := db.NewTestCluster(t)
			t.Cleanup(cleanup)
			beforeConfig := map[string]string{"features.profiles": "true", "features.networks": "true", "restricted": "true", "restricted.networks.access": "shared", "user.marker": "before"}
			afterConfig := map[string]string{"features.profiles": "true", "features.networks": "false", "user.marker": "after"}
			ownerIDs := map[string]int{}
			createOwner := func(ctx context.Context, tx *db.ClusterTx, name string, config map[string]string) *api.Project {
				id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: name, Description: "owner-" + name})
				require.NoError(t, err)
				ownerIDs[name] = int(id)
				require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), id, config))
				return &api.Project{Name: name, ProjectPut: api.ProjectPut{Description: "owner-" + name, Config: config}}
			}

			writeDevices := func(ctx context.Context, tx *db.ClusterTx, id int, name, marker string) map[string]cluster.Device {
				config := map[string]map[string]string{name: {"type": "nic", "network": "shared", "security.acls": "acl-a", "user.marker": marker}}
				devices, err := cluster.APIToDevices(config)
				require.NoError(t, err)
				require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), int64(id), devices))
				devices, err = cluster.GetProfileDevices(ctx, tx.Tx(), id)
				require.NoError(t, err)
				require.Equal(t, config, cluster.DevicesToAPI(devices))
				return devices
			}

			createProfile := func(ctx context.Context, tx *db.ClusterTx, owner, name string) cluster.Profile {
				id, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: owner, Name: name, Description: "profile-" + name})
				require.NoError(t, err)
				return cluster.Profile{ID: int(id), ProjectID: ownerIDs[owner], Project: owner, Name: name, Description: "profile-" + name}
			}

			var old, survivor cluster.Profile
			var oldDevices, survivorDevices map[string]cluster.Device
			var oldOwner, survivorOwner *api.Project
			require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				// Keep the default-project all-owner scan limited to these two exact fixture identities.
				require.NoError(t, cluster.DeleteProfile(ctx, tx.Tx(), "default", "default"))
				oldOwner = createOwner(ctx, tx, "obsolete", beforeConfig)
				survivorOwner = createOwner(ctx, tx, "tenant", beforeConfig)
				old = createProfile(ctx, tx, "obsolete", "old-only")
				survivor = createProfile(ctx, tx, "tenant", "survivor")
				oldDevices = writeDevices(ctx, tx, old.ID, "old-nic", "old-device")
				survivorDevices = writeDevices(ctx, tx, survivor.ID, "before-nic", "before-device")
				return nil
			}))
			beforeProfiles := []cluster.Profile{old, survivor}
			beforeDevices := map[int]map[string]cluster.Device{old.ID: oldDevices, survivor.ID: survivorDevices}
			beforeProjects := map[string]*api.Project{"obsolete": oldOwner, "tenant": survivorOwner}
			wantProfiles, wantDevices, wantProjects := beforeProfiles, beforeDevices, beforeProjects
			var snapshot, first aclProfileSnapshot
			attempts := 0
			err := c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				attempts++
				require.LessOrEqual(t, attempts, 2, "only the explicit post-capture error may replay")
				if attempts == 2 && scenario != "replay" {
					require.NoError(t, cluster.DeleteProfile(ctx, tx.Tx(), old.Project, old.Name))
					require.NoError(t, cluster.DeleteProject(ctx, tx.Tx(), old.Project))
					if scenario == "empty" {
						require.NoError(t, cluster.DeleteProfile(ctx, tx.Tx(), survivor.Project, survivor.Name))
						require.NoError(t, cluster.DeleteProject(ctx, tx.Tx(), survivor.Project))
						wantProfiles = nil
						wantDevices = map[int]map[string]cluster.Device{}
						wantProjects = map[string]*api.Project{}
					} else {
						changed := survivor
						changed.Description = "survivor-after"
						require.NoError(t, cluster.UpdateProfile(ctx, tx.Tx(), survivor.Project, survivor.Name, changed))
						changedDevices := writeDevices(ctx, tx, survivor.ID, "after-nic", "after-device")
						changedOwner := &api.Project{Name: "tenant", ProjectPut: api.ProjectPut{Description: "owner-after", Config: afterConfig}}
						require.NoError(t, cluster.UpdateProject(ctx, tx.Tx(), "tenant", changedOwner.ProjectPut))
						newOwner := createOwner(ctx, tx, "new-owner", afterConfig)
						added := createProfile(ctx, tx, "new-owner", "new-profile")
						require.NotEqual(t, old.ID, added.ID)
						addedDevices := writeDevices(ctx, tx, added.ID, "new-nic", "new-device")
						wantProfiles = []cluster.Profile{changed, added}
						wantDevices = map[int]map[string]cluster.Device{changed.ID: changedDevices, added.ID: addedDevices}
						wantProjects = map[string]*api.Project{"tenant": changedOwner, "new-owner": newOwner}
					}
				}

				err := snapshot.collect(ctx, tx, api.ProjectDefaultName)
				if err != nil {
					return err
				}

				if attempts == 1 {
					require.ElementsMatch(t, beforeProfiles, snapshot.profiles)
					require.Equal(t, beforeDevices, snapshot.devices)
					require.Equal(t, beforeProjects, snapshot.projects)
					first = snapshot
					// This injects a callback error after capture, not a Commit or Rollback failure.
					return sqlite3.ErrBusy
				}

				return nil
			})
			require.NoError(t, err)
			require.Equal(t, 2, attempts)
			require.ElementsMatch(t, wantProfiles, snapshot.profiles, "final snapshot contains each current fixture profile exactly once")
			require.Equal(t, wantDevices, snapshot.devices, "final device IDs and configurations replace the prior attempt")
			require.Equal(t, wantProjects, snapshot.projects, "final complete owner records replace the prior attempt")
			require.ElementsMatch(t, beforeProfiles, first.profiles, "prior profile snapshot remains unchanged")
			require.Equal(t, beforeDevices, first.devices, "prior device map aliases remain unchanged")
			require.Equal(t, beforeProjects, first.projects, "prior owner map aliases remain unchanged")
			if scenario != "replay" {
				_, found := snapshot.devices[old.ID]
				require.False(t, found, "removed profile has no stale device-map key")
				_, found = snapshot.projects[old.Project]
				require.False(t, found, "removed owner has no stale project-cache key")
			}

			if scenario == "empty" {
				require.Empty(t, snapshot.profiles)
				require.Empty(t, snapshot.devices)
				require.Empty(t, snapshot.projects)
			}
		})
	}
}
