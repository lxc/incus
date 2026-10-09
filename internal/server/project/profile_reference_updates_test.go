//go:build linux && cgo && !agent

package project_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
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

type profileReferenceFixture struct {
	c          *db.Cluster
	ctx        context.Context
	profileID  int64
	instances  []int64
	oldNetwork int64
	newNetwork int64
	acl        int64
	project    string
}

func newProfileReferenceFixture(t *testing.T, consumers int, mode string) *profileReferenceFixture {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	f := &profileReferenceFixture{c: c, ctx: context.Background(), project: "default"}
	require.NoError(t, c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		f.oldNetwork, err = tx.CreateNetwork(ctx, "default", "old", "", db.NetworkTypeBridge, nil)
		require.NoError(t, err)
		f.newNetwork, err = tx.CreateNetwork(ctx, "default", "new", "", db.NetworkTypeBridge, nil)
		require.NoError(t, err)
		f.acl, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "old-acl"})
		require.NoError(t, err)
		if mode != "default" {
			f.project = "tenant"
			id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: f.project})
			require.NoError(t, err)
			config := map[string]string{"features.profiles": "false", "features.networks": "false"}
			if mode == "shared" {
				config["features.networks"] = "true"
				config["restricted"] = "true"
				config["restricted.networks.access"] = "old,new"
			}

			require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), id, config))
			// Same names in the tenant must not replace shared-default resource identity.
			_, err = tx.CreateNetwork(ctx, f.project, "old", "", db.NetworkTypeBridge, nil)
			require.NoError(t, err)
			_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: f.project, Name: "old-acl"})
			require.NoError(t, err)
		}

		f.profileID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "tracked", Description: "old description"})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileConfig(ctx, tx.Tx(), f.profileID, map[string]string{"user.test": "old"}))
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "old-acl", "mtu": "1400"}})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), f.profileID, devices))
		var nodeName string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&nodeName))
		for i := 0; i < consumers; i++ {
			id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: f.project, Name: fmt.Sprintf("consumer-%d", i), Node: nodeName, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
			require.NoError(t, err)
			require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(id), f.project, []string{"tracked"}))
			f.instances = append(f.instances, id)
			// This baseline belongs to the fixture's successful creation transaction, not migration.
			snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, id)
			require.NoError(t, err)
			require.NoError(t, tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1))
		}

		return nil
	}))
	return f
}

func (f *profileReferenceFixture) request(profile api.ProfilePut, generation int64) project.ProfileReferenceUpdate {
	return project.ProfileReferenceUpdate{Project: f.project, Name: "tracked", ProfileID: f.profileID, Generation: generation, Profile: profile}
}

func (f *profileReferenceFixture) tx(t *testing.T, callback func(context.Context, *db.ClusterTx) error) {
	t.Helper()
	require.NoError(t, f.c.Transaction(f.ctx, callback))
}

func (f *profileReferenceFixture) identity(t *testing.T, token string, instanceID int64) db.ProfileReferenceApply {
	t.Helper()
	var identity db.ProfileReferenceApply
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		s, err := tx.ProfileReferenceState(ctx, instanceID)
		require.NoError(t, err)
		identity = db.ProfileReferenceApply{Token: uuid.NewString(), ChangeToken: token, InstanceID: instanceID, ProjectID: s.ProjectID, MemberID: s.MemberID, PlacementRevision: s.PlacementRevision, Sequence: s.DesiredSequence, Owner: "process-incarnation"}
		if token == "" {
			identity.Sequence++
		}

		return nil
	})
	return identity
}

func (f *profileReferenceFixture) complete(t *testing.T, identity db.ProfileReferenceApply) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
		require.NoError(t, tx.SealProfileReferenceChildren(ctx, identity, nil))
		require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
		return tx.FinalizeProfileReferenceApply(ctx, identity)
	})
}

func TestProfileReferenceCommitBoundaryAndIndependentConsumers(t *testing.T) {
	f := newProfileReferenceFixture(t, 2, "inherited")
	entered := make(chan struct{})
	resume := make(chan struct{})
	done := make(chan error, 1)
	var commit *project.ProfileReferenceCommit
	var applied atomic.Int32
	injectedApplier := func() error {
		applied.Add(1)
		return errors.New("injected application failure")
	}

	go func() {
		var err error
		commit, err = project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Description: "saved description", Config: map[string]string{"user.test": "new"}}, 0), func(ctx context.Context, consumer db.ProfileReferenceConsumer) error {
			if applied.Load() == 0 {
				close(entered)
				<-resume
			}

			return injectedApplier()
		})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("Commit failed before dispatch: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("Post-commit dispatcher did not arrive")
	}

	require.Zero(t, applied.Load())
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, profile, err := tx.GetProfile(ctx, "default", "tracked")
		require.NoError(t, err)
		require.Equal(t, "saved description", profile.Description)
		require.Equal(t, "new", profile.Config["user.test"])
		require.Empty(t, profile.Devices)
		usage, err := tx.ProfileReferenceUsage(ctx, f.oldNetwork, f.acl)
		require.NoError(t, err)
		require.Len(t, usage, 4)
		return nil
	})
	close(resume)
	require.Error(t, <-done)
	require.Equal(t, int32(2), applied.Load())
	require.Len(t, commit.Consumers, 2)
	first := f.identity(t, commit.Token, f.instances[0])
	f.complete(t, first)
	second := f.identity(t, commit.Token, f.instances[1])
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, second))
		require.NoError(t, tx.SealProfileReferenceChildren(ctx, second, nil))
		return tx.FailProfileReferenceApply(ctx, second, db.ProfileReferenceRolledBack)
	})
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		consumers, err := tx.ProfileReferenceConsumers(ctx, commit.Token)
		require.NoError(t, err)
		require.True(t, consumers[0].Satisfied)
		require.False(t, consumers[1].Satisfied)
		usage, err := tx.ProfileReferenceUsage(ctx, f.oldNetwork, f.acl)
		require.NoError(t, err)
		require.NotEmpty(t, usage)
		for _, owner := range usage {
			require.Equal(t, f.instances[1], owner.InstanceID)
		}

		state, err := tx.ProfileReferenceState(ctx, f.instances[1])
		require.NoError(t, err)
		require.Equal(t, "old", state.Applied.ExpandedDevices["eth0"]["network"])
		require.Empty(t, state.Desired.ExpandedDevices)
		return nil
	})
	second = f.identity(t, commit.Token, f.instances[1])
	f.complete(t, second)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		usage, err := tx.ProfileReferenceUsage(ctx, 0, 0)
		require.NoError(t, err)
		require.Empty(t, usage)
		require.NoError(t, tx.FinalizeProfileReferenceApply(ctx, first))
		return tx.FinalizeProfileReferenceApply(ctx, second)
	})
}

func TestProfileReferenceRepointNonCreatedAndDesiredValidation(t *testing.T) {
	for _, mode := range []string{"default", "inherited", "shared"} {
		t.Run(mode, func(t *testing.T) {
			f := newProfileReferenceFixture(t, 1, mode)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error { return tx.NetworkDeleting("default", "old") })
			request := f.request(api.ProfilePut{Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}}}, 0)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error { return tx.NetworkDeleting("default", "new") })
			_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, request, nil)
			require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error { return tx.NetworkCreated("default", "new") })
			commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, request, nil)
			require.NoError(t, err)
			require.Equal(t, f.oldNetwork, commit.Consumers[0].Before.Resources[0].NetworkID)
			require.Equal(t, f.newNetwork, commit.Consumers[0].After.Resources[0].NetworkID)
			require.NotEqual(t, commit.Consumers[0].ProjectID, int64(0))
			if mode != "default" {
				require.NotEqual(t, commit.Consumers[0].ProjectID, commit.Consumers[0].Before.Resources[0].NetworkProjectID)
			}

			f.complete(t, f.identity(t, commit.Token, f.instances[0]))
		})
	}
}

func TestProfileReferenceOverlappingProfilesPreserveAppliedBaseline(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "default")
	var secondProfile int64
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		secondProfile, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "second"})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), "default", []string{"tracked", "second"}))
		// Recreate only this fixture's baseline before any pending transition exists.
		_, err = tx.Tx().ExecContext(ctx, `DELETE FROM instances_profile_reference_apply WHERE instance_id=?`, f.instances[0])
		require.NoError(t, err)
		snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, f.instances[0])
		require.NoError(t, err)
		return tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1)
	})
	first, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
	require.NoError(t, err)
	second, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, project.ProfileReferenceUpdate{Project: "default", Name: "second", ProfileID: secondProfile, Profile: api.ProfilePut{Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}}}}, nil)
	require.NoError(t, err)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		s, err := tx.ProfileReferenceState(ctx, f.instances[0])
		require.NoError(t, err)
		require.Equal(t, int64(2), s.DesiredSequence)
		require.Equal(t, int64(0), s.AppliedSequence)
		require.Equal(t, "old", s.Applied.ExpandedDevices["eth0"]["network"])
		require.Equal(t, "new", s.Desired.ExpandedDevices["eth0"]["network"])
		return nil
	})
	f.complete(t, f.identity(t, second.Token, f.instances[0]))
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, token := range []string{first.Token, second.Token} {
			consumers, err := tx.ProfileReferenceConsumers(ctx, token)
			require.NoError(t, err)
			require.True(t, consumers[0].Satisfied)
		}

		usage, err := tx.ProfileReferenceUsage(ctx, 0, 0)
		require.NoError(t, err)
		require.Empty(t, usage)
		return nil
	})
}

