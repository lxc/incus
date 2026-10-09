//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

type referenceAccountingFixture struct {
	c          *db.Cluster
	ctx        context.Context
	profileID  int64
	instanceID int64
	oldNetwork int64
	newNetwork int64
	aclID      int64
	commit     *project.ProfileReferenceCommit
	identity   db.ProfileReferenceApply
	children   []db.ProfileReferenceChild
}

func newReferenceAccountingFixture(t *testing.T) *referenceAccountingFixture {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	f := &referenceAccountingFixture{c: c, ctx: context.Background()}
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		f.oldNetwork, err = tx.CreateNetwork(ctx, "default", "old", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		f.newNetwork, err = tx.CreateNetwork(ctx, "default", "new", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		f.aclID, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "old-acl"})
		require.NoError(t, err)
		f.profileID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "tracked"})
		require.NoError(t, err)
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "old-acl"}})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), f.profileID, devices))
		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		f.instanceID, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "consumer", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instanceID), "default", []string{"tracked"}))
		snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, f.instanceID)
		require.NoError(t, err)
		return tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1)
	})
	var err error
	f.commit, err = project.CommitProfileReferenceUpdate(f.ctx, c, project.ProfileReferenceUpdate{Project: "default", Name: "tracked", ProfileID: f.profileID, Profile: api.ProfilePut{Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "new"}}}}, nil)
	require.NoError(t, err)
	consumer := f.commit.Consumers[0]
	f.identity = db.ProfileReferenceApply{Token: uuid.NewString(), ChangeToken: f.commit.Token, InstanceID: f.instanceID, ProjectID: consumer.ProjectID, MemberID: consumer.MemberID, PlacementRevision: consumer.PlacementRevision, Sequence: consumer.Sequence, Owner: "owner-incarnation"}
	f.children = []db.ProfileReferenceChild{{NetworkID: f.oldNetwork, ProjectID: consumer.ProjectID, Name: "old", Token: uuid.NewString()}, {NetworkID: f.newNetwork, ProjectID: consumer.ProjectID, Name: "new", Token: uuid.NewString()}}
	return f
}

func (f *referenceAccountingFixture) tx(t *testing.T, callback func(context.Context, *db.ClusterTx) error) {
	t.Helper()
	require.NoError(t, f.c.Transaction(f.ctx, callback))
}

func (f *referenceAccountingFixture) claim(t *testing.T) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, project.ClaimProfileReferenceApply(ctx, tx, f.identity))
		for _, child := range f.children {
			require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, "default", child.Name, child.Token, "nic"))
		}

		return tx.SealProfileReferenceChildren(ctx, f.identity, f.children)
	})
}

func (f *referenceAccountingFixture) count(t *testing.T, table string) int {
	t.Helper()
	count := 0
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.Tx().QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count)
	})
	return count
}

func TestProfileReferenceExactAcknowledgmentAndReceipt(t *testing.T) {
	f := newReferenceAccountingFixture(t)
	f.claim(t)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.MarkProfileReferenceApplied(ctx, f.identity)
	})
	usageCount := f.count(t, "profiles_reference_usage")
	for name, mutate := range map[string]func(*db.ProfileReferenceApply){
		"transition": func(id *db.ProfileReferenceApply) { id.ChangeToken = "wrong-transition" },
		"attempt":    func(id *db.ProfileReferenceApply) { id.Token = "wrong-attempt" },
		"sequence":   func(id *db.ProfileReferenceApply) { id.Sequence++ },
		"instance":   func(id *db.ProfileReferenceApply) { id.InstanceID++ },
		"project":    func(id *db.ProfileReferenceApply) { id.ProjectID++ },
		"member":     func(id *db.ProfileReferenceApply) { id.MemberID++ },
		"placement":  func(id *db.ProfileReferenceApply) { id.PlacementRevision++ },
		"owner":      func(id *db.ProfileReferenceApply) { id.Owner = "replacement-process" },
	} {
		t.Run(name, func(t *testing.T) {
			wrong := f.identity
			mutate(&wrong)
			err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error { return tx.FinalizeProfileReferenceApply(ctx, wrong) })
			require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
			require.Equal(t, usageCount, f.count(t, "profiles_reference_usage"))
			require.Zero(t, f.count(t, "profiles_reference_receipts"))
		})
	}

	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		tx.NodeID(f.identity.MemberID + 100)
		return tx.FinalizeProfileReferenceApply(ctx, f.identity)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.FinalizeProfileReferenceApply(ctx, f.identity)
	})
	require.Zero(t, f.count(t, "profiles_reference_usage"))
	require.Zero(t, f.count(t, "profiles_reference_children"))
	require.Equal(t, 1, f.count(t, "profiles_reference_receipts"))
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		// Completed history does not retain live network ownership.
		_, err := tx.Tx().ExecContext(ctx, `DELETE FROM networks WHERE id=?`, f.oldNetwork)
		require.NoError(t, err)
		_, err = tx.Tx().ExecContext(ctx, `DELETE FROM networks_acls WHERE id=?`, f.aclID)
		require.NoError(t, err)
		return tx.FinalizeProfileReferenceApply(ctx, f.identity)
	})
	second, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, project.ProfileReferenceUpdate{Project: "default", Name: "tracked", ProfileID: f.profileID, Generation: 1, Profile: api.ProfilePut{}}, nil)
	require.NoError(t, err)
	retained := f.count(t, "profiles_reference_usage")
	require.Positive(t, retained)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.FinalizeProfileReferenceApply(ctx, f.identity)
	})
	require.Equal(t, retained, f.count(t, "profiles_reference_usage"))
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		consumers, err := tx.ProfileReferenceConsumers(ctx, second.Token)
		require.NoError(t, err)
		require.False(t, consumers[0].Satisfied)
		return nil
	})
	wrong := f.identity
	wrong.Owner = "wrong-receipt-owner"
	err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error { return tx.FinalizeProfileReferenceApply(ctx, wrong) })
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
}