func TestProfileReferenceNilNoopRollbackAndUnknownBaseline(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "default")
	request := f.request(api.ProfilePut{}, 0)
	rollback := errors.New("rollback transaction")
	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := project.CaptureAndCommitProfileReferenceUpdate(ctx, tx, request)
		require.NoError(t, err)
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, profile, err := tx.GetProfile(ctx, "default", "tracked")
		require.NoError(t, err)
		require.Equal(t, "old description", profile.Description)
		require.Equal(t, "old", profile.Config["user.test"])
		require.Equal(t, "old", profile.Devices["eth0"]["network"])
		var count int
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM profiles_reference_changes`).Scan(&count))
		require.Zero(t, count)
		usage, err := tx.ProfileReferenceUsage(ctx, 0, 0)
		require.NoError(t, err)
		require.Empty(t, usage)
		return nil
	})
	commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, request, nil)
	require.NoError(t, err)
	repeat, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Config: map[string]string{}, Devices: map[string]map[string]string{}}, 1), nil)
	require.NoError(t, err)
	require.Equal(t, commit.Token, repeat.Token)
	require.Equal(t, commit.Generation, repeat.Generation)
	f.complete(t, f.identity(t, commit.Token, f.instances[0]))
	unknown := newProfileReferenceFixture(t, 1, "default")
	unknown.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, `DELETE FROM instances_profile_reference_apply`)
		return err
	})
	_, err = project.CommitProfileReferenceUpdate(unknown.ctx, unknown.c, unknown.request(api.ProfilePut{}, 0), nil)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
}

func TestProfileReferenceVersionRepeatedEditAndImmutableSnapshots(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "shared")
	for _, wrongID := range []bool{false, true} {
		request := f.request(api.ProfilePut{}, 0)
		if wrongID {
			request.ProfileID++
		} else {
			request.Generation++
		}

		_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, request, nil)
		require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	}

	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, profile, err := tx.GetProfile(ctx, "default", "tracked")
		require.NoError(t, err)
		require.Equal(t, "old", profile.Devices["eth0"]["network"])
		var count, generation int
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM profiles_reference_changes`).Scan(&count))
		require.Zero(t, count)
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT reference_generation FROM profiles WHERE id=?`, f.profileID).Scan(&generation))
		require.Zero(t, generation)
		return nil
	})
	request := f.request(api.ProfilePut{Config: map[string]string{"user.empty": ""}, Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "new", "security.acls": "missing-acl"}}}, 0)
	_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, request, nil)
	require.Error(t, err)
	request.Profile.Devices["eth0"]["security.acls"] = ""
	first, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, request, nil)
	require.NoError(t, err)
	request.Generation = 1
	noop, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, request, nil)
	require.NoError(t, err)
	require.Equal(t, first.Token, noop.Token)
	// Neither the request nor a returned snapshot can rewrite the persisted immutable target.
	request.Profile.Devices["eth0"]["network"] = "mutated-return"
	first.Consumers[0].After.Profiles[0].Profile.Devices["eth0"]["network"] = "mutated-return"
	first.Consumers[0].Before.ExpandedDevices["eth0"]["network"] = "mutated-return"
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		consumers, err := tx.ProfileReferenceConsumers(ctx, first.Token)
		require.NoError(t, err)
		require.Equal(t, "old", consumers[0].Before.ExpandedDevices["eth0"]["network"])
		require.Equal(t, "new", consumers[0].After.Profiles[0].Profile.Devices["eth0"]["network"])
		require.Empty(t, consumers[0].After.Profiles[0].Profile.Config)
		return nil
	})
	second, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 1), nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), second.Generation)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		state, err := tx.ProfileReferenceState(ctx, f.instances[0])
		require.NoError(t, err)
		require.Equal(t, "old", state.Applied.ExpandedDevices["eth0"]["network"])
		require.Empty(t, state.Desired.ExpandedDevices)
		require.Equal(t, int64(2), state.DesiredSequence)
		return nil
	})
	f.complete(t, f.identity(t, second.Token, f.instances[0]))
}

func TestProfileReferenceWholeDeviceOverridesAndStaleInputs(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprintf("local-%t", local), func(t *testing.T) {
			f := newProfileReferenceFixture(t, 1, "default")
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}})
				require.NoError(t, err)
				if local {
					require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instances[0], devices))
				} else {
					id, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "override"})
					require.NoError(t, err)
					require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), id, devices))
					require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), "default", []string{"tracked", "override"}))
				}

				_, err = tx.Tx().ExecContext(ctx, `DELETE FROM instances_profile_reference_apply`)
				require.NoError(t, err)
				snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, f.instances[0])
				require.NoError(t, err)
				require.Len(t, snapshot.ExpandedDevices["eth0"], 2)
				require.Empty(t, snapshot.ExpandedDevices["eth0"]["security.acls"])
				require.Empty(t, snapshot.ExpandedDevices["eth0"]["mtu"])
				return tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1)
			})
			commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
			require.NoError(t, err)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				usage, err := tx.ProfileReferenceUsage(ctx, f.oldNetwork, 0)
				require.NoError(t, err)
				require.Empty(t, usage)
				return nil
			})
			identity := f.identity(t, commit.Token, f.instances[0])
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				if local {
					return cluster.UpdateInstanceConfig(ctx, tx.Tx(), f.instances[0], map[string]string{"user.local": "changed"})
				}

				return cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), "default", []string{"override", "tracked"})
			})
			err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return project.ClaimProfileReferenceApply(ctx, tx, identity)
			})
			require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
		})
	}
}

func TestProfileReferenceOrdinaryWriterAndSharedWriterBothOrders(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "default")
	ordinary := f.identity(t, "", f.instances[0])
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.ClaimProfileReferenceApply(ctx, tx, ordinary)
	})
	_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcquireOVNPeerOperation(ctx, "shared", "acl-config", false)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.FailProfileReferenceApply(ctx, ordinary, db.ProfileReferenceBeforeDispatch)
	})
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcquireOVNPeerOperation(ctx, "shared", "acl-config", false)
	})
	ordinary = f.identity(t, "", f.instances[0])
	err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.ClaimProfileReferenceApply(ctx, tx, ordinary)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	_, err = project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.ReleaseOVNNetworkOperation(ctx, "default", db.OVNPeerOperationName, "shared")
	})
	commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
	require.NoError(t, err)
	ordinary = f.identity(t, "", f.instances[0])
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, ordinary))
		return tx.FailProfileReferenceApply(ctx, ordinary, db.ProfileReferenceBeforeDispatch)
	})
	f.complete(t, f.identity(t, commit.Token, f.instances[0]))
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		usage, err := tx.ProfileReferenceUsage(ctx, 0, 0)
		require.NoError(t, err)
		require.Empty(t, usage)
		return nil
	})
}

// profileProjectState captures complete persisted rows, including empty child and receipt sets.
func profileProjectState(t *testing.T, f *profileReferenceFixture) map[string][][]any {
	t.Helper()
	result := map[string][][]any{}
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, table := range []string{"projects", "projects_config", "profiles", "profiles_config", "profiles_devices", "profiles_devices_config", "instances_profile_reference_apply", "profiles_reference_changes", "profiles_reference_consumers", "profiles_reference_attempts", "profiles_reference_usage", "profiles_reference_children", "profiles_reference_receipts"} {
			rows, err := tx.Tx().QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY rowid")
			require.NoError(t, err)
			columns, err := rows.Columns()
			require.NoError(t, err)
			result[table] = [][]any{}
			for rows.Next() {
				values := make([]any, len(columns))
				targets := make([]any, len(columns))
				for i := range values {
					targets[i] = &values[i]
				}

				require.NoError(t, rows.Scan(targets...))
				for i, value := range values {
					bytes, ok := value.([]byte)
					if ok {
						values[i] = string(bytes)
					}
				}
				result[table] = append(result[table], values)
			}

			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
		}

		return nil
	})
	return result
}

func profileProjectGet(t *testing.T, f *profileReferenceFixture, name string) api.ProjectPut {
	t.Helper()
	var result api.ProjectPut
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		record, err := cluster.GetProject(ctx, tx.Tx(), name)
		require.NoError(t, err)
		value, err := record.ToAPI(ctx, tx.Tx())
		require.NoError(t, err)
		result = value.ProjectPut
		return nil
	})
	return result
}

func profileProjectUpdate(f *profileReferenceFixture, name string, request api.ProjectPut, changed []string) error {
	return f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := project.AllowProjectUpdate(tx, name, request.Config, changed)
		if err != nil {
			return err
		}

		return cluster.UpdateProject(ctx, tx.Tx(), name, request)
	})
}

func profileProjectBlocked(t *testing.T, f *profileReferenceFixture, name string, request api.ProjectPut, changed []string) {
	t.Helper()
	before := profileProjectState(t, f)
	err := profileProjectUpdate(f, name, request, changed)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "Expected guard conflict, got %v", err)
	require.ErrorContains(t, err, "unresolved profile reference work")
	require.Equal(t, before, profileProjectState(t, f))
}

func profileProjectCreate(t *testing.T, f *profileReferenceFixture, name string) int64 {
	t.Helper()
	var id int64
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		id, err = cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: name})
		return err
	})
	return id
}

func profileProjectEmptyFixture(t *testing.T) *profileReferenceFixture {
	t.Helper()
	f := newProfileReferenceFixture(t, 1, "inherited")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), f.profileID, nil))
		// This replaces only initial fixture state, before any transition or attempt exists.
		_, err := tx.Tx().ExecContext(ctx, `DELETE FROM instances_profile_reference_apply`)
		require.NoError(t, err)
		snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, f.instances[0])
		require.NoError(t, err)
		require.Empty(t, snapshot.Resources)
		return tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1)
	})
	return f
}

func TestProfileReferenceProjectUpdateSensitiveInputs(t *testing.T) {
	for _, test := range []struct {
		name    string
		mode    string
		key     string
		value   string
		remove  bool
		changed []string
	}{
		{name: "profiles-omitted-keys", key: "features.profiles", value: "true"},
		{name: "default-owned-instance", mode: "default", key: "restricted.networks.access", value: "old"},
		{name: "networks-empty-keys", key: "features.networks", value: "true", changed: []string{}},
		{name: "restricted", key: "restricted", value: "true", changed: []string{"restricted"}},
		{name: "network-access", key: "restricted.networks.access", value: "old", changed: []string{"restricted.networks.access"}},
		{name: "key-removal", key: "features.networks", remove: true},
		{name: "equivalent-boolean", key: "features.networks", value: "0"},
		{name: "equivalent-list-order", mode: "shared", key: "restricted.networks.access", value: "new,old"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mode := test.mode
			if mode == "" {
				mode = "inherited"
			}

			f := newProfileReferenceFixture(t, 1, mode)
			commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
			require.NoError(t, err)
			require.Len(t, commit.Consumers, 1)
			require.False(t, commit.Consumers[0].Satisfied)
			request := profileProjectGet(t, f, f.project)
			if test.remove {
				delete(request.Config, test.key)
			} else {
				request.Config[test.key] = test.value
			}

			profileProjectBlocked(t, f, f.project, request, test.changed)
		})
	}
}

func TestProfileReferenceProjectUpdateStaleChangedKeys(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "inherited")
	earlier := profileProjectGet(t, f, f.project)
	// The API snapshot predates the current project value and the pending consumer.
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		current := profileProjectGetInTx(t, ctx, tx, f.project)
		current.Config["restricted.networks.access"] = "old,new"
		require.NoError(t, cluster.UpdateProject(ctx, tx.Tx(), f.project, current))
		_, err := tx.Tx().ExecContext(ctx, `DELETE FROM instances_profile_reference_apply`)
		require.NoError(t, err)
		snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, f.instances[0])
		require.NoError(t, err)
		return tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1)
	})
	_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
	require.NoError(t, err)
	earlier.Config["user.note"] = "stale request"
	profileProjectBlocked(t, f, f.project, earlier, []string{"user.note"})
}

func profileProjectGetInTx(t *testing.T, ctx context.Context, tx *db.ClusterTx, name string) api.ProjectPut {
	t.Helper()
	record, err := cluster.GetProject(ctx, tx.Tx(), name)
	require.NoError(t, err)
	value, err := record.ToAPI(ctx, tx.Tx())
	require.NoError(t, err)
	return value.ProjectPut
}

func TestProfileReferenceProjectUpdateConsumerOwnership(t *testing.T) {
	for _, mode := range []string{"inherited", "shared", "empty"} {
		t.Run(mode, func(t *testing.T) {
			var f *profileReferenceFixture
			if mode == "empty" {
				f = profileProjectEmptyFixture(t)
			} else {
				f = newProfileReferenceFixture(t, 1, mode)
			}

			profileProjectCreate(t, f, "unrelated")
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "unrelated", Name: "tracked"})
				require.NoError(t, err)
				_, err = tx.CreateNetwork(ctx, "unrelated", "old", "", db.NetworkTypeBridge, nil)
				return err
			})
			commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Config: map[string]string{"user.test": "updated"}}, 0), nil)
			require.NoError(t, err)
			require.Len(t, commit.Consumers, 1)
			if mode == "empty" {
				require.Empty(t, commit.Consumers[0].Before.Resources)
				require.Empty(t, commit.Consumers[0].After.Resources)
			} else {
				require.NotEqual(t, commit.Consumers[0].ProjectID, commit.Consumers[0].Before.Resources[0].NetworkProjectID)
			}

			request := profileProjectGet(t, f, f.project)
			request.Config["features.profiles"] = "true"
			profileProjectBlocked(t, f, f.project, request, nil)
			for _, name := range []string{"default", "unrelated"} {
				request = profileProjectGet(t, f, name)
				request.Config["restricted.networks.access"] = "old,new"
				require.NoError(t, profileProjectUpdate(f, name, request, nil))
				require.Equal(t, request, profileProjectGet(t, f, name))
			}
		})
	}
}

func TestProfileReferenceProjectUpdateOrdinaryAttempts(t *testing.T) {
	for _, phase := range []string{"applying", "applied", "recovery", "retryable"} {
		t.Run(phase, func(t *testing.T) {
			f := newProfileReferenceFixture(t, 1, "inherited")
			identity := f.identity(t, "", f.instances[0])
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
				switch phase {
				case "applied":
					require.NoError(t, tx.SealProfileReferenceChildren(ctx, identity, nil))
					require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
				case "recovery":
					require.NoError(t, tx.FailProfileReferenceApply(ctx, identity, db.ProfileReferenceUnknown))
				case "retryable":
					require.NoError(t, tx.FailProfileReferenceApply(ctx, identity, db.ProfileReferenceBeforeDispatch))
				}

				var actual string
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT phase FROM profiles_reference_attempts WHERE token=?`, identity.Token).Scan(&actual))
				require.Equal(t, phase, actual)
				var consumers int
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM profiles_reference_consumers`).Scan(&consumers))
				require.Zero(t, consumers)
				if phase == "retryable" {
					state, err := tx.ProfileReferenceState(ctx, identity.InstanceID)
					require.NoError(t, err)
					require.Empty(t, state.ActiveToken)
					usage, err := tx.ProfileReferenceUsage(ctx, 0, 0)
					require.NoError(t, err)
					require.NotEmpty(t, usage)
				}

				return nil
			})
			request := profileProjectGet(t, f, f.project)
			request.Config["features.networks"] = "true"
			profileProjectBlocked(t, f, f.project, request, nil)
		})
	}
}

// Deliberate inconsistent DB fixtures exercise each ownership predicate without simulating placement.
func TestProfileReferenceProjectUpdateEvidenceOwners(t *testing.T) {
	for _, test := range []struct {
		name    string
		table   string
		phase   string
		pending bool
	}{
		{name: "unsatisfied-consumer", table: "profiles_reference_consumers", pending: true},
		{name: "unapplied-sequence", table: "instances_profile_reference_apply", pending: true},
		{name: "active-token", table: "instances_profile_reference_apply", phase: "applied"},
		{name: "applying-attempt", table: "profiles_reference_attempts", phase: "applying"},
		{name: "applied-attempt", table: "profiles_reference_attempts", phase: "applied"},
		{name: "recovery-attempt", table: "profiles_reference_attempts", phase: "recovery"},
		{name: "retained-usage", table: "profiles_reference_usage", phase: "retryable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var f *profileReferenceFixture
			if test.table == "profiles_reference_usage" {
				f = newProfileReferenceFixture(t, 1, "inherited")
			} else {
				f = profileProjectEmptyFixture(t)
			}

			storedID := profileProjectCreate(t, f, "stored-owner")
			currentID := profileProjectCreate(t, f, "current-owner")
			profileProjectCreate(t, f, "unrelated")
			if test.pending {
				commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Config: map[string]string{"user.test": "new"}}, 0), nil)
				require.NoError(t, err)
				require.Empty(t, commit.Consumers[0].Before.Resources)
				require.Empty(t, commit.Consumers[0].After.Resources)
			} else {
				identity := f.identity(t, "", f.instances[0])
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
					switch test.phase {
					case "applied":
						require.NoError(t, tx.SealProfileReferenceChildren(ctx, identity, nil))
						require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
					case "recovery":
						require.NoError(t, tx.FailProfileReferenceApply(ctx, identity, db.ProfileReferenceUnknown))
					case "retryable":
						require.NoError(t, tx.FailProfileReferenceApply(ctx, identity, db.ProfileReferenceBeforeDispatch))
					}

					return nil
				})
			}

			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				// Only this predicate's stored owner differs, so other sources cannot protect it.
				result, err := tx.Tx().ExecContext(ctx, "UPDATE "+test.table+" SET project_id=?, member_id=member_id+1000, placement_revision=placement_revision+10 WHERE instance_id=?", storedID, f.instances[0])
				require.NoError(t, err)
				count, err := result.RowsAffected()
				require.NoError(t, err)
				require.Positive(t, count)
				return nil
			})
			request := profileProjectGet(t, f, "stored-owner")
			request.Config["restricted.networks.access"] = "old"
			profileProjectBlocked(t, f, "stored-owner", request, nil)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.Tx().ExecContext(ctx, `UPDATE instances SET project_id=? WHERE id=?`, currentID, f.instances[0])
				return err
			})
			for _, name := range []string{"stored-owner", "current-owner"} {
				request = profileProjectGet(t, f, name)
				request.Config["restricted.networks.access"] = "old"
				profileProjectBlocked(t, f, name, request, nil)
			}

			request = profileProjectGet(t, f, "unrelated")
			request.Config["restricted.networks.access"] = "old"
			require.NoError(t, profileProjectUpdate(f, "unrelated", request, nil))
		})
	}
}

func TestProfileReferenceProjectUpdateSettledHistory(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "inherited")
	commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
	require.NoError(t, err)
	failed := f.identity(t, commit.Token, f.instances[0])
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, failed))
		return tx.FailProfileReferenceApply(ctx, failed, db.ProfileReferenceBeforeDispatch)
	})
	request := profileProjectGet(t, f, f.project)
	request.Config["features.networks"] = "true"
	profileProjectBlocked(t, f, f.project, request, nil)
	newer, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Description: "newer desired profile"}, 1), nil)
	require.NoError(t, err)
	completed := f.identity(t, newer.Token, f.instances[0])
	require.Greater(t, completed.Sequence, failed.Sequence)
	f.complete(t, completed)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		consumers, err := tx.ProfileReferenceConsumers(ctx, commit.Token)
		require.NoError(t, err)
		require.True(t, consumers[0].Satisfied)
		newerConsumers, err := tx.ProfileReferenceConsumers(ctx, newer.Token)
		require.NoError(t, err)
		require.True(t, newerConsumers[0].Satisfied)
		usage, err := tx.ProfileReferenceUsage(ctx, 0, 0)
		require.NoError(t, err)
		require.Empty(t, usage)
		state, err := tx.ProfileReferenceState(ctx, f.instances[0])
		require.NoError(t, err)
		require.Equal(t, state.AppliedSequence, state.DesiredSequence)
		require.Empty(t, state.ActiveToken)
		for token, expected := range map[string]string{failed.Token: "retryable", completed.Token: "completed"} {
			var phase string
			require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT phase FROM profiles_reference_attempts WHERE token=?`, token).Scan(&phase))
			require.Equal(t, expected, phase)
		}

		var receipt bool
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM profiles_reference_receipts WHERE attempt_token=?)`, completed.Token).Scan(&receipt))
		require.True(t, receipt)
		return nil
	})
	require.NoError(t, profileProjectUpdate(f, f.project, request, []string{"features.networks"}))
	require.Equal(t, request, profileProjectGet(t, f, f.project))
}

func TestProfileReferenceProjectUpdatePermittedAndNormalValidation(t *testing.T) {
	t.Run("no-durable-state", func(t *testing.T) {
		f := newProfileReferenceFixture(t, 0, "inherited")
		request := profileProjectGet(t, f, f.project)
		request.Config["features.profiles"] = "true"
		require.NoError(t, profileProjectUpdate(f, f.project, request, nil))
		require.Equal(t, request, profileProjectGet(t, f, f.project))
	})
	t.Run("settled-baseline", func(t *testing.T) {
		f := newProfileReferenceFixture(t, 1, "inherited")
		request := profileProjectGet(t, f, f.project)
		request.Config["features.networks"] = "true"
		require.NoError(t, profileProjectUpdate(f, f.project, request, nil))
		require.Equal(t, request, profileProjectGet(t, f, f.project))
	})
	t.Run("pending-metadata-and-unchanged-values", func(t *testing.T) {
		f := newProfileReferenceFixture(t, 1, "inherited")
		_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
		require.NoError(t, err)
		request := profileProjectGet(t, f, f.project)
		require.NoError(t, profileProjectUpdate(f, f.project, request, []string{"features.profiles", "features.networks", "restricted"}))
		request.Description = "metadata edit"
		request.Config["user.note"] = "allowed"
		request.Config["restricted"] = ""
		require.NoError(t, profileProjectUpdate(f, f.project, request, []string{"user.note"}))
		delete(request.Config, "restricted")
		require.Equal(t, request, profileProjectGet(t, f, f.project))
		// Successful admission does not assert that a later claim tolerates metadata drift.
		before := profileProjectState(t, f)
		request.Config["limits.instances"] = "0"
		err = profileProjectUpdate(f, f.project, request, []string{"limits.instances"})
		require.ErrorContains(t, err, "limits.instances")
		require.Equal(t, before, profileProjectState(t, f))
	})
}

func TestProfileReferenceProjectUpdateReadError(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "inherited")
	request := profileProjectGet(t, f, f.project)
	request.Config["features.profiles"] = "true"
	before := profileProjectState(t, f)
	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, tx.Tx().Rollback())
		return project.AllowProjectUpdate(tx, f.project, request.Config, nil)
	})
	require.ErrorIs(t, err, sql.ErrTxDone)
	require.Equal(t, before, profileProjectState(t, f))
}

// profileMetadataEdit uses the same admission/write transaction as the project API.
func profileMetadataEdit(t *testing.T, f *profileReferenceFixture, operation string) api.ProjectPut {
	t.Helper()
	before := profileProjectState(t, f)
	request := profileProjectGet(t, f, f.project)
	request.Description = "project metadata " + operation
	switch operation {
	case "add":
		require.NotContains(t, request.Config, "user.note")
		request.Config["user.note"] = "added"
	case "update":
		require.Contains(t, request.Config, "user.note")
		request.Config["user.note"] = "updated"
	case "remove":
		require.Contains(t, request.Config, "user.note")
		delete(request.Config, "user.note")
	default:
		t.Fatalf("Unknown metadata operation %q", operation)
	}

	require.NoError(t, profileProjectUpdate(f, f.project, request, []string{"user.note"}))
	require.Equal(t, request, profileProjectGet(t, f, f.project))
	after := profileProjectState(t, f)
	for table, rows := range before {
		if table != "projects" && table != "projects_config" {
			require.Equal(t, rows, after[table], "metadata update rewrote %s", table)
		}
	}
	return request
}

func profileMetadataReadState(t *testing.T, f *profileReferenceFixture) *db.ProfileReferenceState {
	t.Helper()
	var state *db.ProfileReferenceState
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		state, err = tx.ProfileReferenceState(ctx, f.instances[0])
		return err
	})
	return state
}

func profileMetadataAttemptSnapshots(t *testing.T, f *profileReferenceFixture, identity db.ProfileReferenceApply) (string, string) {
	t.Helper()
	var baseline, target string
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.Tx().QueryRowContext(ctx, `SELECT baseline_snapshot, target_snapshot FROM profiles_reference_attempts WHERE token=?`, identity.Token).Scan(&baseline, &target)
	})
	return baseline, target
}

func profileMetadataJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

// This models only the existing DB acknowledgment contract, with no device/backend effects.
func profileMetadataClaimAndComplete(t *testing.T, f *profileReferenceFixture, identity db.ProfileReferenceApply) {
	t.Helper()
	before := profileMetadataReadState(t, f)
	rowsBefore := profileProjectState(t, f)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.ClaimProfileReferenceApply(ctx, tx, identity)
	})
	baseline, target := profileMetadataAttemptSnapshots(t, f, identity)
	require.Equal(t, profileMetadataJSON(t, before.Applied), baseline)
	require.Equal(t, profileMetadataJSON(t, before.Desired), target)
	claimed := profileMetadataReadState(t, f)
	require.Equal(t, before.Applied, claimed.Applied)
	require.Equal(t, before.Desired, claimed.Desired)
	require.Equal(t, before.DesiredSequence, claimed.DesiredSequence)
	require.Equal(t, identity.Token, claimed.ActiveToken)
	rowsAfter := profileProjectState(t, f)
	// Claim creates an attempt/usage and active ownership, never rewrites old consumers.
	for _, table := range []string{"profiles_reference_changes", "profiles_reference_consumers"} {
		require.Equal(t, rowsBefore[table], rowsAfter[table])
	}

	require.Equal(t, rowsBefore["profiles_reference_attempts"], rowsAfter["profiles_reference_attempts"][:len(rowsBefore["profiles_reference_attempts"])], "claim rewrote earlier attempt JSON")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, tx.SealProfileReferenceChildren(ctx, identity, nil))
		require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
		return tx.FinalizeProfileReferenceApply(ctx, identity)
	})
	settled := profileMetadataReadState(t, f)
	require.Equal(t, before.Desired, settled.Applied)
	require.Equal(t, before.Desired, settled.Desired)
	require.Equal(t, identity.Sequence, settled.AppliedSequence)
	require.Equal(t, identity.Sequence, settled.DesiredSequence)
	require.Empty(t, settled.ActiveToken)
	afterBaseline, afterTarget := profileMetadataAttemptSnapshots(t, f, identity)
	require.Equal(t, baseline, afterBaseline)
	require.Equal(t, target, afterTarget)
}

func TestProfileReferenceProjectMetadataPendingClaimProgression(t *testing.T) {
	for _, mode := range []string{"default", "shared"} {
		t.Run(mode, func(t *testing.T) {
			f := newProfileReferenceFixture(t, 1, mode)
			for i, operation := range []string{"add", "update", "remove"} {
				previous := profileMetadataReadState(t, f)
				previousRows := profileProjectState(t, f)
				commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Description: fmt.Sprintf("profile edit %d", i), Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}}}, int64(i)), nil)
				require.NoError(t, err)
				committedRows := profileProjectState(t, f)
				require.Equal(t, previousRows["profiles_reference_attempts"], committedRows["profiles_reference_attempts"], "commit rewrote earlier attempt JSON")
				require.Len(t, commit.Consumers, 1)
				consumer := commit.Consumers[0]
				if mode == "shared" {
					require.NotEqual(t, consumer.ProjectID, consumer.After.Resources[0].NetworkProjectID)
				}

				require.Equal(t, previous.Applied, profileMetadataReadState(t, f).Applied)
				metadata := profileMetadataEdit(t, f, operation)
				pending := profileMetadataReadState(t, f)
				require.Equal(t, consumer.After, pending.Desired)
				require.NotEqual(t, metadata, pending.Desired.Project.ProjectPut)
				profileMetadataClaimAndComplete(t, f, f.identity(t, commit.Token, f.instances[0]))
				require.Equal(t, metadata, profileProjectGet(t, f, f.project))
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					consumers, err := tx.ProfileReferenceConsumers(ctx, commit.Token)
					consumer.Satisfied = true
					require.Equal(t, []db.ProfileReferenceConsumer{consumer}, consumers)
					return err
				})
			}
		})
	}
}

func TestProfileReferenceProjectMetadataBeforeAndBetweenCommits(t *testing.T) {
	for _, mode := range []string{"default", "shared"} {
		t.Run(mode, func(t *testing.T) {
			f := newProfileReferenceFixture(t, 1, mode)
			initial := profileMetadataReadState(t, f)
			firstMetadata := profileMetadataEdit(t, f, "add")
			first, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Description: "first pending edit"}, 0), nil)
			require.NoError(t, err)
			require.Equal(t, firstMetadata, first.Consumers[0].Before.Project.ProjectPut)
			require.Equal(t, firstMetadata, first.Consumers[0].After.Project.ProjectPut)
			require.Equal(t, initial.Applied, profileMetadataReadState(t, f).Applied)
			firstRows := profileProjectState(t, f)["profiles_reference_consumers"]
			secondMetadata := profileMetadataEdit(t, f, "update")
			second, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Description: "second pending edit", Config: map[string]string{"user.profile": "real input"}}, 1), nil)
			require.NoError(t, err)
			require.Equal(t, secondMetadata, second.Consumers[0].Before.Project.ProjectPut)
			require.Equal(t, secondMetadata, second.Consumers[0].After.Project.ProjectPut)
			allRows := profileProjectState(t, f)["profiles_reference_consumers"]
			require.Equal(t, firstRows, allRows[:len(firstRows)], "new transition rewrote earlier snapshot JSON")
			pending := profileMetadataReadState(t, f)
			require.Equal(t, initial.Applied, pending.Applied)
			require.Equal(t, int64(2), pending.DesiredSequence)
			latestMetadata := profileMetadataEdit(t, f, "remove")
			profileMetadataClaimAndComplete(t, f, f.identity(t, second.Token, f.instances[0]))
			require.Equal(t, latestMetadata, profileProjectGet(t, f, f.project))
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				for _, commit := range []*project.ProfileReferenceCommit{first, second} {
					consumers, err := tx.ProfileReferenceConsumers(ctx, commit.Token)
					require.NoError(t, err)
					original := commit.Consumers[0]
					original.Satisfied = true
					require.Equal(t, []db.ProfileReferenceConsumer{original}, consumers)
				}

				return nil
			})
		})
	}
}

func TestProfileReferenceProjectMetadataOrdinaryTarget(t *testing.T) {
	for _, mode := range []string{"default", "shared"} {
		t.Run(mode, func(t *testing.T) {
			f := newProfileReferenceFixture(t, 1, mode)
			for _, operation := range []string{"add", "update", "remove"} {
				before := profileMetadataReadState(t, f)
				metadata := profileMetadataEdit(t, f, operation)
				identity := f.identity(t, "", f.instances[0])
				require.Equal(t, before.DesiredSequence+1, identity.Sequence)
				var fresh db.ProfileReferenceSnapshot
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					current, err := project.CaptureProfileReferenceSnapshot(ctx, tx, f.instances[0])
					require.NoError(t, err)
					fresh = *current
					return project.ClaimProfileReferenceApply(ctx, tx, identity)
				})
				baseline, target := profileMetadataAttemptSnapshots(t, f, identity)
				require.Equal(t, profileMetadataJSON(t, before.Applied), baseline)
				require.Equal(t, profileMetadataJSON(t, fresh), target)
				require.Equal(t, metadata, fresh.Project.ProjectPut)
				require.NotEqual(t, before.Desired.Project.ProjectPut, fresh.Project.ProjectPut)
				claimed := profileMetadataReadState(t, f)
				require.Equal(t, before.Applied, claimed.Applied)
				require.Equal(t, before.Desired, claimed.Desired)
				require.Equal(t, identity.Sequence, claimed.DesiredSequence)
				rows := profileProjectState(t, f)
				err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					return project.ClaimProfileReferenceApply(ctx, tx, identity)
				})
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
				require.Equal(t, rows, profileProjectState(t, f), "repeated claim advanced state")
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					require.NoError(t, tx.SealProfileReferenceChildren(ctx, identity, nil))
					require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
					return tx.FinalizeProfileReferenceApply(ctx, identity)
				})
				settled := profileMetadataReadState(t, f)
				require.Equal(t, fresh, settled.Applied)
				require.Equal(t, fresh, settled.Desired)
				require.Equal(t, identity.Sequence, settled.DesiredSequence)
				require.Equal(t, identity.Sequence, settled.AppliedSequence)
				afterBaseline, afterTarget := profileMetadataAttemptSnapshots(t, f, identity)
				require.Equal(t, baseline, afterBaseline)
				require.Equal(t, target, afterTarget)
			}
		})
	}
}

func TestProfileReferenceProjectMetadataStrictProductionInputs(t *testing.T) {
	for _, drift := range []string{"limits.instances", "restricted.containers.privilege", "features.images", "project-name", "local-user-config", "local-device"} {
		t.Run(drift, func(t *testing.T) {
			f := newProfileReferenceFixture(t, 1, "inherited")
			first, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Description: "first"}, 0), nil)
			require.NoError(t, err)
			profileMetadataEdit(t, f, "add")
			switch drift {
			case "limits.instances", "restricted.containers.privilege", "features.images":
				request := profileProjectGet(t, f, f.project)
				request.Config[drift] = map[string]string{"limits.instances": "10", "restricted.containers.privilege": "unprivileged", "features.images": "false"}[drift]
				require.NoError(t, profileProjectUpdate(f, f.project, request, []string{drift}))
			case "project-name":
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					return cluster.RenameProject(ctx, tx.Tx(), f.project, "renamed")
				})
				f.project = "renamed"
			case "local-user-config":
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					return cluster.UpdateInstanceConfig(ctx, tx.Tx(), f.instances[0], map[string]string{"user.local": "strict"})
				})
			case "local-device":
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					devices, err := cluster.APIToDevices(map[string]map[string]string{"disk": {"type": "disk", "path": "/data", "source": "/tmp"}})
					require.NoError(t, err)
					return cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instances[0], devices)
				})
			}

			before := profileProjectState(t, f)
			identity := f.identity(t, first.Token, f.instances[0])
			err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return project.ClaimProfileReferenceApply(ctx, tx, identity)
			})
			require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
			require.ErrorContains(t, err, "Instance desired inputs changed")
			require.Equal(t, before, profileProjectState(t, f))
			commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Description: "second"}, 1), nil)
			require.Nil(t, commit)
			require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
			require.ErrorContains(t, err, "Profile reference state or application ownership changed")
			require.Equal(t, before, profileProjectState(t, f), "failed capture/commit callback changed persisted rows")
		})
	}
}

func TestProfileReferenceProjectMetadataCallbackRollback(t *testing.T) {
	for _, boundary := range []string{"commit", "claim"} {
		t.Run(boundary, func(t *testing.T) {
			f := newProfileReferenceFixture(t, 1, "shared")
			first, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Description: "pending"}, 0), nil)
			require.NoError(t, err)
			profileMetadataEdit(t, f, "add")
			before := profileProjectState(t, f)
			identity := f.identity(t, first.Token, f.instances[0])
			rollback := errors.New("metadata regression callback rollback")
			err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				if boundary == "commit" {
					_, err := project.CaptureAndCommitProfileReferenceUpdate(ctx, tx, f.request(api.ProfilePut{Description: "next"}, 1))
					require.NoError(t, err)
				} else {
					require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
				}

				return rollback
			})
			require.ErrorIs(t, err, rollback)
			require.Equal(t, before, profileProjectState(t, f))
		})
	}
}

func TestProfileReferenceProjectMetadataStillValidatesCurrentNetwork(t *testing.T) {
	f := newProfileReferenceFixture(t, 1, "shared")
	commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}}}, 0), nil)
	require.NoError(t, err)
	profileMetadataEdit(t, f, "add")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error { return tx.NetworkDeleting("default", "new") })
	before := profileProjectState(t, f)
	identity := f.identity(t, commit.Token, f.instances[0])
	err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.ClaimProfileReferenceApply(ctx, tx, identity)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	require.NotContains(t, err.Error(), "Instance desired inputs changed")
	require.Equal(t, before, profileProjectState(t, f))
}

// ordinaryFixture initializes only fixture evidence, before any transition is captured.
func ordinaryFixture(t *testing.T, mode string, keepApplied bool) (*profileReferenceFixture, int64, int64) {
	t.Helper()
	f := newProfileReferenceFixture(t, 1, mode)
	var keep, selected int64
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		keep, err = tx.CreateNetwork(ctx, "default", "keep", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		_, err = tx.Tx().ExecContext(ctx, `UPDATE networks SET type=? WHERE id IN (?,?)`, db.NetworkTypeOVN, f.oldNetwork, f.newNetwork)
		require.NoError(t, err)
		if mode == "shared" {
			config := map[string]string{"features.profiles": "false", "features.networks": "true", "restricted": "true", "restricted.networks.access": "old,new,keep"}
			require.NoError(t, cluster.UpdateProject(ctx, tx.Tx(), f.project, api.ProjectPut{Config: config}))
		}

		selected, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "selected"})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileConfig(ctx, tx.Tx(), selected, map[string]string{"user.test": "selected"}))
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), selected, devices))
		if keepApplied {
			devices, err = cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "old-acl", "mtu": "1400"}, "keep": {"type": "nic", "network": "keep"}})
			require.NoError(t, err)
			require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), f.profileID, devices))
		}

		return ordinaryFixtureBaseline(ctx, tx, f.instances[0])
	})
	return f, keep, selected
}

func ordinaryFixtureBaseline(ctx context.Context, tx *db.ClusterTx, instanceID int64) error {
	_, err := tx.Tx().ExecContext(ctx, `DELETE FROM instances_profile_reference_apply WHERE instance_id=?`, instanceID)
	if err != nil {
		return err
	}

	snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, instanceID)
	if err != nil {
		return err
	}

	return tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1)
}

func ordinaryCapture(t *testing.T, f *profileReferenceFixture, names []string) db.OrdinaryProfileReferenceUpdate {
	t.Helper()
	var proposed *db.OrdinaryProfileReferenceUpdate
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		proposed, err = project.CaptureOrdinaryProfileReferenceInputs(ctx, tx, f.instances[0], names)
		return err
	})
	return *proposed
}

func ordinaryRows(t *testing.T, f *profileReferenceFixture) map[string][][]any {
	t.Helper()
	saved := profileProjectState(t, f)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, table := range []string{"instances", "instances_config", "instances_devices", "instances_devices_config", "instances_profiles", "networks_ovn_operations", "networks_ovn_notifications"} {
			rows, err := tx.Tx().QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY rowid")
			require.NoError(t, err)
			columns, err := rows.Columns()
			require.NoError(t, err)
			saved[table] = [][]any{}
			for rows.Next() {
				values := make([]any, len(columns))
				pointers := make([]any, len(columns))
				for i := range values {
					pointers[i] = &values[i]
				}

				require.NoError(t, rows.Scan(pointers...))
				for i, value := range values {
					bytes, ok := value.([]byte)
					if ok {
						values[i] = string(bytes)
					}
				}
				saved[table] = append(saved[table], values)
			}

			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
		}

		return nil
	})
	return saved
}

func ordinaryConflict(t *testing.T, f *profileReferenceFixture, identity db.ProfileReferenceApply, proposed db.OrdinaryProfileReferenceUpdate) {
	t.Helper()
	before := ordinaryRows(t, f)
	input := profileMetadataJSON(t, proposed)
	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.ClaimOrdinaryProfileReferenceApply(ctx, tx, identity, proposed)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "Expected input conflict, got %v", err)
	require.Equal(t, before, ordinaryRows(t, f), "rejected proposal changed rows")
	require.Equal(t, input, profileMetadataJSON(t, proposed), "claim mutated caller input")
}

func TestOrdinaryProfileReferenceComposition(t *testing.T) {
	for _, mode := range []string{"default", "inherited", "shared"} {
		for _, selection := range []string{"attach", "reverse", "detach", "detach-empty", "local", "remove"} {
			t.Run(mode+"/"+selection, func(t *testing.T) {
				f, _, selected := ordinaryFixture(t, mode, false)
				names := []string{"tracked", "selected"}
				switch selection {
				case "reverse":
					names = []string{"selected", "tracked"}
				case "detach", "remove":
					names = nil
				case "detach-empty":
					names = []string{}
				}

				proposed := ordinaryCapture(t, f, names)
				if len(names) == 0 {
					require.Empty(t, proposed.Profiles, "nil and explicit empty selections must both detach profiles")
				}

				require.Zero(t, proposed.Expected.DesiredSequence)
				require.Zero(t, proposed.Expected.AppliedSequence)
				require.Zero(t, proposed.Expected.InputRevision)
				require.Zero(t, proposed.Expected.ObservationWatermark)
				require.Equal(t, instancetype.Container, proposed.Fields.Type)
				proposed.Config["user.local"] = "override"
				proposed.Config["user.deleted"] = ""
				if selection == "local" || selection == "detach" || selection == "detach-empty" {
					proposed.Devices["eth0"] = map[string]string{"type": "nic", "network": "new", "security.acls": "", "mtu": ""}
				}

				if selection == "remove" {
					proposed.Config = nil
					proposed.Devices = nil
					proposed.Profiles = nil
				}

				caller := profileMetadataJSON(t, proposed)
				expectedJSON := profileMetadataJSON(t, proposed.Expected)
				identity := f.identity(t, "", f.instances[0])
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					return project.ClaimOrdinaryProfileReferenceApply(ctx, tx, identity, proposed)
				})
				require.Equal(t, caller, profileMetadataJSON(t, proposed))
				require.Equal(t, expectedJSON, profileMetadataJSON(t, proposed.Expected))
				baselineJSON, targetJSON := profileMetadataAttemptSnapshots(t, f, identity)
				require.Equal(t, profileMetadataJSON(t, proposed.Expected.Current), baselineJSON)
				var target db.ProfileReferenceSnapshot
				require.NoError(t, json.Unmarshal([]byte(targetJSON), &target))
				require.Len(t, target.Profiles, len(names))
				for i, profile := range target.Profiles {
					require.Equal(t, names[i], profile.Profile.Name)
				}

				if len(names) == 0 {
					require.Empty(t, target.Profiles, "persisted target must retain explicit zero-profile selection")
				}

				require.NotNil(t, target.LocalConfig)
				require.NotNil(t, target.LocalDevices)
				require.NotNil(t, target.Profiles)
				require.NotNil(t, target.Resources)
				require.NotContains(t, target.LocalConfig, "user.deleted")
				if selection == "remove" {
					require.Empty(t, target.ExpandedDevices)
					require.Empty(t, target.ExpandedConfig)
				} else {
					network := f.newNetwork
					if selection == "reverse" {
						network = f.oldNetwork
						require.Equal(t, "old", target.ExpandedConfig["user.test"])
						require.Equal(t, "1400", target.ExpandedDevices["eth0"]["mtu"])
						require.Equal(t, f.acl, target.Resources[1].ACLID)
					} else {
						require.Len(t, target.Resources, 1, "no-ACL NIC still retains its network")
						require.NotContains(t, target.ExpandedDevices["eth0"], "mtu")
						require.NotContains(t, target.ExpandedDevices["eth0"], "security.acls")
					}

					require.Equal(t, network, target.Resources[0].NetworkID)
					require.Equal(t, "default", target.Resources[0].NetworkProject)
					require.Equal(t, "ovn", target.Resources[0].NetworkType)
					if mode != "default" {
						require.NotEqual(t, target.ProjectID, target.Resources[0].NetworkProjectID)
					}

					if selection == "attach" {
						require.Equal(t, selected, target.Profiles[1].ID)
						require.Equal(t, "selected", target.ExpandedConfig["user.test"])
					}

					require.Equal(t, "override", target.ExpandedConfig["user.local"])
				}

				ordinaryConflict(t, f, identity, proposed)
			})
		}
	}
}

func TestOrdinaryProfileReferencePendingUnionAndSeparateReservation(t *testing.T) {
	for _, mode := range []string{"default", "shared"} {
		for _, keepApplied := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/keep-applied-%t", mode, keepApplied), func(t *testing.T) {
				f, keep, _ := ordinaryFixture(t, mode, keepApplied)
				earlier := f.identity(t, "", f.instances[0])
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, earlier))
					return tx.FailProfileReferenceApply(ctx, earlier, db.ProfileReferenceBeforeDispatch)
				})
				oldAttempt := profileProjectState(t, f)["profiles_reference_attempts"]
				original := profileMetadataReadState(t, f)
				devices := map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "old-acl"}, "keep": {"type": "nic", "network": "keep"}}
				for i := 0; i < 2; i++ {
					f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
						_, err := project.CaptureAndCommitProfileReferenceUpdate(ctx, tx, f.request(api.ProfilePut{Description: fmt.Sprintf("pending-%d", i), Devices: devices}, int64(i)))
						return err
					})
				}

				pending := profileMetadataReadState(t, f)
				require.Equal(t, original.Applied, pending.Applied)
				if !keepApplied {
					require.NotContains(t, pending.Applied.ExpandedDevices, "keep")
				}

				require.Equal(t, "keep", pending.Desired.ExpandedDevices["keep"]["network"])
				proposed := ordinaryCapture(t, f, []string{"tracked"})
				proposed.Devices["eth0"] = map[string]string{"type": "nic", "network": "new"}
				proposed.Config["user.local"] = "ordinary"
				saved := ordinaryRows(t, f)
				metadata := profileMetadataEdit(t, f, "add")
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error { return tx.NetworkDeleting("default", "old") })
				identity := f.identity(t, "", f.instances[0])
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					return project.ClaimOrdinaryProfileReferenceApply(ctx, tx, identity, proposed)
				})
				claimed := profileMetadataReadState(t, f)
				require.Equal(t, pending.DesiredSequence+1, claimed.DesiredSequence)
				require.Equal(t, pending.Desired, claimed.Desired)
				require.Equal(t, pending.Applied, claimed.Applied)
				baselineJSON, targetJSON := profileMetadataAttemptSnapshots(t, f, identity)
				require.Equal(t, profileMetadataJSON(t, pending.Applied), baselineJSON)
				after := ordinaryRows(t, f)
				require.Equal(t, oldAttempt, after["profiles_reference_attempts"][:len(oldAttempt)])
				require.Equal(t, saved["profiles_reference_consumers"], after["profiles_reference_consumers"])
				require.Equal(t, saved["profiles_reference_usage"], after["profiles_reference_usage"][:len(saved["profiles_reference_usage"])])
				// Reserve owns its transaction, after the claim callback has committed.
				children, err := f.c.ReserveProfileReferenceChildren(f.ctx, identity)
				require.NoError(t, err)
				require.Len(t, children, 3, "reserve baseline N + proposed M + pending-only unchanged U")
				found := map[int64]bool{}
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					for _, child := range children {
						found[child.NetworkID] = true
						require.Equal(t, "default", child.ProjectName)
						require.NotEmpty(t, child.Token)
						var stored bool
						require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM profiles_reference_children WHERE attempt_token=? AND network_id=? AND project_id=? AND name=? AND token=?)`, identity.Token, child.NetworkID, child.ProjectID, child.Name, child.Token).Scan(&stored))
						require.True(t, stored)
						require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM networks_ovn_operations WHERE project_id=? AND name=? AND token=? AND operation='nic')`, child.ProjectID, child.Name, child.Token).Scan(&stored))
						require.True(t, stored)
					}

					var sealed bool
					require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT children_sealed FROM profiles_reference_attempts WHERE token=?`, identity.Token).Scan(&sealed))
					require.True(t, sealed)
					return nil
				})
				require.Equal(t, map[int64]bool{f.oldNetwork: true, f.newNetwork: true, keep: true}, found)
				var target db.ProfileReferenceSnapshot
				require.NoError(t, json.Unmarshal([]byte(targetJSON), &target))
				require.Equal(t, metadata, target.Project.ProjectPut)
				require.Equal(t, proposed.Profiles, target.Profiles)
				require.Equal(t, proposed.Config, target.LocalConfig)
				require.Equal(t, proposed.Devices, target.LocalDevices)
				require.Equal(t, map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}, "keep": {"type": "nic", "network": "keep"}}, target.ExpandedDevices)
				require.Len(t, target.Resources, 2)
				require.Equal(t, f.newNetwork, target.Resources[0].NetworkID)
				require.Equal(t, keep, target.Resources[1].NetworkID, "pending Desired U must remain in ordinary target")
				final := ordinaryRows(t, f)
				require.Equal(t, after["profiles_reference_usage"], final["profiles_reference_usage"])
				require.Equal(t, after["profiles_reference_consumers"], final["profiles_reference_consumers"])
				b, a := profileMetadataAttemptSnapshots(t, f, identity)
				require.Equal(t, baselineJSON, b)
				require.Equal(t, targetJSON, a)
			})
		}
	}
}

func TestOrdinaryProfileReferenceExpectedAndSelectedChecks(t *testing.T) {
	for _, boundary := range []string{"expected-current", "selected-generation", "selected-payload", "selected-id", "selected-project", "duplicate-id", "duplicate-name", "expected-version", "current-version", "input-revision", "watermark", "expected-snapshot", "proposed-snapshot", "wrong-instance", "wrong-project", "wrong-member", "wrong-placement", "wrong-sequence", "change-token"} {
		t.Run(boundary, func(t *testing.T) {
			f, _, _ := ordinaryFixture(t, "default", false)
			proposed := ordinaryCapture(t, f, []string{"selected"})
			identity := f.identity(t, "", f.instances[0])
			switch boundary {
			case "expected-current":
				proposed.Expected.Current.LocalConfig["user.stale"] = "stale"
			case "selected-generation":
				proposed.Profiles[0].Generation++
			case "selected-payload":
				proposed.Profiles[0].Profile.Description = "stale"
			case "selected-id":
				proposed.Profiles[0].ID++
			case "selected-project":
				proposed.Profiles[0].Profile.Project = "tenant"
			case "duplicate-id":
				proposed.Profiles = append(proposed.Profiles, proposed.Profiles[0])
				proposed.Profiles[1].Profile.Name = "tracked"
			case "duplicate-name":
				proposed.Profiles = append(proposed.Profiles, proposed.Profiles[0])
				proposed.Profiles[1].ID++
			case "expected-version":
				proposed.Expected.Version = 2
			case "current-version":
				proposed.Expected.Current.Version = 0
			case "input-revision":
				proposed.Expected.InputRevision = 1
			case "watermark":
				proposed.Expected.ObservationWatermark = 1
			case "expected-snapshot":
				proposed.Expected.Fields.Snapshot = true
			case "proposed-snapshot":
				proposed.Fields.Snapshot = true
			case "wrong-instance":
				identity.InstanceID++
			case "wrong-project":
				identity.ProjectID++
			case "wrong-member":
				identity.MemberID++
			case "wrong-placement":
				identity.PlacementRevision++
			case "wrong-sequence":
				identity.Sequence++
			case "change-token":
				identity.ChangeToken = uuid.NewString()
			}

			ordinaryConflict(t, f, identity, proposed)
		})
	}
}