func TestProfileReferenceChildOwnershipAndTransactionalFinalization(t *testing.T) {
	for _, fault := range []string{"missing-operation", "replaced-token", "wrong-member", "wrong-operation", "notification", "missing-child", "rollback"} {
		t.Run(fault, func(t *testing.T) {
			f := newReferenceAccountingFixture(t)
			f.claim(t)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.MarkProfileReferenceApplied(ctx, f.identity)
			})
			usageCount := f.count(t, "profiles_reference_usage")
			rollback := errors.New("injected transaction rollback")
			err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				switch fault {
				case "missing-operation":
					_, err = tx.Tx().ExecContext(ctx, `DELETE FROM networks_ovn_operations WHERE name='new'`)
				case "replaced-token":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE networks_ovn_operations SET token='replacement' WHERE name='new'`)
				case "wrong-member":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE networks_ovn_operations SET node_id=node_id+100 WHERE name='new'`)
				case "wrong-operation":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE networks_ovn_operations SET operation='delete' WHERE name='new'`)
				case "notification":
					err = tx.AddOVNNotification(ctx, "pending-child-work", f.children[1].Token)
				case "missing-child":
					_, err = tx.Tx().ExecContext(ctx, `DELETE FROM profiles_reference_children WHERE network_id=?`, f.newNetwork)
				}

				require.NoError(t, err)
				err = tx.FinalizeProfileReferenceApply(ctx, f.identity)
				if fault == "rollback" {
					require.NoError(t, err)
					return rollback
				}

				require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
				var count int
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM profiles_reference_receipts`).Scan(&count))
				require.Zero(t, count)
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM profiles_reference_usage`).Scan(&count))
				require.Equal(t, usageCount, count)
				return err
			})
			require.Error(t, err)
			if fault == "rollback" {
				require.ErrorIs(t, err, rollback)
			}

			require.Equal(t, usageCount, f.count(t, "profiles_reference_usage"))
			require.Equal(t, 2, f.count(t, "profiles_reference_children"))
			require.Equal(t, 2, f.count(t, "networks_ovn_operations"))
			require.Zero(t, f.count(t, "profiles_reference_receipts"))
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				for _, child := range f.children {
					token, err := tx.OVNNetworkOperationToken(ctx, "default", child.Name)
					require.NoError(t, err)
					require.Equal(t, child.Token, token)
				}

				return nil
			})
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.FinalizeProfileReferenceApply(ctx, f.identity)
			})
			require.Zero(t, f.count(t, "profiles_reference_usage"))
			require.Zero(t, f.count(t, "networks_ovn_operations"))
		})
	}
}

func TestProfileReferenceSealedCleanFailureCanRetry(t *testing.T) {
	f := newReferenceAccountingFixture(t)
	f.claim(t)
	retained := f.count(t, "profiles_reference_usage")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.FailProfileReferenceApply(ctx, f.identity, db.ProfileReferenceRolledBack)
	})
	require.Equal(t, retained, f.count(t, "profiles_reference_usage"))
	require.Zero(t, f.count(t, "profiles_reference_children"))
	require.Zero(t, f.count(t, "networks_ovn_operations"))
	oldIdentity := f.identity
	f.identity.Token = uuid.NewString()
	for i := range f.children {
		f.children[i].Token = uuid.NewString()
	}

	f.claim(t)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, tx.MarkProfileReferenceApplied(ctx, f.identity))
		return tx.FinalizeProfileReferenceApply(ctx, f.identity)
	})
	require.Zero(t, f.count(t, "profiles_reference_usage"))
	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.FinalizeProfileReferenceApply(ctx, oldIdentity)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
}

func TestProfileReferenceAdmissionAndUnknownOutcome(t *testing.T) {
	f := newReferenceAccountingFixture(t)
	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		tx.NodeID(f.identity.MemberID + 100)
		return project.ClaimProfileReferenceApply(ctx, tx, f.identity)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	require.Zero(t, f.count(t, "profiles_reference_attempts"))
	f.claim(t)
	err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.SealProfileReferenceChildren(ctx, f.identity, f.children)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.FailProfileReferenceApply(ctx, f.identity, db.ProfileReferenceUnknown)
	})
	usageCount := f.count(t, "profiles_reference_usage")
	err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.FinalizeProfileReferenceApply(ctx, f.identity)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.FailProfileReferenceApply(ctx, f.identity, db.ProfileReferenceRolledBack)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	err = f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcquireOVNPeerOperation(ctx, "shared", "acl-config", false)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	require.Equal(t, usageCount, f.count(t, "profiles_reference_usage"))
	require.Zero(t, f.count(t, "profiles_reference_receipts"))
}

func TestProfileReferenceRetainedIdentityAndRestrict(t *testing.T) {
	f := newReferenceAccountingFixture(t)
	for _, statement := range []string{
		`DELETE FROM instances WHERE id=?`,
		`DELETE FROM profiles WHERE id=?`,
		`DELETE FROM projects WHERE id=?`,
		`DELETE FROM networks WHERE id=?`,
		`DELETE FROM networks_acls WHERE id=?`,
		`DELETE FROM nodes WHERE id=?`,
	} {
		ids := map[string]int64{`DELETE FROM instances WHERE id=?`: f.instanceID, `DELETE FROM profiles WHERE id=?`: f.profileID, `DELETE FROM projects WHERE id=?`: f.identity.ProjectID, `DELETE FROM networks WHERE id=?`: f.oldNetwork, `DELETE FROM networks_acls WHERE id=?`: f.aclID, `DELETE FROM nodes WHERE id=?`: f.identity.MemberID}
		err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			_, err := tx.Tx().ExecContext(ctx, statement, ids[statement])
			return err
		})
		require.Error(t, err, statement)
	}

	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, `UPDATE networks SET name='renamed-old' WHERE id=?`, f.oldNetwork)
		require.NoError(t, err)
		reused, err := tx.CreateNetwork(ctx, "default", "old", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		require.NotEqual(t, f.oldNetwork, reused)
		usage, err := tx.ProfileReferenceUsage(ctx, reused, 0)
		require.NoError(t, err)
		require.Empty(t, usage)
		usage, err = tx.ProfileReferenceUsage(ctx, f.oldNetwork, f.aclID)
		require.NoError(t, err)
		require.NotEmpty(t, usage)
		return nil
	})
}

func TestProfileReferenceInputsEqualProjectMetadata(t *testing.T) {
	base := db.ProfileReferenceSnapshot{
		Version: 1, InstanceID: 2, ProjectID: 3, MemberID: 4,
		Project: api.Project{Name: "tenant", UsedBy: []string{"/1.0/instances/a"}, ProjectPut: api.ProjectPut{Description: "original", Config: map[string]string{"user.note": "original", "features.profiles": "false", "future.setting": "exact"}}},
		Profiles: []db.ProfileReferenceProfile{
			{ID: 5, Generation: 6, Profile: api.Profile{Name: "first", Project: "default", ProfilePut: api.ProfilePut{Description: "profile", Config: map[string]string{"user.note": "profile"}, Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "net", "user.note": "device"}}}}},
			{ID: 7, Generation: 8, Profile: api.Profile{Name: "second", Project: "default"}},
		},
		LocalConfig: map[string]string{"user.note": "local"}, LocalDevices: map[string]map[string]string{"disk": {"type": "disk", "path": "/data", "user.note": "local-device"}},
		ExpandedConfig: map[string]string{"user.note": "expanded"}, ExpandedDevices: map[string]map[string]string{"eth0": {"type": "nic", "network": "net", "user.note": "expanded-device"}},
		Resources: []db.ProfileReferenceResource{
			{Device: "eth0", NetworkID: 9, NetworkProjectID: 10, NetworkProject: "default", NetworkName: "net", NetworkType: "ovn", ACLID: 11, ACLProjectID: 10, Config: map[string]string{"network": "net", "user.note": "resource"}},
			{Device: "eth1", NetworkID: 12, NetworkProjectID: 10, NetworkProject: "default", NetworkName: "other", NetworkType: "bridge"},
		},
	}

	for _, test := range []struct {
		name   string
		equal  bool
		mutate func(*db.ProfileReferenceSnapshot)
	}{
		{"description", true, func(s *db.ProfileReferenceSnapshot) { s.Project.Description = "edited" }},
		{"add-user", true, func(s *db.ProfileReferenceSnapshot) { s.Project.Config["user.new"] = "added" }},
		{"exact-user-prefix", true, func(s *db.ProfileReferenceSnapshot) { s.Project.Config["user."] = "added" }},
		{"update-user", true, func(s *db.ProfileReferenceSnapshot) { s.Project.Config["user.note"] = "edited" }},
		{"remove-user", true, func(s *db.ProfileReferenceSnapshot) { delete(s.Project.Config, "user.note") }},
		{"version", false, func(s *db.ProfileReferenceSnapshot) { s.Version++ }},
		{"instance-id", false, func(s *db.ProfileReferenceSnapshot) { s.InstanceID++ }},
		{"project-id", false, func(s *db.ProfileReferenceSnapshot) { s.ProjectID++ }},
		{"member-id", false, func(s *db.ProfileReferenceSnapshot) { s.MemberID++ }},
		{"project-name", false, func(s *db.ProfileReferenceSnapshot) { s.Project.Name = "renamed" }},
		{"project-used-by", false, func(s *db.ProfileReferenceSnapshot) { s.Project.UsedBy[0] = "/1.0/instances/b" }},
		{"project-limit", false, func(s *db.ProfileReferenceSnapshot) { s.Project.Config["limits.instances"] = "10" }},
		{"project-restriction", false, func(s *db.ProfileReferenceSnapshot) {
			s.Project.Config["restricted.containers.privilege"] = "unprivileged"
		}},
		{"project-feature", false, func(s *db.ProfileReferenceSnapshot) { s.Project.Config["features.profiles"] = "true" }},
		{"project-other-feature", false, func(s *db.ProfileReferenceSnapshot) { s.Project.Config["features.images"] = "false" }},
		{"project-unknown-key", false, func(s *db.ProfileReferenceSnapshot) { s.Project.Config["future.setting"] = "changed" }},
		{"project-key-removal", false, func(s *db.ProfileReferenceSnapshot) { delete(s.Project.Config, "future.setting") }},
		{"user-without-dot", false, func(s *db.ProfileReferenceSnapshot) { s.Project.Config["user"] = "strict" }},
		{"uppercase-user", false, func(s *db.ProfileReferenceSnapshot) { s.Project.Config["User.note"] = "strict" }},
		{"profile-id", false, func(s *db.ProfileReferenceSnapshot) { s.Profiles[0].ID++ }},
		{"profile-generation", false, func(s *db.ProfileReferenceSnapshot) { s.Profiles[0].Generation++ }},
		{"profile-order", false, func(s *db.ProfileReferenceSnapshot) { s.Profiles[0], s.Profiles[1] = s.Profiles[1], s.Profiles[0] }},
		{"profile-name", false, func(s *db.ProfileReferenceSnapshot) { s.Profiles[0].Profile.Name = "renamed" }},
		{"profile-project", false, func(s *db.ProfileReferenceSnapshot) { s.Profiles[0].Profile.Project = "tenant" }},
		{"profile-description", false, func(s *db.ProfileReferenceSnapshot) { s.Profiles[0].Profile.Description = "changed" }},
		{"profile-user-config", false, func(s *db.ProfileReferenceSnapshot) { s.Profiles[0].Profile.Config["user.note"] = "changed" }},
		{"profile-device-user-config", false, func(s *db.ProfileReferenceSnapshot) { s.Profiles[0].Profile.Devices["eth0"]["user.note"] = "changed" }},
		{"local-user-config", false, func(s *db.ProfileReferenceSnapshot) { s.LocalConfig["user.note"] = "changed" }},
		{"local-device-user-config", false, func(s *db.ProfileReferenceSnapshot) { s.LocalDevices["disk"]["user.note"] = "changed" }},
		{"expanded-user-config", false, func(s *db.ProfileReferenceSnapshot) { s.ExpandedConfig["user.note"] = "changed" }},
		{"expanded-device-user-config", false, func(s *db.ProfileReferenceSnapshot) { s.ExpandedDevices["eth0"]["user.note"] = "changed" }},
		{"resource-order", false, func(s *db.ProfileReferenceSnapshot) { s.Resources[0], s.Resources[1] = s.Resources[1], s.Resources[0] }},
		{"resource-device", false, func(s *db.ProfileReferenceSnapshot) { s.Resources[0].Device = "eth2" }},
		{"network-id", false, func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkID++ }},
		{"network-project-id", false, func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkProjectID++ }},
		{"network-project", false, func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkProject = "tenant" }},
		{"network-name", false, func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkName = "renamed" }},
		{"network-type", false, func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkType = "bridge" }},
		{"acl-id", false, func(s *db.ProfileReferenceSnapshot) { s.Resources[0].ACLID++ }},
		{"acl-project-id", false, func(s *db.ProfileReferenceSnapshot) { s.Resources[0].ACLProjectID++ }},
		{"resource-user-config", false, func(s *db.ProfileReferenceSnapshot) { s.Resources[0].Config["user.note"] = "changed" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(base)
			require.NoError(t, err)
			var changed db.ProfileReferenceSnapshot
			require.NoError(t, json.Unmarshal(data, &changed))
			// Every strict case also has otherwise permitted metadata drift.
			changed.Project.Description = "fresh metadata"
			changed.Project.Config["user.note"] = "fresh metadata"
			test.mutate(&changed)
			changedData, err := json.Marshal(changed)
			require.NoError(t, err)
			require.Equal(t, test.equal, db.ProfileReferenceInputsEqual(base, changed))
			require.Equal(t, test.equal, db.ProfileReferenceInputsEqual(changed, base))
			after, err := json.Marshal(base)
			require.NoError(t, err)
			require.Equal(t, data, after, "left caller snapshot mutated")
			after, err = json.Marshal(changed)
			require.NoError(t, err)
			require.Equal(t, changedData, after, "right caller snapshot mutated")
		})
	}

	for _, leftConfig := range []map[string]string{nil, {}, {"user.note": "left"}} {
		for _, rightConfig := range []map[string]string{nil, {}, {"user.other": "right"}} {
			left := db.ProfileReferenceSnapshot{Project: api.Project{ProjectPut: api.ProjectPut{Config: leftConfig}}}
			right := db.ProfileReferenceSnapshot{Project: api.Project{ProjectPut: api.ProjectPut{Config: rightConfig}}}
			leftData, err := json.Marshal(left)
			require.NoError(t, err)
			rightData, err := json.Marshal(right)
			require.NoError(t, err)
			require.True(t, db.ProfileReferenceInputsEqual(left, right))
			after, err := json.Marshal(left)
			require.NoError(t, err)
			require.Equal(t, leftData, after)
			after, err = json.Marshal(right)
			require.NoError(t, err)
			require.Equal(t, rightData, after)
		}
	}
	// Only filtered project config normalizes nil/empty; other representation stays exact.
	for name, mutate := range map[string]func(*db.ProfileReferenceSnapshot){
		"local-config":     func(s *db.ProfileReferenceSnapshot) { s.LocalConfig = map[string]string{} },
		"local-devices":    func(s *db.ProfileReferenceSnapshot) { s.LocalDevices = map[string]map[string]string{} },
		"expanded-config":  func(s *db.ProfileReferenceSnapshot) { s.ExpandedConfig = map[string]string{} },
		"expanded-devices": func(s *db.ProfileReferenceSnapshot) { s.ExpandedDevices = map[string]map[string]string{} },
		"profiles":         func(s *db.ProfileReferenceSnapshot) { s.Profiles = []db.ProfileReferenceProfile{} },
		"resources":        func(s *db.ProfileReferenceSnapshot) { s.Resources = []db.ProfileReferenceResource{} },
		"project-used-by":  func(s *db.ProfileReferenceSnapshot) { s.Project.UsedBy = []string{} },
	} {
		t.Run("nil-empty-"+name, func(t *testing.T) {
			var empty, changed db.ProfileReferenceSnapshot
			mutate(&changed)
			require.False(t, db.ProfileReferenceInputsEqual(empty, changed))
		})
	}
}

func TestProfileReferenceDBClaimRejectsFreshProjectMetadataTarget(t *testing.T) {
	f := newReferenceAccountingFixture(t)
	var before *db.ProfileReferenceState
	var consumers []db.ProfileReferenceConsumer
	var usage []db.ProfileReferenceUsage
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		record, err := cluster.GetProject(ctx, tx.Tx(), "default")
		require.NoError(t, err)
		current, err := record.ToAPI(ctx, tx.Tx())
		require.NoError(t, err)
		current.Description = "current metadata"
		current.Config["user.note"] = "current metadata"
		require.NoError(t, project.AllowProjectUpdate(tx, "default", current.Config, []string{"user.note"}))
		require.NoError(t, cluster.UpdateProject(ctx, tx.Tx(), "default", current.ProjectPut))
		before, err = tx.ProfileReferenceState(ctx, f.instanceID)
		require.NoError(t, err)
		consumers, err = tx.ProfileReferenceConsumers(ctx, f.commit.Token)
		require.NoError(t, err)
		usage, err = tx.ProfileReferenceUsage(ctx, 0, 0)
		return err
	})
	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		current, err := project.CaptureProfileReferenceSnapshot(ctx, tx, f.instanceID)
		require.NoError(t, err)
		require.NotEqual(t, before.Desired, *current)
		require.True(t, db.ProfileReferenceInputsEqual(before.Desired, *current))
		return tx.ClaimProfileReferenceApply(ctx, f.identity, *current)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	require.Zero(t, f.count(t, "profiles_reference_attempts"))
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		after, err := tx.ProfileReferenceState(ctx, f.instanceID)
		require.NoError(t, err)
		require.Equal(t, before, after)
		afterConsumers, err := tx.ProfileReferenceConsumers(ctx, f.commit.Token)
		require.NoError(t, err)
		require.Equal(t, consumers, afterConsumers)
		afterUsage, err := tx.ProfileReferenceUsage(ctx, 0, 0)
		require.NoError(t, err)
		require.Equal(t, usage, afterUsage)
		return nil
	})
}

type profileV1Mutation struct {
	name      string
	column    string
	value     any
	version   bool
	malformed bool
}

func profileV1SnapshotMutations(columns ...string) []profileV1Mutation {
	var mutations []profileV1Mutation
	for _, column := range columns {
		for _, version := range []int{0, 2, 99} {
			mutations = append(mutations, profileV1Mutation{name: fmt.Sprintf("%s-version-%d", column, version), column: column, value: version, version: true})
		}

		mutations = append(mutations, profileV1Mutation{name: column + "-malformed", column: column, value: "{", malformed: true})
	}

	return mutations
}

func profileV1StateMutations() []profileV1Mutation {
	mutations := []profileV1Mutation{
		{name: "contract-version", column: "contract_version", value: 2},
		{name: "input-revision", column: "input_revision", value: 1},
		{name: "materialized-observation", column: "materialized_observation_id", value: 1},
	}

	return append(mutations, profileV1SnapshotMutations("applied_snapshot", "desired_snapshot")...)
}

func profileV1AttemptMutations() []profileV1Mutation {
	mutations := []profileV1Mutation{
		{name: "contract-version", column: "contract_version", value: 2},
		{name: "claimed-revision", column: "claimed_input_revision", value: 1},
		{name: "result-revision", column: "result_input_revision", value: 1},
		{name: "observation-cutoff", column: "observation_cutoff", value: 1},
		{name: "generation-plan-object", column: "generation_plan", value: "{}"},
		{name: "generation-plan-space", column: "generation_plan", value: " "},
		{name: "result-object", column: "result_snapshot", value: "{}"},
		{name: "result-space", column: "result_snapshot", value: " "},
	}

	return append(mutations, profileV1SnapshotMutations("baseline_snapshot", "target_snapshot")...)
}

func (m profileV1Mutation) apply(t *testing.T, f *referenceAccountingFixture, table, key string, id any) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		value := m.value
		if m.version {
			var data string
			err := tx.Tx().QueryRowContext(ctx, "SELECT "+m.column+" FROM "+table+" WHERE "+key+"=?", id).Scan(&data)
			require.NoError(t, err)
			var snapshot db.ProfileReferenceSnapshot
			require.NoError(t, json.Unmarshal([]byte(data), &snapshot))
			version, ok := m.value.(int)
			require.True(t, ok, "snapshot version mutation must be an int")
			snapshot.Version = version
			encoded, err := json.Marshal(snapshot)
			require.NoError(t, err)
			value = string(encoded)
		}

		_, err := tx.Tx().ExecContext(ctx, "UPDATE "+table+" SET "+m.column+"=? WHERE "+key+"=?", value, id)
		return err
	})
}

type profileV1Observation struct {
	rows     map[string][]string
	usage    []db.ProfileReferenceUsage
	retained []db.RetainedProfileReference
}

func profileV1Observe(t *testing.T, f *referenceAccountingFixture) profileV1Observation {
	t.Helper()
	// Reuse the child fixture's complete SELECT * accounting/reservation snapshot.
	childFixture := &profileChildrenFixture{c: f.c, ctx: f.ctx}
	saved := profileV1Observation{rows: childFixture.savedRows(t)}
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		saved.usage, err = tx.ProfileReferenceUsage(ctx, 0, 0)
		require.NoError(t, err)
		saved.retained, err = tx.RetainedProfileReferenceUsage(ctx, db.NetworkReferenceFilter{})
		require.NoError(t, err)
		require.Len(t, saved.retained, len(saved.usage))
		return nil
	})
	return saved
}

// Read within a finalizer's callback to distinguish preflight from rollback after cleanup.
func profileV1RowsInTransaction(t *testing.T, ctx context.Context, tx *db.ClusterTx) map[string][]string {
	t.Helper()
	saved := map[string][]string{}
	for _, table := range []string{"instances_profile_reference_apply", "profiles_reference_changes", "profiles_reference_consumers", "profiles_reference_attempts", "profiles_reference_usage", "profiles_reference_children", "profiles_reference_receipts", "networks_ovn_operations", "networks_ovn_notifications"} {
		rows, err := tx.Tx().QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY rowid")
		require.NoError(t, err)
		columns, err := rows.Columns()
		require.NoError(t, err)
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}

			require.NoError(t, rows.Scan(pointers...))
			data, err := json.Marshal(values)
			require.NoError(t, err)
			saved[table] = append(saved[table], string(data))
		}

		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}

	return saved
}

func profileV1RequireError(t *testing.T, err error, malformed bool) {
	t.Helper()
	require.Error(t, err)
	if malformed {
		var syntax *json.SyntaxError
		require.ErrorAs(t, err, &syntax, "decoding failure must propagate")
		require.False(t, api.StatusErrorCheck(err, http.StatusConflict))
	} else {
		require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	}
}

func profileV1PrepareBoundary(t *testing.T, f *referenceAccountingFixture, boundary string) {
	t.Helper()
	switch boundary {
	case "reserve", "seal":
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			return project.ClaimProfileReferenceApply(ctx, tx, f.identity)
		})
		if boundary == "seal" {
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				for _, child := range f.children {
					require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, "default", child.Name, child.Token, "nic"))
				}

				return nil
			})
		}

	case "mark", "fail-unknown", "fail-before-dispatch", "fail-rolled-back", "finalize":
		f.claim(t)
		if boundary == "finalize" {
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.MarkProfileReferenceApplied(ctx, f.identity)
			})
		}
	}
}

func profileV1NextConsumer(f *referenceAccountingFixture) db.ProfileReferenceConsumer {
	consumer := f.commit.Consumers[0]
	consumer.Before = consumer.After
	consumer.Sequence++
	return consumer
}

func profileV1CallBoundary(t *testing.T, f *referenceAccountingFixture, boundary string) (map[string][]string, error) {
	t.Helper()
	if boundary == "reserve" {
		children, err := f.c.ReserveProfileReferenceChildren(f.ctx, f.identity)
		require.Nil(t, children, "unsupported state/attempt must expose no child tokens")
		return nil, err
	}

	var state *db.ProfileReferenceState
	var inputs *db.OrdinaryProfileReferenceUpdate
	var inside map[string][]string
	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		switch boundary {
		case "state":
			state, err = tx.ProfileReferenceState(ctx, f.instanceID)
		case "ordinary-capture":
			inputs, err = project.CaptureOrdinaryProfileReferenceInputs(ctx, tx, f.instanceID, []string{"tracked"})
		case "create":
			err = tx.CreateProfileReferenceChange(ctx, uuid.NewString(), f.profileID, f.identity.ProjectID, 2, api.ProfilePut{}, api.ProfilePut{}, []db.ProfileReferenceConsumer{profileV1NextConsumer(f)})
		case "db-claim":
			err = tx.ClaimProfileReferenceApply(ctx, f.identity, f.commit.Consumers[0].After)
		case "project-claim":
			err = project.ClaimProfileReferenceApply(ctx, tx, f.identity)
		case "seal":
			err = tx.SealProfileReferenceChildren(ctx, f.identity, f.children)
		case "mark":
			err = tx.MarkProfileReferenceApplied(ctx, f.identity)
		case "fail-unknown":
			err = tx.FailProfileReferenceApply(ctx, f.identity, db.ProfileReferenceUnknown)
		case "fail-before-dispatch":
			err = tx.FailProfileReferenceApply(ctx, f.identity, db.ProfileReferenceBeforeDispatch)
		case "fail-rolled-back":
			err = tx.FailProfileReferenceApply(ctx, f.identity, db.ProfileReferenceRolledBack)
		case "finalize":
			err = tx.FinalizeProfileReferenceApply(ctx, f.identity)
			inside = profileV1RowsInTransaction(t, ctx, tx)
		default:
			t.Fatalf("unknown production boundary %q", boundary)
		}

		return err
	})
	if boundary == "state" {
		require.Nil(t, state)
	}

	if boundary == "ordinary-capture" {
		require.Nil(t, inputs)
	}

	return inside, err
}

func TestProfileReferenceV1CompatibilityState(t *testing.T) {
	for _, boundary := range []string{"state", "ordinary-capture", "create", "db-claim", "project-claim", "reserve", "seal", "mark", "fail-unknown", "fail-before-dispatch", "fail-rolled-back", "finalize"} {
		t.Run(boundary, func(t *testing.T) {
			for _, mutation := range profileV1StateMutations() {
				t.Run(mutation.name, func(t *testing.T) {
					f := newReferenceAccountingFixture(t)
					profileV1PrepareBoundary(t, f, boundary)
					mutation.apply(t, f, "instances_profile_reference_apply", "instance_id", f.instanceID)
					before := profileV1Observe(t, f)
					require.NotEmpty(t, before.usage)
					inside, err := profileV1CallBoundary(t, f, boundary)
					profileV1RequireError(t, err, mutation.malformed)
					if boundary == "finalize" {
						require.Equal(t, before.rows, inside, "no finalizer mutation before callback returns")
					}

					require.Equal(t, before, profileV1Observe(t, f), "successful callback rollback and conservative usage")
				})
			}
		})
	}
}

func TestProfileReferenceV1CompatibilityAttempt(t *testing.T) {
	for _, boundary := range []string{"reserve", "seal", "mark", "fail-unknown", "fail-before-dispatch", "fail-rolled-back", "finalize"} {
		t.Run(boundary, func(t *testing.T) {
			for _, mutation := range profileV1AttemptMutations() {
				t.Run(mutation.name, func(t *testing.T) {
					f := newReferenceAccountingFixture(t)
					profileV1PrepareBoundary(t, f, boundary)
					mutation.apply(t, f, "profiles_reference_attempts", "token", f.identity.Token)
					before := profileV1Observe(t, f)
					require.NotEmpty(t, before.usage)
					inside, err := profileV1CallBoundary(t, f, boundary)
					profileV1RequireError(t, err, mutation.malformed)
					if boundary == "finalize" {
						require.Equal(t, before.rows, inside, "no finalizer mutation before callback returns")
					}

					require.Equal(t, before, profileV1Observe(t, f))
				})
			}
		})
	}
}

func TestProfileReferenceV1CompatibilityConsumerInputsAndRead(t *testing.T) {
	for _, field := range []string{"before", "after"} {
		for _, version := range []int{0, 2, 99} {
			t.Run(fmt.Sprintf("create-%s-%d", field, version), func(t *testing.T) {
				f := newReferenceAccountingFixture(t)
				before := profileV1Observe(t, f)
				consumer := profileV1NextConsumer(f)
				if field == "before" {
					consumer.Before.Version = version
				} else {
					consumer.After.Version = version
				}

				err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.CreateProfileReferenceChange(ctx, uuid.NewString(), f.profileID, f.identity.ProjectID, 2, api.ProfilePut{}, api.ProfilePut{}, []db.ProfileReferenceConsumer{consumer})
				})
				profileV1RequireError(t, err, false)
				require.Equal(t, before, profileV1Observe(t, f), "caller returns error through actual transaction")
			})
		}
	}
	t.Run("late-consumer-rollback", func(t *testing.T) {
		f := newReferenceAccountingFixture(t)
		first := profileV1NextConsumer(f)
		second := first
		second.Sequence++
		second.After.Version = 2
		before := profileV1Observe(t, f)
		var during map[string][]string
		err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			err := tx.CreateProfileReferenceChange(ctx, uuid.NewString(), f.profileID, f.identity.ProjectID, 2, api.ProfilePut{}, api.ProfilePut{}, []db.ProfileReferenceConsumer{first, second})
			during = profileV1RowsInTransaction(t, ctx, tx)
			return err
		})
		profileV1RequireError(t, err, false)
		require.Greater(t, len(during["profiles_reference_consumers"]), len(before.rows["profiles_reference_consumers"]), "earlier consumer wrote before later rejection")
		require.Equal(t, before, profileV1Observe(t, f), "successful callback rollback preserves earlier writes")
	})
	for _, mutation := range profileV1SnapshotMutations("before_snapshot", "after_snapshot") {
		t.Run("read-"+mutation.name, func(t *testing.T) {
			f := newReferenceAccountingFixture(t)
			mutation.apply(t, f, "profiles_reference_consumers", "id", f.commit.Consumers[0].ID)
			before := profileV1Observe(t, f)
			var consumers []db.ProfileReferenceConsumer
			err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				consumers, err = tx.ProfileReferenceConsumers(ctx, f.commit.Token)
				return err
			})
			profileV1RequireError(t, err, mutation.malformed)
			require.Nil(t, consumers)
			require.Equal(t, before, profileV1Observe(t, f))
		})
	}
}

func profileV1Complete(t *testing.T, f *referenceAccountingFixture) {
	t.Helper()
	f.claim(t)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.MarkProfileReferenceApplied(ctx, f.identity)
		if err != nil {
			return err
		}

		return tx.FinalizeProfileReferenceApply(ctx, f.identity)
	})
}

func TestProfileReferenceV1CompatibilityCompleted(t *testing.T) {
	for _, mutation := range profileV1AttemptMutations() {
		t.Run("attempt-"+mutation.name, func(t *testing.T) {
			f := newReferenceAccountingFixture(t)
			profileV1Complete(t, f)
			mutation.apply(t, f, "profiles_reference_attempts", "token", f.identity.Token)
			before := profileV1Observe(t, f)
			inside, err := profileV1CallBoundary(t, f, "finalize")
			profileV1RequireError(t, err, mutation.malformed)
			require.Equal(t, before.rows, inside)
			require.Equal(t, before, profileV1Observe(t, f))
		})
	}

	for _, column := range []string{"result_snapshot", "post_commit_evidence"} {
		for _, value := range []string{"{}", " "} {
			t.Run("receipt-"+column+"-"+fmt.Sprintf("%q", value), func(t *testing.T) {
				f := newReferenceAccountingFixture(t)
				profileV1Complete(t, f)
				mutation := profileV1Mutation{column: column, value: value}
				mutation.apply(t, f, "profiles_reference_receipts", "attempt_token", f.identity.Token)
				before := profileV1Observe(t, f)
				inside, err := profileV1CallBoundary(t, f, "finalize")
				profileV1RequireError(t, err, false)
				require.Equal(t, before.rows, inside)
				require.Equal(t, before, profileV1Observe(t, f))
			})
		}
	}
	t.Run("missing-receipt", func(t *testing.T) {
		f := newReferenceAccountingFixture(t)
		profileV1Complete(t, f)
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			_, err := tx.Tx().ExecContext(ctx, `DELETE FROM profiles_reference_receipts WHERE attempt_token=?`, f.identity.Token)
			return err
		})
		before := profileV1Observe(t, f)
		inside, err := profileV1CallBoundary(t, f, "finalize")
		profileV1RequireError(t, err, false)
		require.Equal(t, before.rows, inside)
		require.Equal(t, before, profileV1Observe(t, f))
	})
	t.Run("historical-after-state-and-placement-advance", func(t *testing.T) {
		f := newReferenceAccountingFixture(t)
		profileV1Complete(t, f)
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.FinalizeProfileReferenceApply(ctx, f.identity)
		})
		_, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, project.ProfileReferenceUpdate{Project: "default", Name: "tracked", ProfileID: f.profileID, Generation: 1, Profile: api.ProfilePut{}}, nil)
		require.NoError(t, err)
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			member, err := tx.CreateNode("later-member", "192.0.2.99")
			require.NoError(t, err)
			_, err = tx.Tx().ExecContext(ctx, `UPDATE instances SET node_id=? WHERE id=?`, member, f.instanceID)
			require.NoError(t, err)
			_, err = tx.Tx().ExecContext(ctx, `UPDATE instances_profile_reference_apply SET member_id=?, placement_revision=2, contract_version=2, input_revision=7, materialized_observation_id=9, active_token='later-owner' WHERE instance_id=?`, member, f.instanceID)
			return err
		})
		for _, column := range []string{"applied_snapshot", "desired_snapshot"} {
			profileV1Mutation{column: column, value: 2, version: true}.apply(t, f, "instances_profile_reference_apply", "instance_id", f.instanceID)
		}

		before := profileV1Observe(t, f)
		require.NotEmpty(t, before.usage, "later pending usage remains protected")
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			tx.NodeID(f.identity.MemberID + 100)
			return tx.FinalizeProfileReferenceApply(ctx, f.identity)
		})
		require.Equal(t, before, profileV1Observe(t, f), "historical receipt is independent of live ownership")
	})
}

func profileV1RetryHistory(t *testing.T) (*referenceAccountingFixture, db.ProfileReferenceApply) {
	t.Helper()
	f := newReferenceAccountingFixture(t)
	f.claim(t)
	old := f.identity
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.FailProfileReferenceApply(ctx, old, db.ProfileReferenceRolledBack)
	})
	f.identity.Token = uuid.NewString()
	f.identity.ChangeToken = ""
	f.identity.Sequence++
	for i := range f.children {
		f.children[i].Token = uuid.NewString()
	}

	f.claim(t)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.MarkProfileReferenceApplied(ctx, f.identity)
	})
	return f, old
}

func TestProfileReferenceV1CompatibilityFinalizeHistory(t *testing.T) {
	for _, mutation := range profileV1AttemptMutations() {
		t.Run("retryable-"+mutation.name, func(t *testing.T) {
			f, old := profileV1RetryHistory(t)
			mutation.apply(t, f, "profiles_reference_attempts", "token", old.Token)
			before := profileV1Observe(t, f)
			require.NotEmpty(t, before.rows["profiles_reference_children"])
			require.NotEmpty(t, before.rows["networks_ovn_operations"])
			inside, err := profileV1CallBoundary(t, f, "finalize")
			profileV1RequireError(t, err, mutation.malformed)
			require.Equal(t, before.rows, inside, "historical preflight must precede child and usage cleanup")
			require.Equal(t, before, profileV1Observe(t, f))
		})
	}

	for _, mutation := range profileV1SnapshotMutations("before_snapshot", "after_snapshot") {
		t.Run("consumer-"+mutation.name, func(t *testing.T) {
			f, _ := profileV1RetryHistory(t)
			mutation.apply(t, f, "profiles_reference_consumers", "id", f.commit.Consumers[0].ID)
			before := profileV1Observe(t, f)
			inside, err := profileV1CallBoundary(t, f, "finalize")
			profileV1RequireError(t, err, mutation.malformed)
			require.Equal(t, before.rows, inside, "covered consumer preflight must precede child and usage cleanup")
			require.Equal(t, before, profileV1Observe(t, f))
		})
	}
}

func TestProfileReferenceV1CompatibilityHistoricalExclusions(t *testing.T) {
	for _, exclusion := range []string{"instance", "newer", "member", "placement", "usage-free", "completed"} {
		t.Run(exclusion, func(t *testing.T) {
			f, old := profileV1RetryHistory(t)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_attempts SET contract_version=2 WHERE token=?`, old.Token)
				require.NoError(t, err)
				switch exclusion {
				case "instance":
					var node string
					require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, f.identity.MemberID).Scan(&node))
					instance, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "unrelated", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
					require.NoError(t, err)
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_attempts SET instance_id=? WHERE token=?`, instance, old.Token)
					require.NoError(t, err)
				case "newer":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_attempts SET sequence=? WHERE token=?`, f.identity.Sequence+1, old.Token)
				case "member":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_attempts SET member_id=member_id+100 WHERE token=?`, old.Token)
				case "placement":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_attempts SET placement_revision=placement_revision+1 WHERE token=?`, old.Token)
				case "usage-free":
					_, err = tx.Tx().ExecContext(ctx, `DELETE FROM profiles_reference_usage WHERE attempt_token=?`, old.Token)
				case "completed":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_attempts SET phase='completed' WHERE token=?`, old.Token)
				}

				return err
			})
			before := profileV1Observe(t, f)
			var oldUsage []db.ProfileReferenceUsage
			var oldRetained []db.RetainedProfileReference
			for _, usage := range before.usage {
				if usage.AttemptToken == old.Token {
					oldUsage = append(oldUsage, usage)
				}
			}
			for _, usage := range before.retained {
				if usage.AttemptToken == old.Token {
					oldRetained = append(oldRetained, usage)
				}
			}
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.FinalizeProfileReferenceApply(ctx, f.identity)
			})
			after := profileV1Observe(t, f)
			require.Equal(t, before.rows["profiles_reference_attempts"][0], after.rows["profiles_reference_attempts"][0], "excluded historical attempt remains byte-identical")
			require.Equal(t, oldUsage, append([]db.ProfileReferenceUsage(nil), after.usage...))
			require.Equal(t, oldRetained, append([]db.RetainedProfileReference(nil), after.retained...))
			require.Empty(t, after.rows["profiles_reference_children"])
			require.Empty(t, after.rows["networks_ovn_operations"])
		})
	}
}

func TestProfileReferenceV1CompatibilityConsumerExclusions(t *testing.T) {
	for _, exclusion := range []string{"instance", "newer", "member", "placement", "satisfied", "covered-without-usage"} {
		t.Run(exclusion, func(t *testing.T) {
			f := newReferenceAccountingFixture(t)
			profileV1PrepareBoundary(t, f, "finalize")
			consumerID := f.commit.Consumers[0].ID
			profileV1Mutation{column: "after_snapshot", value: 2, version: true}.apply(t, f, "profiles_reference_consumers", "id", consumerID)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				switch exclusion {
				case "instance":
					var node string
					require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, f.identity.MemberID).Scan(&node))
					instance, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "unrelated-consumer", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
					require.NoError(t, err)
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_consumers SET instance_id=? WHERE id=?`, instance, consumerID)
					require.NoError(t, err)
				case "newer":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_consumers SET sequence=? WHERE id=?`, f.identity.Sequence+1, consumerID)
				case "member":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_consumers SET member_id=member_id+100 WHERE id=?`, consumerID)
				case "placement":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_consumers SET placement_revision=placement_revision+1 WHERE id=?`, consumerID)
				case "satisfied":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_consumers SET satisfied=1 WHERE id=?`, consumerID)
				case "covered-without-usage":
					_, err = tx.Tx().ExecContext(ctx, `DELETE FROM profiles_reference_usage WHERE consumer_id=?`, consumerID)
				}

				return err
			})
			before := profileV1Observe(t, f)
			inside, err := profileV1CallBoundary(t, f, "finalize")
			if exclusion == "covered-without-usage" {
				profileV1RequireError(t, err, false)
				require.Equal(t, before.rows, inside)
				require.Equal(t, before, profileV1Observe(t, f))
				return
			}

			require.NoError(t, err)
			after := profileV1Observe(t, f)
			require.Equal(t, before.rows["profiles_reference_consumers"], after.rows["profiles_reference_consumers"])
			require.Equal(t, before.rows["profiles_reference_changes"], after.rows["profiles_reference_changes"], "no covered consumers means no completion update")
			var consumerUsage []db.ProfileReferenceUsage
			var consumerRetained []db.RetainedProfileReference
			for _, usage := range before.usage {
				if usage.ConsumerID == consumerID {
					consumerUsage = append(consumerUsage, usage)
				}
			}
			for _, usage := range before.retained {
				if usage.ConsumerID == consumerID {
					consumerRetained = append(consumerRetained, usage)
				}
			}
			require.Equal(t, consumerUsage, after.usage)
			require.Equal(t, consumerRetained, after.retained)
		})
	}
}