func TestOrdinaryProfileReferenceScalarsAndBaseImage(t *testing.T) {
	mutations := map[string]func(*db.OrdinaryProfileReferenceUpdate){
		"name":         func(p *db.OrdinaryProfileReferenceUpdate) { p.Fields.Name = "other" },
		"type":         func(p *db.OrdinaryProfileReferenceUpdate) { p.Fields.Type = instancetype.VM },
		"architecture": func(p *db.OrdinaryProfileReferenceUpdate) { p.Fields.Architecture++ },
		"description":  func(p *db.OrdinaryProfileReferenceUpdate) { p.Fields.Description = "other" },
		"ephemeral":    func(p *db.OrdinaryProfileReferenceUpdate) { p.Fields.Ephemeral = !p.Fields.Ephemeral },
		"stateful":     func(p *db.OrdinaryProfileReferenceUpdate) { p.Fields.Stateful = !p.Fields.Stateful },
		"creation": func(p *db.OrdinaryProfileReferenceUpdate) {
			p.Fields.CreationDate = p.Fields.CreationDate.Add(time.Nanosecond)
		},
		"last-used": func(p *db.OrdinaryProfileReferenceUpdate) {
			p.Fields.LastUsedDate = sql.NullTime{Time: time.Now(), Valid: true}
		},
		"expiry": func(p *db.OrdinaryProfileReferenceUpdate) {
			p.Fields.ExpiryDate = sql.NullTime{Time: time.Now(), Valid: true}
		},
		"last-valid-zero":   func(p *db.OrdinaryProfileReferenceUpdate) { p.Fields.LastUsedDate = sql.NullTime{Valid: true} },
		"expiry-valid-zero": func(p *db.OrdinaryProfileReferenceUpdate) { p.Fields.ExpiryDate = sql.NullTime{Valid: true} },
		"invalid-nonzero":   func(p *db.OrdinaryProfileReferenceUpdate) { p.Fields.LastUsedDate = sql.NullTime{Time: time.Now()} },
		"base-image":        func(p *db.OrdinaryProfileReferenceUpdate) { p.Fields.BaseImage = "other" },
		"base-alias-change": func(p *db.OrdinaryProfileReferenceUpdate) { p.Config["volatile.base_image"] = "other" },
		"base-alias-delete": func(p *db.OrdinaryProfileReferenceUpdate) { delete(p.Config, "volatile.base_image") },
		"base-alias-empty":  func(p *db.OrdinaryProfileReferenceUpdate) { p.Config["volatile.base_image"] = "" },
		"expected-scalar":   func(p *db.OrdinaryProfileReferenceUpdate) { p.Expected.Fields.Description = "stale" },
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f, _, _ := ordinaryFixture(t, "default", false)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				require.NoError(t, cluster.UpdateInstanceConfig(ctx, tx.Tx(), f.instances[0], map[string]string{"volatile.base_image": "original"}))
				_, err := tx.Tx().ExecContext(ctx, `UPDATE instances SET last_use_date=NULL, expiry_date=NULL WHERE id=?`, f.instances[0])
				require.NoError(t, err)
				return ordinaryFixtureBaseline(ctx, tx, f.instances[0])
			})
			proposed := ordinaryCapture(t, f, []string{"tracked"})
			require.Equal(t, "original", proposed.Fields.BaseImage)
			require.Equal(t, sql.NullTime{}, proposed.Fields.LastUsedDate)
			mutate(&proposed)
			ordinaryConflict(t, f, f.identity(t, "", f.instances[0]), proposed)
		})
	}
}

func TestOrdinaryProfileReferenceMapperNormalizationAndIsolation(t *testing.T) {
	for _, validZero := range []bool{false, true} {
		t.Run(fmt.Sprintf("valid-zero-%t", validZero), func(t *testing.T) {
			f, _, _ := ordinaryFixture(t, "default", false)
			instant := time.Date(2026, 10, 4, 1, 2, 3, 123456789, time.FixedZone("offset", 3600))
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				var nullable any
				if validZero {
					nullable = time.Time{}
				}

				_, err := tx.Tx().ExecContext(ctx, `UPDATE instances SET creation_date=?, last_use_date=?, expiry_date=? WHERE id=?`, instant, nullable, nullable, f.instances[0])
				require.NoError(t, err)
				require.NoError(t, cluster.UpdateInstanceConfig(ctx, tx.Tx(), f.instances[0], map[string]string{"user.delete": "original"}))
				return ordinaryFixtureBaseline(ctx, tx, f.instances[0])
			})
			proposed := ordinaryCapture(t, f, nil)
			require.Empty(t, proposed.Profiles, "nil selected names must not inherit attachments")
			require.Len(t, proposed.Expected.Current.Profiles, 1)
			require.Equal(t, validZero, proposed.Fields.LastUsedDate.Valid)
			require.True(t, proposed.Fields.LastUsedDate.Time.IsZero())
			require.Equal(t, validZero, proposed.Fields.ExpiryDate.Valid)
			require.Equal(t, time.UTC, proposed.Fields.CreationDate.Location())
			require.Equal(t, instant.UTC(), proposed.Fields.CreationDate)
			proposed.Fields.Architecture = 0
			proposed.Fields.CreationDate = proposed.Fields.CreationDate.In(time.FixedZone("same-instant", -7200))
			proposed.Config["user.delete"] = ""
			proposed.Devices["eth0"] = map[string]string{"type": "nic", "network": "new", "mtu": ""}
			require.Equal(t, "original", proposed.Expected.Current.LocalConfig["user.delete"])
			require.Empty(t, proposed.Expected.Current.LocalDevices)
			identity := f.identity(t, "", f.instances[0])
			caller := profileMetadataJSON(t, proposed)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				return project.ClaimOrdinaryProfileReferenceApply(ctx, tx, identity, proposed)
			})
			require.Equal(t, caller, profileMetadataJSON(t, proposed))
			_, raw := profileMetadataAttemptSnapshots(t, f, identity)
			var target db.ProfileReferenceSnapshot
			require.NoError(t, json.Unmarshal([]byte(raw), &target))
			require.Empty(t, target.LocalConfig)
			require.Equal(t, map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}}, target.LocalDevices)
			// Caller writes after admission cannot mutate the saved JSON or its detached expected read.
			proposed.Config["user.after"] = "changed"
			proposed.Devices["eth0"]["network"] = "old"
			_, after := profileMetadataAttemptSnapshots(t, f, identity)
			require.Equal(t, raw, after)
			require.NotContains(t, proposed.Expected.Current.LocalConfig, "user.after")
		})
	}
}

func TestOrdinaryProfileReferenceLiveDrift(t *testing.T) {
	boundaries := []string{"local-config", "local-device", "attachment-detach", "attachment-order", "attachment-id", "selected-generation", "selected-payload", "selected-name-reuse", "project-inheritance", "project-operational", "network-type", "acl-identity", "member", "placement", "desired-sequence", "applied-sequence", "applied-version", "desired-version", "missing-baseline", "shared-writer", "scalar", "null-to-valid-zero"}
	for _, boundary := range boundaries {
		t.Run(boundary, func(t *testing.T) {
			f, _, selected := ordinaryFixture(t, "inherited", false)
			if boundary == "attachment-order" {
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), f.project, []string{"tracked", "selected"}))
					return ordinaryFixtureBaseline(ctx, tx, f.instances[0])
				})
			}

			if boundary == "null-to-valid-zero" {
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.Tx().ExecContext(ctx, `UPDATE instances SET last_use_date=NULL WHERE id=?`, f.instances[0])
					return err
				})
			}

			if boundary == "applied-sequence" {
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := project.CaptureAndCommitProfileReferenceUpdate(ctx, tx, f.request(api.ProfilePut{Description: "pending sequence"}, 0))
					return err
				})
			}

			proposed := ordinaryCapture(t, f, []string{"selected"})
			identity := f.identity(t, "", f.instances[0])
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				switch boundary {
				case "local-config":
					return cluster.UpdateInstanceConfig(ctx, tx.Tx(), f.instances[0], map[string]string{"user.changed": "yes"})
				case "local-device":
					devices, e := cluster.APIToDevices(map[string]map[string]string{"local": {"type": "nic", "network": "new"}})
					require.NoError(t, e)
					return cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instances[0], devices)
				case "attachment-detach":
					return cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), f.project, nil)
				case "attachment-order":
					return cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), f.project, []string{"selected", "tracked"})
				case "attachment-id":
					return cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), f.project, []string{"selected"})
				case "selected-generation":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles SET reference_generation=reference_generation+1 WHERE id=?`, selected)
				case "selected-payload":
					return cluster.UpdateProfileConfig(ctx, tx.Tx(), selected, map[string]string{"user.changed": "yes"})
				case "selected-name-reuse":
					require.NoError(t, cluster.DeleteProfile(ctx, tx.Tx(), "default", "selected"))
					id, e := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "selected"})
					require.NoError(t, e)
					require.NotEqual(t, selected, id)
				case "project-inheritance":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE projects_config SET value='true' WHERE project_id=? AND key='features.profiles'`, proposed.Expected.Current.ProjectID)
				case "project-operational":
					_, err = tx.Tx().ExecContext(ctx, `INSERT INTO projects_config(project_id,key,value) VALUES(?,'limits.instances','9')`, proposed.Expected.Current.ProjectID)
				case "network-type":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE networks SET type=? WHERE id=?`, db.NetworkTypeBridge, f.oldNetwork)
				case "acl-identity":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE networks_acls SET id=id+100 WHERE id=?`, f.acl)
				case "member":
					_, err = tx.Tx().ExecContext(ctx, `INSERT INTO nodes(name,description,address,schema,api_extensions,arch) SELECT 'other','','192.0.2.2',schema,api_extensions,arch FROM nodes WHERE id=?`, identity.MemberID)
					require.NoError(t, err)
					_, err = tx.Tx().ExecContext(ctx, `UPDATE instances SET node_id=(SELECT id FROM nodes WHERE name='other') WHERE id=?`, f.instances[0])
				case "placement":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE instances_profile_reference_apply SET placement_revision=placement_revision+1 WHERE instance_id=?`, f.instances[0])
				case "desired-sequence":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE instances_profile_reference_apply SET desired_sequence=desired_sequence+1 WHERE instance_id=?`, f.instances[0])
				case "applied-sequence":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE instances_profile_reference_apply SET applied_sequence=applied_sequence+1 WHERE instance_id=?`, f.instances[0])
				case "applied-version", "desired-version":
					column := "applied_snapshot"
					if boundary == "desired-version" {
						column = "desired_snapshot"
					}

					var data string
					require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT "+column+" FROM instances_profile_reference_apply WHERE instance_id=?", f.instances[0]).Scan(&data))
					var snapshot db.ProfileReferenceSnapshot
					require.NoError(t, json.Unmarshal([]byte(data), &snapshot))
					snapshot.Version = 2
					_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_profile_reference_apply SET "+column+"=? WHERE instance_id=?", profileMetadataJSON(t, snapshot), f.instances[0])
				case "missing-baseline":
					_, err = tx.Tx().ExecContext(ctx, `DELETE FROM instances_profile_reference_apply WHERE instance_id=?`, f.instances[0])
				case "shared-writer":
					return tx.AcquireOVNPeerOperation(ctx, "ordinary-shared", "acl-config", false)
				case "scalar":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE instances SET description='changed after capture' WHERE id=?`, f.instances[0])
				case "null-to-valid-zero":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE instances SET last_use_date=? WHERE id=?`, time.Time{}, f.instances[0])
				}

				return err
			})
			ordinaryConflict(t, f, identity, proposed)
		})
	}
}

func TestOrdinaryProfileReferenceClaimCallbackRollback(t *testing.T) {
	f, _, _ := ordinaryFixture(t, "default", false)
	proposed := ordinaryCapture(t, f, []string{"selected"})
	identity := f.identity(t, "", f.instances[0])
	before := ordinaryRows(t, f)
	injected := errors.New("ordinary claim callback rollback")
	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimOrdinaryProfileReferenceApply(ctx, tx, identity, proposed))
		var count int
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM profiles_reference_attempts WHERE token=?`, identity.Token).Scan(&count))
		require.Equal(t, 1, count)
		return injected
	})
	require.ErrorIs(t, err, injected)
	require.Equal(t, before, ordinaryRows(t, f))
}

func TestOrdinaryProfileReferenceReservationConflictPreservesClaim(t *testing.T) {
	f, _, _ := ordinaryFixture(t, "default", false)
	proposed := ordinaryCapture(t, f, []string{"selected"})
	identity := f.identity(t, "", f.instances[0])
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.ClaimOrdinaryProfileReferenceApply(ctx, tx, identity, proposed)
	})
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcquireOVNNetworkOperation(ctx, "default", "new", "foreign-child", "nic")
	})
	before := ordinaryRows(t, f)
	children, err := f.c.ReserveProfileReferenceChildren(f.ctx, identity)
	require.Error(t, err)
	require.Nil(t, children)
	require.Equal(t, before, ordinaryRows(t, f), "failed separate reservation must preserve committed claim and retention")
	require.Equal(t, identity.Token, profileMetadataReadState(t, f).ActiveToken)
}

func TestOrdinaryProfileReferenceDesiredValidationAndDuplicateCapture(t *testing.T) {
	f, _, _ := ordinaryFixture(t, "default", false)
	before := ordinaryRows(t, f)
	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := project.CaptureOrdinaryProfileReferenceInputs(ctx, tx, f.instances[0], []string{"selected", "selected"})
		return err
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	require.Equal(t, before, ordinaryRows(t, f))
	proposed := ordinaryCapture(t, f, []string{"selected"})
	identity := f.identity(t, "", f.instances[0])
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error { return tx.NetworkDeleting("default", "new") })
	ordinaryConflict(t, f, identity, proposed)
}