// Deliberate historical fixtures make unrelated completion writes observable.
func profileV1UnrelatedChanges(t *testing.T, f *referenceAccountingFixture) []string {
	t.Helper()
	tokens := []string{uuid.NewString(), uuid.NewString()}
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		for i, token := range tokens {
			_, err := tx.Tx().ExecContext(ctx, `INSERT INTO profiles_reference_changes (token, profile_id, project_id, generation, old_profile, new_profile, completed) VALUES (?, ?, ?, ?, '{}', '{}', 0)`, token, f.profileID, f.identity.ProjectID, 100+i)
			require.NoError(t, err)
			snapshot := f.commit.Consumers[0].After
			snapshot.Version = 2
			data, err := json.Marshal(snapshot)
			require.NoError(t, err)
			_, err = tx.Tx().ExecContext(ctx, `INSERT INTO profiles_reference_consumers (change_token, instance_id, project_id, member_id, placement_revision, sequence, before_snapshot, after_snapshot, satisfied) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, token, f.instanceID, f.identity.ProjectID, f.identity.MemberID, f.identity.PlacementRevision, 100+i, string(data), string(data), i == 0)
			require.NoError(t, err)
		}

		return nil
	})
	return tokens
}

func profileV1RequireChange(t *testing.T, f *referenceAccountingFixture, token string, completed bool) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var actual bool
		err := tx.Tx().QueryRowContext(ctx, `SELECT completed FROM profiles_reference_changes WHERE token=?`, token).Scan(&actual)
		require.NoError(t, err)
		require.Equal(t, completed, actual, "change %s", token)
		return nil
	})
}

func profileV1ApplyWithReservation(t *testing.T, f *referenceAccountingFixture) {
	t.Helper()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.ClaimProfileReferenceApply(ctx, tx, f.identity)
	})
	children, err := f.c.ReserveProfileReferenceChildren(f.ctx, f.identity)
	require.NoError(t, err)
	require.NotEmpty(t, children)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.MarkProfileReferenceApplied(ctx, f.identity)
		if err != nil {
			return err
		}

		return tx.FinalizeProfileReferenceApply(ctx, f.identity)
	})
}

func TestProfileReferenceV1CompatibilityCompletionScope(t *testing.T) {
	t.Run("shared-profile", func(t *testing.T) {
		f := newReferenceAccountingFixture(t)
		var secondInstance int64
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			var node string
			require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, f.identity.MemberID).Scan(&node))
			var err error
			secondInstance, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: "second-consumer", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
			require.NoError(t, err)
			require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(secondInstance), "default", []string{"tracked"}))
			snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, secondInstance)
			require.NoError(t, err)
			return tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1)
		})
		commit, err := project.CommitProfileReferenceUpdate(f.ctx, f.c, project.ProfileReferenceUpdate{Project: "default", Name: "tracked", ProfileID: f.profileID, Generation: 1, Profile: api.ProfilePut{Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "old-acl"}}}}, nil)
		require.NoError(t, err)
		require.Len(t, commit.Consumers, 2)
		unrelated := profileV1UnrelatedChanges(t, f)
		for i, consumer := range commit.Consumers {
			current := *f
			current.instanceID = consumer.InstanceID
			current.identity = db.ProfileReferenceApply{Token: uuid.NewString(), ChangeToken: commit.Token, InstanceID: consumer.InstanceID, ProjectID: consumer.ProjectID, MemberID: consumer.MemberID, PlacementRevision: consumer.PlacementRevision, Sequence: consumer.Sequence, Owner: "shared-consumer-owner"}
			profileV1ApplyWithReservation(t, &current)
			profileV1RequireChange(t, f, commit.Token, i == 1)
			for _, token := range unrelated {
				profileV1RequireChange(t, f, token, false)
			}

			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				consumers, err := tx.ProfileReferenceConsumers(ctx, commit.Token)
				require.NoError(t, err)
				for j, saved := range consumers {
					require.Equal(t, j <= i, saved.Satisfied)
				}

				usage, err := tx.ProfileReferenceUsage(ctx, 0, 0)
				require.NoError(t, err)
				if i == 0 {
					require.NotEmpty(t, usage)
					for _, owner := range usage {
						require.Equal(t, secondInstance, owner.InstanceID)
					}
				} else {
					require.Empty(t, usage)
				}

				return nil
			})
		}
	})
	t.Run("zero-covered-consumers", func(t *testing.T) {
		f := newReferenceAccountingFixture(t)
		profileV1Complete(t, f)
		unrelated := profileV1UnrelatedChanges(t, f)
		f.identity.Token = uuid.NewString()
		f.identity.ChangeToken = ""
		f.identity.Sequence++
		before := profileV1Observe(t, f)
		profileV1ApplyWithReservation(t, f)
		after := profileV1Observe(t, f)
		require.Equal(t, before.rows["profiles_reference_changes"], after.rows["profiles_reference_changes"])
		require.Equal(t, before.rows["profiles_reference_consumers"], after.rows["profiles_reference_consumers"])
		for _, token := range unrelated {
			profileV1RequireChange(t, f, token, false)
		}
	})
}

func TestProfileReferenceV1CompatibilityBaselineAndTarget(t *testing.T) {
	for _, version := range []int{0, 2, 99} {
		t.Run(fmt.Sprintf("initialize-%d", version), func(t *testing.T) {
			f := newReferenceAccountingFixture(t)
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.Tx().ExecContext(ctx, `DELETE FROM instances_profile_reference_apply WHERE instance_id=?`, f.instanceID)
				return err
			})
			before := profileV1Observe(t, f)
			snapshot := f.commit.Consumers[0].Before
			snapshot.Version = version
			err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.InitializeProfileReferenceBaseline(ctx, snapshot, 1)
			})
			profileV1RequireError(t, err, false)
			require.Equal(t, before, profileV1Observe(t, f))
			_, err = profileV1CallBoundary(t, f, "state")
			profileV1RequireError(t, err, false)
			require.Equal(t, before, profileV1Observe(t, f), "missing legacy baseline is never synthesized")
		})
		t.Run(fmt.Sprintf("claim-target-%d", version), func(t *testing.T) {
			f := newReferenceAccountingFixture(t)
			before := profileV1Observe(t, f)
			target := f.commit.Consumers[0].After
			target.Version = version
			err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.ClaimProfileReferenceApply(ctx, f.identity, target)
			})
			profileV1RequireError(t, err, false)
			require.Equal(t, before, profileV1Observe(t, f))
		})
	}
}

func TestProfileReferenceV1CompatibilityValidLifecycle(t *testing.T) {
	f := newReferenceAccountingFixture(t)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var version, revision, observation int64
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT contract_version, input_revision, materialized_observation_id FROM instances_profile_reference_apply WHERE instance_id=?`, f.instanceID).Scan(&version, &revision, &observation))
		require.EqualValues(t, 1, version)
		require.Zero(t, revision)
		require.Zero(t, observation)
		return project.ClaimProfileReferenceApply(ctx, tx, f.identity)
	})
	children, err := f.c.ReserveProfileReferenceChildren(f.ctx, f.identity)
	require.NoError(t, err)
	require.Len(t, children, 2)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var version, claimed, result, cutoff int64
		var plan, snapshot string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT contract_version, claimed_input_revision, result_input_revision, observation_cutoff, generation_plan, result_snapshot FROM profiles_reference_attempts WHERE token=?`, f.identity.Token).Scan(&version, &claimed, &result, &cutoff, &plan, &snapshot))
		require.EqualValues(t, 1, version)
		require.Zero(t, claimed)
		require.Zero(t, result)
		require.Zero(t, cutoff)
		require.Empty(t, plan)
		require.Empty(t, snapshot)
		return tx.MarkProfileReferenceApplied(ctx, f.identity)
	})
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.FinalizeProfileReferenceApply(ctx, f.identity)
	})
	before := profileV1Observe(t, f)
	require.Empty(t, before.usage)
	require.Empty(t, before.retained)
	require.Empty(t, before.rows["profiles_reference_children"])
	require.Empty(t, before.rows["networks_ovn_operations"])
	profileV1RequireChange(t, f, f.commit.Token, true)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var result, evidence, phase string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT result_snapshot, post_commit_evidence FROM profiles_reference_receipts WHERE attempt_token=?`, f.identity.Token).Scan(&result, &evidence))
		require.Empty(t, result)
		require.Empty(t, evidence)
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT phase FROM profiles_reference_attempts WHERE token=?`, f.identity.Token).Scan(&phase))
		require.Equal(t, "completed", phase)
		state, err := tx.ProfileReferenceState(ctx, f.instanceID)
		require.NoError(t, err)
		require.Equal(t, f.identity.Sequence, state.AppliedSequence)
		require.Equal(t, f.identity.Sequence, state.DesiredSequence)
		require.Equal(t, f.commit.Consumers[0].After, state.Applied)
		require.Equal(t, state.Applied, state.Desired)
		require.Empty(t, state.ActiveToken)
		return tx.FinalizeProfileReferenceApply(ctx, f.identity)
	})
	require.Equal(t, before, profileV1Observe(t, f))
}

func TestProfileReferenceV1CompatibilitySQLErrors(t *testing.T) {
	for _, boundary := range []string{"state", "consumer", "attempt", "receipt", "history"} {
		t.Run(boundary, func(t *testing.T) {
			f := newReferenceAccountingFixture(t)
			table := "instances_profile_reference_apply"
			switch boundary {
			case "consumer":
				table = "profiles_reference_consumers"
			case "attempt":
				profileV1PrepareBoundary(t, f, "mark")
				table = "profiles_reference_attempts"
			case "receipt":
				profileV1Complete(t, f)
				table = "profiles_reference_receipts"
			case "history":
				profileV1PrepareBoundary(t, f, "finalize")
				table = "profiles_reference_usage"
			}

			before := profileV1Observe(t, f)
			err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.Tx().ExecContext(ctx, "ALTER TABLE "+table+" RENAME TO unavailable_profile_v1_table")
				require.NoError(t, err)
				switch boundary {
				case "state":
					state, err := tx.ProfileReferenceState(ctx, f.instanceID)
					require.Nil(t, state)
					return err
				case "consumer":
					consumers, err := tx.ProfileReferenceConsumers(ctx, f.commit.Token)
					require.Nil(t, consumers)
					return err
				case "attempt":
					return tx.MarkProfileReferenceApplied(ctx, f.identity)
				default:
					return tx.FinalizeProfileReferenceApply(ctx, f.identity)
				}
			})
			require.ErrorContains(t, err, "no such table")
			require.False(t, api.StatusErrorCheck(err, http.StatusConflict))
			require.Equal(t, before, profileV1Observe(t, f), "SQL error propagates through successful callback rollback")
		})
	}
}