func TestOrdinaryProfileReferenceTenantOwnedIdentities(t *testing.T) {
	for _, names := range [][]string{{"tracked", "selected"}, {"selected", "tracked"}} {
		t.Run(names[0]+"-then-"+names[1], func(t *testing.T) {
			f, _, defaultSelected := ordinaryFixture(t, "inherited", false)
			profiles := map[string]int64{}
			networks := map[string]int64{}
			acls := map[string]int64{}
			var tenantID int64
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				tenantID, err = cluster.GetProjectID(ctx, tx.Tx(), f.project)
				require.NoError(t, err)
				// Build tenant-owned fixture inputs before initializing their evidenced baseline.
				require.NoError(t, cluster.UpdateProject(ctx, tx.Tx(), f.project, api.ProjectPut{Config: map[string]string{"features.profiles": "true", "features.networks": "true"}}))
				var old int64
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT id FROM networks WHERE project_id=? AND name='old'`, tenantID).Scan(&old))
				networks["old"] = old
				_, err = tx.Tx().ExecContext(ctx, `UPDATE networks SET type=? WHERE id=?`, db.NetworkTypeOVN, old)
				require.NoError(t, err)
				networks["new"], err = tx.CreateNetwork(ctx, f.project, "new", "", db.NetworkTypeOVN, nil)
				require.NoError(t, err)
				acls["old"], err = cluster.GetNetworkACLID(ctx, tx.Tx(), f.project, "old-acl")
				require.NoError(t, err)
				acls["new"], err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: f.project, Name: "new-acl"})
				require.NoError(t, err)
				_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "new-acl"})
				require.NoError(t, err)
				for _, name := range []string{"tracked", "selected"} {
					profiles[name], err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: f.project, Name: name, Description: "tenant " + name})
					require.NoError(t, err)
					network := "old"
					if name == "selected" {
						network = "new"
					}

					require.NoError(t, cluster.UpdateProfileConfig(ctx, tx.Tx(), profiles[name], map[string]string{"user.owner": f.project, "user.profile": name}))
					devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": network, "security.acls": network + "-acl"}})
					require.NoError(t, err)
					require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), profiles[name], devices))
				}

				require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), f.project, []string{"tracked"}))
				return ordinaryFixtureBaseline(ctx, tx, f.instances[0])
			})
			require.NotEqual(t, f.profileID, profiles["tracked"], "same-name default profile must remain a decoy")
			require.NotEqual(t, defaultSelected, profiles["selected"])
			require.NotEqual(t, f.oldNetwork, networks["old"])
			require.NotEqual(t, f.newNetwork, networks["new"])
			require.NotEqual(t, f.acl, acls["old"])
			proposed := ordinaryCapture(t, f, names)
			require.Equal(t, tenantID, proposed.Expected.Current.ProjectID)
			require.Len(t, proposed.Expected.Current.Profiles, 1)
			require.Equal(t, profiles["tracked"], proposed.Expected.Current.Profiles[0].ID)
			require.Len(t, proposed.Profiles, 2)
			for i, profile := range proposed.Profiles {
				require.Equal(t, profiles[names[i]], profile.ID)
				require.Equal(t, f.project, profile.Profile.Project)
				require.Equal(t, names[i], profile.Profile.Name)
				require.Zero(t, profile.Generation)
			}

			identity := f.identity(t, "", f.instances[0])
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				return project.ClaimOrdinaryProfileReferenceApply(ctx, tx, identity, proposed)
			})
			_, targetJSON := profileMetadataAttemptSnapshots(t, f, identity)
			var target db.ProfileReferenceSnapshot
			require.NoError(t, json.Unmarshal([]byte(targetJSON), &target))
			require.Equal(t, proposed.Profiles, target.Profiles)
			require.Equal(t, tenantID, target.ProjectID)
			require.Equal(t, "tenant", target.Project.Name)
			require.Equal(t, "tenant", target.ExpandedConfig["user.owner"])
			require.Equal(t, names[1], target.ExpandedConfig["user.profile"])
			network := "old"
			if names[1] == "selected" {
				network = "new"
			}

			device := map[string]string{"type": "nic", "network": network, "security.acls": network + "-acl"}
			require.Equal(t, map[string]map[string]string{"eth0": device}, target.ExpandedDevices)
			resource := db.ProfileReferenceResource{Device: "eth0", NetworkID: networks[network], NetworkProjectID: tenantID, NetworkProject: "tenant", NetworkName: network, NetworkType: "ovn", Config: device}
			aclResource := resource
			aclResource.ACLID = acls[network]
			aclResource.ACLProjectID = tenantID
			require.Equal(t, []db.ProfileReferenceResource{resource, aclResource}, target.Resources)
			children, err := f.c.ReserveProfileReferenceChildren(f.ctx, identity)
			require.NoError(t, err)
			expectedNetworks := map[int64]bool{networks["old"]: true, networks[network]: true}
			require.Len(t, children, len(expectedNetworks))
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				for _, child := range children {
					require.True(t, expectedNetworks[child.NetworkID])
					require.Equal(t, tenantID, child.ProjectID)
					require.Equal(t, "tenant", child.ProjectName)
					require.NotEmpty(t, child.Token)
					var stored bool
					require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM profiles_reference_children WHERE attempt_token=? AND network_id=? AND project_id=? AND name=? AND token=?)`, identity.Token, child.NetworkID, tenantID, child.Name, child.Token).Scan(&stored))
					require.True(t, stored)
				}

				return nil
			})
		})
	}
}

func profileIdentityMutate(f *profileReferenceFixture, operation, projectName, name string) error {
	return f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		if operation == "rename" {
			return project.RenameProfileWithReferences(ctx, tx, projectName, name, name+"-renamed")
		}

		return project.DeleteProfileWithReferences(ctx, tx, projectName, name)
	})
}

func profileIdentityBlocked(t *testing.T, f *profileReferenceFixture, operation, projectName, name string) {
	t.Helper()
	before := profileProjectState(t, f)
	err := profileIdentityMutate(f, operation, projectName, name)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "wrapper must reject retained identity: %v", err)
	require.EqualError(t, err, "Profile identity is retained by reference accounting or cannot be verified")
	require.Equal(t, before, profileProjectState(t, f), "wrapper changed identity or retained evidence")
}

func profileIdentityCreate(t *testing.T, f *profileReferenceFixture, projectName, name string) db.ProfileReferenceProfile {
	t.Helper()
	var result db.ProfileReferenceProfile
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		result.ID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: projectName, Name: name})
		result.Profile = api.Profile{Project: projectName, Name: name}
		return err
	})
	return result
}

func profileIdentityExec(t *testing.T, f *profileReferenceFixture, statement string, args ...any) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, statement, args...)
		return err
	})
}

func profileIdentityProfiles(t *testing.T, f *profileReferenceFixture, table, column string, profiles []db.ProfileReferenceProfile) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var data string
		err := tx.Tx().QueryRowContext(ctx, "SELECT "+column+" FROM "+table+" LIMIT 1").Scan(&data)
		require.NoError(t, err)
		var snapshot db.ProfileReferenceSnapshot
		require.NoError(t, json.Unmarshal([]byte(data), &snapshot))
		snapshot.Profiles = profiles
		_, err = tx.Tx().ExecContext(ctx, "UPDATE "+table+" SET "+column+"=?", profileMetadataJSON(t, snapshot))
		return err
	})
}

func profileIdentityClearCurrent(t *testing.T, f *profileReferenceFixture) {
	t.Helper()
	for _, column := range []string{"applied_snapshot", "desired_snapshot"} {
		profileIdentityProfiles(t, f, "instances_profile_reference_apply", column, nil)
	}

	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]), f.project, nil)
	})
}

func TestProfileReferenceIdentityNoAccounting(t *testing.T) {
	for _, operation := range []string{"rename", "delete"} {
		t.Run(operation, func(t *testing.T) {
			f := newProfileReferenceFixture(t, 0, "default")
			require.NoError(t, profileIdentityMutate(f, operation, "default", "tracked"))
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := cluster.GetProfileID(ctx, tx.Tx(), "default", "tracked")
				require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "%v", err)
				if operation == "rename" {
					id, err := cluster.GetProfileID(ctx, tx.Tx(), "default", "tracked-renamed")
					require.NoError(t, err)
					require.Equal(t, f.profileID, id)
				}

				return nil
			})
			before := profileProjectState(t, f)
			err := profileIdentityMutate(f, operation, "default", "missing")
			require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "%v", err)
			require.Equal(t, before, profileProjectState(t, f))
		})
	}

	t.Run("untracked-attached-rename", func(t *testing.T) {
		f := newProfileReferenceFixture(t, 1, "default")
		profileIdentityExec(t, f, `DELETE FROM instances_profile_reference_apply`)
		require.NoError(t, profileIdentityMutate(f, "rename", "default", "tracked"))
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			profiles, err := cluster.GetInstanceProfiles(ctx, tx.Tx(), int(f.instances[0]))
			require.NoError(t, err)
			require.Len(t, profiles, 1)
			require.Equal(t, int(f.profileID), profiles[0].ID)
			require.Equal(t, "tracked-renamed", profiles[0].Name)
			return nil
		})
	})
	t.Run("destination-conflict", func(t *testing.T) {
		f := newProfileReferenceFixture(t, 0, "default")
		profileIdentityCreate(t, f, "default", "tracked-renamed")
		before := profileProjectState(t, f)
		err := profileIdentityMutate(f, "rename", "default", "tracked")
		require.Error(t, err)
		rawErr := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			return cluster.RenameProfile(ctx, tx.Tx(), "default", "tracked", "tracked-renamed")
		})
		require.Error(t, rawErr)
		require.EqualError(t, err, rawErr.Error(), "wrapper preserves the mapper's destination conflict")
		require.Equal(t, before, profileProjectState(t, f))
	})
}

func TestProfileReferenceIdentityIdleSnapshots(t *testing.T) {
	for _, column := range []string{"applied_snapshot", "desired_snapshot"} {
		for _, operation := range []string{"rename", "delete"} {
			t.Run(column+"/"+operation, func(t *testing.T) {
				f := profileProjectEmptyFixture(t)
				state := profileMetadataReadState(t, f)
				profiles := state.Applied.Profiles
				profileIdentityClearCurrent(t, f)
				profileIdentityProfiles(t, f, "instances_profile_reference_apply", column, profiles)
				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					var count int
					require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM profiles_reference_usage`).Scan(&count))
					require.Zero(t, count)
					return nil
				})
				profileIdentityBlocked(t, f, operation, "default", "tracked")
			})
		}
	}
}

func TestProfileReferenceIdentityConsumerRoots(t *testing.T) {
	for _, retained := range []bool{false, true} {
		for _, column := range []string{"before_snapshot", "after_snapshot"} {
			for _, operation := range []string{"rename", "delete"} {
				t.Run(fmt.Sprintf("retained-satisfied-%v/%s/%s", retained, column, operation), func(t *testing.T) {
					f := newProfileReferenceFixture(t, 1, "inherited")
					other := profileIdentityCreate(t, f, "default", "historical")
					commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
					require.NoError(t, err)
					require.NotEqual(t, other.ID, commit.ProfileID)
					profileIdentityClearCurrent(t, f)
					for _, field := range []string{"before_snapshot", "after_snapshot"} {
						profileIdentityProfiles(t, f, "profiles_reference_consumers", field, nil)
					}

					profileIdentityProfiles(t, f, "profiles_reference_consumers", column, []db.ProfileReferenceProfile{other})
					if retained {
						// Deliberately inconsistent satisfied-with-usage fixture: labels cannot release ownership.
						profileIdentityExec(t, f, `UPDATE profiles_reference_consumers SET satisfied=1`)
					} else {
						profileIdentityExec(t, f, `DELETE FROM profiles_reference_usage`)
					}

					profileIdentityBlocked(t, f, operation, "default", "historical")
				})
			}
		}
	}
}

// Lifecycle helpers establish rows; direct adjustments below isolate each durable ownership root.
func profileIdentityAttempt(t *testing.T, root string) (*profileReferenceFixture, db.ProfileReferenceApply) {
	t.Helper()
	f := newProfileReferenceFixture(t, 1, "inherited")
	identity := f.identity(t, "", f.instances[0])
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, identity))
		if root == "applied" || root == "completed" {
			require.NoError(t, tx.SealProfileReferenceChildren(ctx, identity, nil))
			require.NoError(t, tx.MarkProfileReferenceApplied(ctx, identity))
			if root == "completed" {
				return tx.FinalizeProfileReferenceApply(ctx, identity)
			}
		}
		if root == "recovery" {
			return tx.FailProfileReferenceApply(ctx, identity, db.ProfileReferenceUnknown)
		}

		if strings.HasPrefix(root, "retryable") {
			return tx.FailProfileReferenceApply(ctx, identity, db.ProfileReferenceBeforeDispatch)
		}

		return nil
	})
	profileIdentityClearCurrent(t, f)
	profileIdentityExec(t, f, `UPDATE instances_profile_reference_apply SET active_token=''`)
	if !strings.HasSuffix(root, "usage") {
		profileIdentityExec(t, f, `DELETE FROM profiles_reference_usage`)
	}

	if strings.HasPrefix(root, "completed-") {
		// Deliberately inconsistent completed-with-ownership fixture, not a legal finalizer outcome.
		profileIdentityExec(t, f, `UPDATE profiles_reference_attempts SET phase='completed'`)
	}

	if strings.HasSuffix(root, "active") {
		profileIdentityExec(t, f, `UPDATE instances_profile_reference_apply SET active_token=?`, identity.Token)
	}

	if strings.HasSuffix(root, "children") {
		profileIdentityExec(t, f, `INSERT INTO profiles_reference_children (attempt_token, network_id, project_id, name, token) VALUES (?, ?, 1, 'old', 'retained-child')`, identity.Token, f.oldNetwork)
	}

	if root == "unsupported" {
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			_, err := tx.Tx().ExecContext(ctx, `PRAGMA ignore_check_constraints=ON`)
			require.NoError(t, err)
			_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_attempts SET phase='future-phase'`)
			require.NoError(t, err)
			_, err = tx.Tx().ExecContext(ctx, `PRAGMA ignore_check_constraints=OFF`)
			return err
		})
	}

	return f, identity
}

func TestProfileReferenceIdentityAttemptRoots(t *testing.T) {
	for _, root := range []string{"applying", "applied", "recovery", "unsupported", "retryable-active", "completed-active", "retryable-usage", "completed-usage", "retryable-children", "completed-children"} {
		for _, column := range []string{"baseline_snapshot", "target_snapshot"} {
			for _, operation := range []string{"rename", "delete"} {
				t.Run(root+"/"+column+"/"+operation, func(t *testing.T) {
					f, _ := profileIdentityAttempt(t, root)
					other := profileIdentityCreate(t, f, "default", "historical")
					for _, field := range []string{"baseline_snapshot", "target_snapshot"} {
						profileIdentityProfiles(t, f, "profiles_reference_attempts", field, nil)
					}

					profileIdentityProfiles(t, f, "profiles_reference_attempts", column, []db.ProfileReferenceProfile{other})
					profileIdentityBlocked(t, f, operation, "default", "historical")
				})
			}
		}
	}
}

func TestProfileReferenceIdentityGlobalExactIDs(t *testing.T) {
	for _, operation := range []string{"rename", "delete"} {
		t.Run(operation, func(t *testing.T) {
			f := profileProjectEmptyFixture(t)
			tenant := profileIdentityCreate(t, f, "tenant", "tracked")
			require.NotEqual(t, f.profileID, tenant.ID)
			profileIdentityBlocked(t, f, operation, "default", "tracked")
			require.NoError(t, profileIdentityMutate(f, operation, "tenant", "tracked"))
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				id, err := cluster.GetProfileID(ctx, tx.Tx(), "default", "tracked")
				require.NoError(t, err)
				require.Equal(t, f.profileID, id)
				return nil
			})
		})
	}
}

func TestProfileReferenceIdentitySelectedContracts(t *testing.T) {
	for _, test := range []struct {
		table, column string
		value         any
	}{
		{"instances_profile_reference_apply", "contract_version", 2},
		{"instances_profile_reference_apply", "input_revision", 1},
		{"instances_profile_reference_apply", "materialized_observation_id", 1},
		{"profiles_reference_attempts", "contract_version", 2},
		{"profiles_reference_attempts", "claimed_input_revision", 1},
		{"profiles_reference_attempts", "result_input_revision", 1},
		{"profiles_reference_attempts", "observation_cutoff", 1},
		{"profiles_reference_attempts", "generation_plan", "secret-future-plan"},
		{"profiles_reference_attempts", "result_snapshot", "secret-future-result"},
		{"profiles_reference_receipts", "result_snapshot", "secret-receipt-result"},
		{"profiles_reference_receipts", "post_commit_evidence", "secret-commit-evidence"},
	} {
		for _, operation := range []string{"rename", "delete"} {
			t.Run(test.table+"/"+test.column+"/"+operation, func(t *testing.T) {
				f, identity := profileIdentityAttempt(t, "applying")
				profileIdentityCreate(t, f, "default", "unrelated")
				if test.table == "profiles_reference_receipts" {
					// A selected attempt with a receipt is validated even before completed phase.
					profileIdentityExec(t, f, `INSERT INTO profiles_reference_receipts (attempt_token) VALUES (?)`, identity.Token)
				}

				profileIdentityExec(t, f, "UPDATE "+test.table+" SET "+test.column+"=?", test.value)
				profileIdentityBlocked(t, f, operation, "default", "unrelated")
			})
		}
	}
}

func TestProfileReferenceIdentitySnapshotProjection(t *testing.T) {
	for _, root := range []string{"state", "consumer", "attempt"} {
		for _, invalid := range []string{"missing-profiles", "object-profiles", "string-profiles", "null-object", "array-object", "bad-json", "trailing-json", "version", "instance", "project", "member", "missing-header", "null-header", "id-zero", "id-negative", "id-string", "id-fraction", "id-null", "profile-null", "duplicate-id", "contradictory-name", "duplicate-key", "case-key", "duplicate-profile-key", "case-id-key"} {
			for _, operation := range []string{"rename", "delete"} {
				t.Run(root+"/"+invalid+"/"+operation, func(t *testing.T) {
					f := profileProjectEmptyFixture(t)
					profileIdentityCreate(t, f, "default", "unrelated")
					table, column := "instances_profile_reference_apply", "desired_snapshot"
					switch root {
					case "consumer":
						_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
						require.NoError(t, err)
						table, column = "profiles_reference_consumers", "after_snapshot"
					case "attempt":
						identity := f.identity(t, "", f.instances[0])
						f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
							return project.ClaimProfileReferenceApply(ctx, tx, identity)
						})
						table, column = "profiles_reference_attempts", "target_snapshot"
					}

					state := profileMetadataReadState(t, f)
					object := map[string]any{"Version": 1, "InstanceID": state.InstanceID, "ProjectID": state.ProjectID, "MemberID": state.MemberID, "Profiles": []any{}}
					profile := map[string]any{"ID": f.profileID}
					switch invalid {
					case "missing-profiles":
						delete(object, "Profiles")
					case "object-profiles":
						object["Profiles"] = map[string]any{}
					case "string-profiles":
						object["Profiles"] = "secret-profile-config"
					case "version":
						object["Version"] = 2
					case "instance":
						object["InstanceID"] = state.InstanceID + 1
					case "project":
						object["ProjectID"] = state.ProjectID + 1
					case "member":
						object["MemberID"] = state.MemberID + 1
					case "missing-header":
						delete(object, "InstanceID")
					case "null-header":
						object["InstanceID"] = nil
					case "id-zero":
						profile["ID"] = 0
					case "id-negative":
						profile["ID"] = -1
					case "id-string":
						profile["ID"] = "secret-profile-config"
					case "id-fraction":
						profile["ID"] = 1.5
					case "id-null":
						profile["ID"] = nil
					case "profile-null":
						object["Profiles"] = []any{nil}
					case "duplicate-id":
						object["Profiles"] = []any{profile, profile}
					case "contradictory-name":
						object["Profiles"] = []any{map[string]any{"ID": 100, "Profile": map[string]string{"project": "x", "name": "same"}}, map[string]any{"ID": 101, "Profile": map[string]string{"project": "x", "name": "same"}}}
					}

					if strings.HasPrefix(invalid, "id-") {
						object["Profiles"] = []any{profile}
					}

					data := profileMetadataJSON(t, object)
					switch invalid {
					case "null-object":
						data = "null"
					case "array-object":
						data = "[]"
					case "bad-json":
						data = `{secret-profile-config`
					case "trailing-json":
						data += `{}`
					case "duplicate-key":
						data = strings.TrimSuffix(data, "}") + `,"Profiles":[]}`
					case "case-key":
						data = strings.TrimSuffix(data, "}") + `,"profiles":[]}`
					case "duplicate-profile-key":
						data = strings.Replace(data, `"Profiles":[]`, `"Profiles":[{"ID":100,"ID":101}]`, 1)
					case "case-id-key":
						data = strings.Replace(data, `"Profiles":[]`, `"Profiles":[{"ID":100,"id":101}]`, 1)
					}

					profileIdentityExec(t, f, "UPDATE "+table+" SET "+column+"=?", data)
					profileIdentityBlocked(t, f, operation, "default", "unrelated")
				})
			}
		}
	}
}

func TestProfileReferenceIdentityEmptyAndInertHistory(t *testing.T) {
	for _, empty := range []string{"null", "[]"} {
		for _, operation := range []string{"rename", "delete"} {
			t.Run(empty+"/"+operation, func(t *testing.T) {
				f := profileProjectEmptyFixture(t)
				profileIdentityClearCurrent(t, f)
				if empty == "[]" {
					for _, field := range []string{"applied_snapshot", "desired_snapshot"} {
						profileIdentityProfiles(t, f, "instances_profile_reference_apply", field, []db.ProfileReferenceProfile{})
					}
				}
				require.NoError(t, profileIdentityMutate(f, operation, "default", "tracked"))
			})
		}
	}
	for _, phase := range []string{"retryable", "completed"} {
		t.Run(phase, func(t *testing.T) {
			f, identity := profileIdentityAttempt(t, phase)
			profileIdentityExec(t, f, `UPDATE profiles_reference_attempts SET baseline_snapshot='secret-invalid', target_snapshot='null', contract_version=2, generation_plan='future'`)
			if phase == "completed" {
				profileIdentityExec(t, f, `UPDATE profiles_reference_receipts SET result_snapshot='future', post_commit_evidence='future' WHERE attempt_token=?`, identity.Token)
			}

			require.NoError(t, profileIdentityMutate(f, "rename", "default", "tracked"))
		})
	}

	t.Run("satisfied-unretained-consumer-and-completed-change", func(t *testing.T) {
		f := profileProjectEmptyFixture(t)
		commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
		require.NoError(t, err)
		f.complete(t, f.identity(t, commit.Token, f.instances[0]))
		profileIdentityClearCurrent(t, f)
		profileIdentityExec(t, f, `UPDATE profiles_reference_consumers SET before_snapshot='invalid', after_snapshot='null'`)
		require.NoError(t, profileIdentityMutate(f, "rename", "default", "tracked"))
		profileIdentityBlocked(t, f, "delete", "default", "tracked-renamed")
	})
	t.Run("incomplete-change-without-consumers", func(t *testing.T) {
		f := newProfileReferenceFixture(t, 0, "default")
		_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, f.request(api.ProfilePut{}, 0), nil)
		require.NoError(t, err)
		// Deliberate incomplete empty change proves this independent root does not depend on consumers.
		profileIdentityExec(t, f, `UPDATE profiles_reference_changes SET completed=0`)
		profileIdentityBlocked(t, f, "rename", "default", "tracked")
		profileIdentityBlocked(t, f, "delete", "default", "tracked")
	})
}

func TestProfileReferenceIdentityFeatureOffTransaction(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprintf("retained-%v", retained), func(t *testing.T) {
			f := newProfileReferenceFixture(t, 0, "inherited")
			request := profileProjectGet(t, f, "tenant")
			request.Config["features.profiles"] = "true"
			require.NoError(t, profileProjectUpdate(f, "tenant", request, []string{"features.profiles"}))
			tenant := profileIdentityCreate(t, f, "tenant", "default")
			var globalID int64
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				globalID, err = cluster.GetProfileID(ctx, tx.Tx(), "default", "default")
				if api.StatusErrorCheck(err, http.StatusNotFound) {
					globalID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "default"})
				}

				require.NoError(t, err)
				var nodeName string
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&nodeName))
				id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "tenant", Name: "idle", Node: nodeName, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
				require.NoError(t, err)
				f.instances = []int64{id}
				require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(id), "tenant", []string{"default"}))
				snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, id)
				require.NoError(t, err)
				require.Equal(t, tenant.ID, snapshot.Profiles[0].ID)
				require.NoError(t, tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1))
				return cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(id), "tenant", nil)
			})
			if !retained {
				profileIdentityClearCurrent(t, f)
			}

			before := profileProjectState(t, f)
			request.Config["features.profiles"] = "false"
			reached := false
			err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				err := project.AllowProjectUpdate(tx, "tenant", request.Config, []string{"features.profiles"})
				if err != nil {
					return err
				}

				err = cluster.UpdateProject(ctx, tx.Tx(), "tenant", request)
				if err != nil {
					return err
				}

				_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles SET description='earlier-callback-write' WHERE id=?`, f.profileID)
				require.NoError(t, err)
				var value string
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT value FROM projects_config WHERE project_id=(SELECT id FROM projects WHERE name='tenant') AND key='features.profiles'`).Scan(&value))
				require.Equal(t, "false", value)
				reached = true
				return project.DeleteProfileWithReferences(ctx, tx, "tenant", "default")
			})
			require.True(t, reached, "feature-off fixture must reach the identity wrapper after the project write")
			if retained {
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
				require.Equal(t, before, profileProjectState(t, f), "callback error must undo project config and earlier writes")
			} else {
				require.NoError(t, err)
				require.Equal(t, request, profileProjectGet(t, f, "tenant"))
			}

			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				id, err := cluster.GetProfileID(ctx, tx.Tx(), "default", "default")
				require.NoError(t, err)
				require.Equal(t, globalID, id)
				_, err = cluster.GetProfileID(ctx, tx.Tx(), "tenant", "default")
				if retained {
					require.NoError(t, err)
				} else {
					require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "%v", err)
				}

				return nil
			})
		})
	}
}

func TestProfileReferenceIdentityLaterReadFailure(t *testing.T) {
	for _, failure := range []string{"decode", "scan", "query"} {
		for _, operation := range []string{"rename", "delete"} {
			t.Run(failure+"/"+operation, func(t *testing.T) {
				f := newProfileReferenceFixture(t, 2, "default")
				profileIdentityCreate(t, f, "default", "unrelated")
				switch failure {
				case "decode":
					profileIdentityExec(t, f, `UPDATE instances_profile_reference_apply SET desired_snapshot='secret-invalid' WHERE instance_id=?`, f.instances[1])
				case "scan":
					profileIdentityExec(t, f, `UPDATE instances_profile_reference_apply SET member_id='invalid-integer' WHERE instance_id=?`, f.instances[1])
				}

				before := profileProjectState(t, f)
				err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					if failure == "query" {
						_, err := tx.Tx().ExecContext(ctx, `ALTER TABLE profiles_reference_children RENAME TO unavailable_children`)
						require.NoError(t, err)
					}

					if operation == "rename" {
						return project.RenameProfileWithReferences(ctx, tx, "default", "unrelated", "renamed")
					}

					return project.DeleteProfileWithReferences(ctx, tx, "default", "unrelated")
				})
				require.Error(t, err)
				if failure == "decode" {
					require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
					require.NotContains(t, err.Error(), "secret-invalid")
				} else {
					require.False(t, api.StatusErrorCheck(err, http.StatusConflict), "real SQL errors must propagate")
				}

				require.Equal(t, before, profileProjectState(t, f))
			})
		}
	}
}

func TestProfileReferenceIdentityUnsupportedAttemptPhase(t *testing.T) {
	for _, phase := range []string{"", "future-phase"} {
		for _, operation := range []string{"rename", "delete"} {
			t.Run(phase+"/"+operation, func(t *testing.T) {
				f, _ := profileIdentityAttempt(t, "applying")
				profileIdentityCreate(t, f, "default", "unrelated")
				for _, column := range []string{"baseline_snapshot", "target_snapshot"} {
					profileIdentityProfiles(t, f, "profiles_reference_attempts", column, nil)
				}

				f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
					// Isolated deliberate corruption: no supported schema writer emits these phases.
					_, err := tx.Tx().ExecContext(ctx, `PRAGMA ignore_check_constraints=ON`)
					require.NoError(t, err)
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_attempts SET phase=?`, phase)
					require.NoError(t, err)
					_, err = tx.Tx().ExecContext(ctx, `PRAGMA ignore_check_constraints=OFF`)
					return err
				})
				profileIdentityBlocked(t, f, operation, "default", "unrelated")
			})
		}
	}
}

type projectDeletionOwner struct {
	id       int64
	profile  int64
	instance int64
	network  int64
	acl      int64
}

type projectDeletionFixture struct {
	f      *profileReferenceFixture
	target projectDeletionOwner
	other  projectDeletionOwner
}

func newProjectDeletionFixture(t *testing.T) *projectDeletionFixture {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	f := &profileReferenceFixture{c: c, ctx: context.Background()}
	fixture := &projectDeletionFixture{f: f}
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var nodeName string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&nodeName))
		for name, owner := range map[string]*projectDeletionOwner{"target": &fixture.target, "other": &fixture.other} {
			var err error
			owner.id, err = cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: name})
			require.NoError(t, err)
			owner.profile, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: name, Name: "default"})
			require.NoError(t, err)
			owner.instance, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: name, Name: "same-instance", Node: nodeName, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
			require.NoError(t, err)
			owner.network, err = tx.CreateNetwork(ctx, name, "same-network", "", db.NetworkTypeBridge, nil)
			require.NoError(t, err)
			owner.acl, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: name, Name: "same-acl"})
			require.NoError(t, err)
		}

		return nil
	})
	return fixture
}

// Each row uses other owners except the one explicitly selected numeric dependency.
func (d *projectDeletionFixture) accounting(t *testing.T, table, branch, phase string, withACL bool) {
	t.Helper()
	d.f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		owner := d.other
		networkProject := d.other.id
		var aclID, aclProject any
		if withACL {
			aclID, aclProject = d.other.acl, d.other.id
		}

		switch branch {
		case "project_id":
			owner.id = d.target.id
		case "instance_owner":
			owner.instance = d.target.instance
		case "profile_owner":
			owner.profile = d.target.profile
		case "network_owner":
			owner.network = d.target.network
		case "network_project_id":
			networkProject = d.target.id
		case "acl_project_id":
			aclID, aclProject = d.other.acl, d.target.id
		case "acl_owner":
			aclID, aclProject = d.target.acl, d.other.id
		case "unrelated":
		default:
			t.Fatalf("Unknown dependency %q", branch)
		}

		insertChange := func(owner projectDeletionOwner) {
			_, err := tx.Tx().ExecContext(ctx, `INSERT INTO profiles_reference_changes (token, profile_id, project_id, generation, old_profile, new_profile, completed) VALUES ('change', ?, ?, 1, 'secret-invalid', '{"version":99}', 1)`, owner.profile, owner.id)
			require.NoError(t, err)
		}

		insertAttempt := func(owner projectDeletionOwner) {
			_, err := tx.Tx().ExecContext(ctx, `INSERT INTO profiles_reference_attempts (token, instance_id, project_id, member_id, placement_revision, sequence, owner, baseline_snapshot, target_snapshot, phase, children_sealed, child_set, contract_version, claimed_input_revision, generation_plan, result_snapshot, result_input_revision, observation_cutoff) VALUES ('attempt', ?, ?, 1, 1, 1, 'released', 'secret-invalid', '{"version":99}', ?, 1, '[]', 2, 7, 'future', 'null', 8, 9)`, owner.instance, owner.id, phase)
			require.NoError(t, err)
			_, err = tx.Tx().ExecContext(ctx, `INSERT INTO profiles_reference_receipts (attempt_token, result_snapshot, post_commit_evidence) VALUES ('attempt', 'secret-invalid', 'future')`)
			require.NoError(t, err)
		}

		switch table {
		case "instances_profile_reference_apply":
			_, err := tx.Tx().ExecContext(ctx, `INSERT INTO instances_profile_reference_apply (instance_id, project_id, member_id, placement_revision, desired_sequence, applied_sequence, applied_snapshot, desired_snapshot, contract_version, input_revision, materialized_observation_id) VALUES (?, ?, 1, 1, 1, 1, 'secret-invalid', '{"version":99}', 2, 7, 9)`, owner.instance, owner.id)
			require.NoError(t, err)
		case "profiles_reference_changes":
			insertChange(owner)
		case "profiles_reference_consumers":
			insertChange(d.other)
			_, err := tx.Tx().ExecContext(ctx, `INSERT INTO profiles_reference_consumers (change_token, instance_id, project_id, member_id, placement_revision, sequence, before_snapshot, after_snapshot, satisfied) VALUES ('change', ?, ?, 1, 1, 1, 'secret-invalid', '{"version":99}', 1)`, owner.instance, owner.id)
			require.NoError(t, err)
		case "profiles_reference_attempts":
			insertAttempt(owner)
		case "profiles_reference_children":
			insertAttempt(d.other)
			_, err := tx.Tx().ExecContext(ctx, `INSERT INTO profiles_reference_children (attempt_token, network_id, project_id, name, token) VALUES ('attempt', ?, ?, 'released-looking', '')`, owner.network, owner.id)
			require.NoError(t, err)
		case "profiles_reference_usage":
			insertAttempt(d.other)
			_, err := tx.Tx().ExecContext(ctx, `INSERT INTO profiles_reference_usage (attempt_token, instance_id, project_id, member_id, placement_revision, sequence, role, device, network_id, network_project_id, network_type, acl_id, acl_project_id, config) VALUES ('attempt', ?, ?, 1, 1, 1, 'before', 'released-looking', ?, ?, 'unknown', ?, ?, 'secret-invalid')`, owner.instance, owner.id, owner.network, networkProject, aclID, aclProject)
			require.NoError(t, err)
		default:
			t.Fatalf("Unknown table %q", table)
		}

		return nil
	})
}

func projectDeletionRows(t *testing.T, f *profileReferenceFixture) map[string][][]any {
	t.Helper()
	saved := ordinaryRows(t, f)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, table := range []string{"networks", "networks_config", "networks_acls", "networks_acls_config"} {
			rows, err := tx.Tx().QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY rowid")
			require.NoError(t, err)
			columns, err := rows.Columns()
			require.NoError(t, err)
			saved[table] = [][]any{}
			for rows.Next() {
				values := make([]any, len(columns))
				pointers := make([]any, len(columns))
				for i := range values {
					pointers[i] = &values[i]
				}

				require.NoError(t, rows.Scan(pointers...))
				for i, value := range values {
					bytes, ok := value.([]byte)
					if ok {
						values[i] = string(bytes)
					}
				}
				saved[table] = append(saved[table], values)
			}

			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
		}

		return nil
	})
	return saved
}

func (d *projectDeletionFixture) check(final bool) error {
	return d.f.c.Transaction(d.f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		if final {
			return project.DeleteProjectWithReferences(ctx, tx, "target", d.target.id)
		}

		return project.CheckProjectReferenceDeletion(ctx, tx, d.target.id)
	})
}

func TestProjectReferenceDeletionDependencies(t *testing.T) {
	for table, branches := range map[string][]string{
		"instances_profile_reference_apply": {"project_id", "instance_owner"},
		"profiles_reference_changes":        {"project_id", "profile_owner"},
		"profiles_reference_consumers":      {"project_id", "instance_owner"},
		"profiles_reference_attempts":       {"project_id", "instance_owner"},
		"profiles_reference_children":       {"project_id", "network_owner"},
		"profiles_reference_usage":          {"project_id", "network_project_id", "acl_project_id", "instance_owner", "network_owner", "acl_owner"},
	} {
		for _, branch := range branches {
			t.Run(table+"/"+branch, func(t *testing.T) {
				d := newProjectDeletionFixture(t)
				d.accounting(t, table, branch, "completed", false)
				before := projectDeletionRows(t, d.f)
				for _, final := range []bool{false, true} {
					err := d.check(final)
					require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "Expected typed reference conflict (final=%v), got %v", final, err)
					require.EqualError(t, err, "Project has retained profile reference accounting")
					require.Equal(t, before, projectDeletionRows(t, d.f))
				}
			})
		}
	}
}

func TestProjectReferenceDeletionAttemptPhases(t *testing.T) {
	for _, phase := range []string{"applying", "applied", "recovery", "retryable", "completed", "", "future-phase"} {
		t.Run(phase, func(t *testing.T) {
			d := newProjectDeletionFixture(t)
			d.accounting(t, "profiles_reference_attempts", "project_id", "completed", false)
			d.f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				// Isolated deliberate corruption also covers labels no supported schema writer emits.
				_, err := tx.Tx().ExecContext(ctx, `PRAGMA ignore_check_constraints=ON`)
				require.NoError(t, err)
				_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_attempts SET phase=?`, phase)
				require.NoError(t, err)
				_, err = tx.Tx().ExecContext(ctx, `PRAGMA ignore_check_constraints=OFF`)
				return err
			})
			before := projectDeletionRows(t, d.f)
			for _, final := range []bool{false, true} {
				err := d.check(final)
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "Expected typed reference conflict, got %v", err)
			}

			require.Equal(t, before, projectDeletionRows(t, d.f))
		})
	}
}

func TestProjectReferenceDeletionUnrelated(t *testing.T) {
	for _, table := range []string{"none", "instances_profile_reference_apply", "profiles_reference_changes", "profiles_reference_consumers", "profiles_reference_attempts", "profiles_reference_children", "profiles_reference_usage"} {
		t.Run(table, func(t *testing.T) {
			d := newProjectDeletionFixture(t)
			if table != "none" {
				d.accounting(t, table, "unrelated", "completed", true)
			}

			before := projectDeletionRows(t, d.f)
			require.NoError(t, d.check(false))
			require.Equal(t, before, projectDeletionRows(t, d.f))
			require.NoError(t, d.check(true))
			after := projectDeletionRows(t, d.f)
			for _, table := range []string{"instances_profile_reference_apply", "profiles_reference_changes", "profiles_reference_consumers", "profiles_reference_attempts", "profiles_reference_children", "profiles_reference_usage", "profiles_reference_receipts"} {
				require.Equal(t, before[table], after[table], table)
			}

			d.f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := cluster.GetProject(ctx, tx.Tx(), "target")
				require.True(t, api.StatusErrorCheck(err, http.StatusNotFound))
				for table, id := range map[string]int64{"profiles": d.target.profile, "instances": d.target.instance, "networks": d.target.network, "networks_acls": d.target.acl} {
					var count int
					require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id=?", id).Scan(&count))
					require.Zero(t, count, table)
				}

				_, err = cluster.GetProject(ctx, tx.Tx(), "other")
				return err
			})
		})
	}
}

func TestProjectReferenceDeletionFinalRecheck(t *testing.T) {
	d := newProjectDeletionFixture(t)
	require.NoError(t, d.check(false))
	// A later committed fixture transaction is sequential evidence, not a concurrent force operation.
	d.accounting(t, "profiles_reference_children", "network_owner", "completed", false)
	before := projectDeletionRows(t, d.f)
	err := d.check(true)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "Expected final recheck conflict, got %v", err)
	require.Equal(t, before, projectDeletionRows(t, d.f))
}

func TestProjectReferenceDeletionReusedName(t *testing.T) {
	d := newProjectDeletionFixture(t)
	require.NoError(t, d.check(false))
	d.f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		// Preserve the old row and give its name to a distinct, accounting-free project.
		require.NoError(t, cluster.RenameProject(ctx, tx.Tx(), "target", "original"))
		id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "target"})
		require.NoError(t, err)
		require.NotEqual(t, d.target.id, id)
		_, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "target", Name: "default"})
		return err
	})
	before := projectDeletionRows(t, d.f)
	err := d.check(true)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "Expected original-ID conflict, got %v", err)
	require.EqualError(t, err, "Project identity changed before deletion")
	require.Equal(t, before, projectDeletionRows(t, d.f))
}

func TestProjectReferenceDeletionInvalidIdentity(t *testing.T) {
	for _, final := range []bool{false, true} {
		for _, id := range []int64{-1, 0, 999999} {
			t.Run(fmt.Sprintf("final=%v/id=%d", final, id), func(t *testing.T) {
				d := newProjectDeletionFixture(t)
				before := projectDeletionRows(t, d.f)
				err := d.f.c.Transaction(d.f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					if final {
						return project.DeleteProjectWithReferences(ctx, tx, "target", id)
					}

					return project.CheckProjectReferenceDeletion(ctx, tx, id)
				})
				status := http.StatusConflict
				if !final && id > 0 {
					status = http.StatusNotFound
				}

				require.True(t, api.StatusErrorCheck(err, status), "Expected %d, got %v", status, err)
				require.Equal(t, before, projectDeletionRows(t, d.f))
			})
		}
	}
	t.Run("missing literal name", func(t *testing.T) {
		d := newProjectDeletionFixture(t)
		before := projectDeletionRows(t, d.f)
		err := d.f.c.Transaction(d.f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			return project.DeleteProjectWithReferences(ctx, tx, "missing", d.target.id)
		})
		require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "Expected not found, got %v", err)
		require.Equal(t, before, projectDeletionRows(t, d.f))
	})
	t.Run("renamed original", func(t *testing.T) {
		d := newProjectDeletionFixture(t)
		require.NoError(t, d.check(false))
		d.f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			return cluster.RenameProject(ctx, tx.Tx(), "target", "renamed")
		})
		before := projectDeletionRows(t, d.f)
		err := d.check(true)
		require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "Expected not found, got %v", err)
		require.Equal(t, before, projectDeletionRows(t, d.f))
	})
}

func TestProjectReferenceDeletionReadFailure(t *testing.T) {
	for _, table := range []string{"projects", "instances_profile_reference_apply", "profiles_reference_changes", "profiles_reference_consumers", "profiles_reference_attempts", "profiles_reference_children", "profiles_reference_usage"} {
		for _, final := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/final=%v", table, final), func(t *testing.T) {
				d := newProjectDeletionFixture(t)
				before := projectDeletionRows(t, d.f)
				err := d.f.c.Transaction(d.f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.Tx().ExecContext(ctx, "ALTER TABLE "+table+" RENAME TO unavailable_fixture_table")
					require.NoError(t, err)
					if final {
						if table == "projects" {
							_, readErr := cluster.GetProjectID(ctx, tx.Tx(), "target")
							require.Error(t, readErr)
							err = project.DeleteProjectWithReferences(ctx, tx, "target", d.target.id)
							require.EqualError(t, err, readErr.Error(), "Preserve the mapper's project read error")
							return err
						}

						return project.DeleteProjectWithReferences(ctx, tx, "target", d.target.id)
					}

					return project.CheckProjectReferenceDeletion(ctx, tx, d.target.id)
				})
				require.Error(t, err)
				if !final || table != "projects" {
					require.ErrorContains(t, err, "no such table")
				}

				require.False(t, api.StatusErrorCheck(err, http.StatusConflict, http.StatusNotFound))
				require.Equal(t, before, projectDeletionRows(t, d.f))
			})
		}
	}
}

func TestProjectReferenceDeletionCallbackRollback(t *testing.T) {
	for _, final := range []bool{false, true} {
		t.Run(fmt.Sprintf("final=%v", final), func(t *testing.T) {
			d := newProjectDeletionFixture(t)
			d.accounting(t, "profiles_reference_usage", "acl_owner", "retryable", true)
			before := projectDeletionRows(t, d.f)
			err := d.f.c.Transaction(d.f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				for _, statement := range []string{
					`UPDATE projects SET description='earlier project write' WHERE name='target'`,
					`UPDATE networks SET description='earlier network write'`,
					`UPDATE profiles_reference_usage SET config='earlier accounting write'`,
				} {
					_, err := tx.Tx().ExecContext(ctx, statement)
					require.NoError(t, err)
				}

				if final {
					return project.DeleteProjectWithReferences(ctx, tx, "target", d.target.id)
				}

				return project.CheckProjectReferenceDeletion(ctx, tx, d.target.id)
			})
			require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "Expected callback guard conflict, got %v", err)
			require.Equal(t, before, projectDeletionRows(t, d.f))
		})
	}
}
