//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/shared/api"
)

type profileChildrenFixture struct {
	c          *db.Cluster
	ctx        context.Context
	identity   db.ProfileReferenceApply
	networks   map[string]int64
	projects   map[string]int64
	before     db.ProfileReferenceSnapshot
	after      db.ProfileReferenceSnapshot
	project    string
	profileID  int64
	instanceID int64
}

// Every positive fixture captures real expanded devices and catalog identities, commits a real
// profile transition and claims through the project API. The same-name fixture instead admits an
// ordinary DB attempt across an explicit project config change, using separately captured targets.
func newProfileChildrenFixture(t *testing.T, mode string) *profileChildrenFixture {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	f := &profileChildrenFixture{c: c, ctx: context.Background(), networks: map[string]int64{}, projects: map[string]int64{}, project: "default"}
	beforeDevices := map[string]map[string]string{
		"move":      {"type": "nic", "network": "old", "security.acls": "acl-a,acl-b"},
		"old-copy":  {"type": "nic", "network": "old"},
		"stay":      {"type": "nic", "network": "stable", "security.acls": "acl-a,acl-b"},
		"stay-copy": {"type": "nic", "network": "stable"},
	}

	afterDevices := map[string]map[string]string{
		"move":      {"type": "nic", "network": "new"},
		"stay":      {"type": "nic", "network": "stable", "security.acls": "acl-a,acl-b"},
		"stay-copy": {"type": "nic", "network": "stable"},
	}

	if mode == "whole-device" {
		// The local device replaces this entire inherited device; none of these values may leak.
		beforeDevices["shadow"] = map[string]string{"type": "nic", "network": "shadowed", "security.acls": "acl-a", "mtu": "1234"}
		afterDevices["shadow"] = beforeDevices["shadow"]
	}

	if mode == "empty" {
		beforeDevices = map[string]map[string]string{}
		afterDevices = map[string]map[string]string{}
	}

	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		f.projects["default"], err = cluster.GetProjectID(ctx, tx.Tx(), "default")
		require.NoError(t, err)
		projectNames := []string{"default"}
		if mode == "shared" || mode == "same-name" {
			f.project = "tenant"
			f.projects["tenant"], err = cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "tenant"})
			require.NoError(t, err)
			require.NoError(t, cluster.CreateProjectConfig(ctx, tx.Tx(), f.projects["tenant"], map[string]string{"features.profiles": "false", "features.networks": "true", "restricted": "true", "restricted.networks.access": "old,stable,new"}))
			projectNames = append(projectNames, "tenant")
		}

		for _, projectName := range projectNames {
			networkNames := []string{"old", "stable", "new"}
			if mode == "whole-device" {
				networkNames = append(networkNames, "shadowed")
			}

			for _, name := range networkNames {
				kind := db.NetworkTypeOVN
				if mode == "bridge" {
					kind = db.NetworkTypeBridge
				}

				f.networks[projectName+"/"+name], err = tx.CreateNetwork(ctx, projectName, name, "", kind, nil)
				require.NoError(t, err)
			}

			for _, name := range []string{"acl-a", "acl-b"} {
				_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: projectName, Name: name})
				require.NoError(t, err)
			}
		}
		f.profileID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "child-union"})
		require.NoError(t, err)
		devices, err := cluster.APIToDevices(beforeDevices)
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), f.profileID, devices))
		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		f.instanceID, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: f.project, Name: "child-consumer", Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(f.instanceID), f.project, []string{"child-union"}))
		if mode == "whole-device" {
			local, err := cluster.APIToDevices(map[string]map[string]string{"shadow": {"type": "nic", "network": "stable"}})
			require.NoError(t, err)
			require.NoError(t, cluster.UpdateInstanceDevices(ctx, tx.Tx(), f.instanceID, local))
		}

		snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, f.instanceID)
		require.NoError(t, err)
		f.before = *snapshot
		return tx.InitializeProfileReferenceBaseline(ctx, *snapshot, 1)
	})
	if mode == "same-name" {
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			require.NoError(t, cluster.UpdateProject(ctx, tx.Tx(), "tenant", api.ProjectPut{Config: map[string]string{"features.profiles": "false", "features.networks": "true"}}))
			snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, f.instanceID)
			require.NoError(t, err)
			f.after = *snapshot
			require.NoError(t, project.ValidateDeviceNetworkReferences(ctx, tx, f.project, deviceConfig.NewDevices(snapshot.ExpandedDevices)))
			f.identity = db.ProfileReferenceApply{Token: uuid.NewString(), InstanceID: f.instanceID, ProjectID: snapshot.ProjectID, MemberID: snapshot.MemberID, PlacementRevision: 1, Sequence: 1, Owner: "child-owner"}
			return tx.ClaimProfileReferenceApply(ctx, f.identity, *snapshot)
		})
		return f
	}

	updated := api.ProfilePut{Devices: afterDevices}
	if mode == "empty" {
		// A real config-only transition has a consumer while both resource sets remain empty.
		updated.Config = map[string]string{"user.child-transition": "target"}
	}

	commit, err := project.CommitProfileReferenceUpdate(f.ctx, c, project.ProfileReferenceUpdate{Project: f.project, Name: "child-union", ProfileID: f.profileID, Profile: updated}, nil)
	require.NoError(t, err)
	require.Len(t, commit.Consumers, 1)
	consumer := commit.Consumers[0]
	f.after = consumer.After
	f.identity = db.ProfileReferenceApply{Token: uuid.NewString(), ChangeToken: commit.Token, InstanceID: f.instanceID, ProjectID: consumer.ProjectID, MemberID: consumer.MemberID, PlacementRevision: consumer.PlacementRevision, Sequence: consumer.Sequence, Owner: "child-owner"}
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.ClaimProfileReferenceApply(ctx, tx, f.identity)
	})
	return f
}

func (f *profileChildrenFixture) tx(t *testing.T, callback func(context.Context, *db.ClusterTx) error) {
	t.Helper()
	require.NoError(t, f.c.Transaction(f.ctx, callback))
}

// savedRows compares complete persisted values, including usage, attempt snapshots and foreign tokens.
func (f *profileChildrenFixture) savedRows(t *testing.T) map[string][]string {
	t.Helper()
	saved := map[string][]string{}
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
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

		return nil
	})
	return saved
}

func (f *profileChildrenFixture) expected(projectName string, names ...string) []db.ProfileReferenceChildNetwork {
	result := make([]db.ProfileReferenceChildNetwork, 0, len(names))
	for _, name := range names {
		result = append(result, db.ProfileReferenceChildNetwork{NetworkID: f.networks[projectName+"/"+name], ProjectID: f.projects[projectName], ProjectName: projectName, Name: name})
	}

	return result
}

func (f *profileChildrenFixture) assertReserved(t *testing.T, expected []db.ProfileReferenceChildNetwork) []db.ProfileReferenceChildNetwork {
	t.Helper()
	result, err := f.c.ReserveProfileReferenceChildren(f.ctx, f.identity)
	require.NoError(t, err)
	actual := slices.Clone(result)
	for i := range actual {
		actual[i].Token = ""
	}
	// This exact assertion is the mutation-sensitivity oracle for the production union.
	require.Equal(t, expected, actual, "complete persisted baseline-plus-target OVN union")
	tokens := map[string]bool{f.identity.Token: true}
	for _, child := range result {
		require.NotEmpty(t, child.Token)
		require.False(t, tokens[child.Token], "every child token must be distinct")
		tokens[child.Token] = true
	}

	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var sealed bool
		var phase, baseline, target, childSet string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT children_sealed, phase, baseline_snapshot, target_snapshot, child_set FROM profiles_reference_attempts WHERE token=?`, f.identity.Token).Scan(&sealed, &phase, &baseline, &target, &childSet))
		require.True(t, sealed)
		require.Equal(t, "applying", phase)
		var before, after db.ProfileReferenceSnapshot
		require.NoError(t, json.Unmarshal([]byte(baseline), &before))
		require.NoError(t, json.Unmarshal([]byte(target), &after))
		require.Equal(t, f.before, before)
		require.Equal(t, f.after, after)
		var children []db.ProfileReferenceChild
		require.NoError(t, json.Unmarshal([]byte(childSet), &children))
		require.Len(t, children, len(result))
		for i, child := range result {
			require.Equal(t, db.ProfileReferenceChild{NetworkID: child.NetworkID, ProjectID: child.ProjectID, Name: child.Name, Token: child.Token}, children[i])
			var token, operation string
			var member int64
			var fenced bool
			require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT token, node_id, operation, backend_fenced FROM networks_ovn_operations WHERE project_id=? AND name=?`, child.ProjectID, child.Name).Scan(&token, &member, &operation, &fenced))
			require.Equal(t, child.Token, token)
			require.Equal(t, f.identity.MemberID, member)
			require.Equal(t, "nic", operation)
			require.True(t, fenced)
		}

		var count int
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM profiles_reference_children WHERE attempt_token=?`, f.identity.Token).Scan(&count))
		require.Equal(t, len(result), count)
		state, err := tx.ProfileReferenceState(ctx, f.instanceID)
		require.NoError(t, err)
		require.Equal(t, f.before, state.Applied)
		require.Zero(t, state.AppliedSequence)
		require.Equal(t, f.identity.Token, state.ActiveToken)
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM profiles_reference_receipts`).Scan(&count))
		require.Zero(t, count)
		return nil
	})
	return result
}

func (f *profileChildrenFixture) assertRejected(t *testing.T, identity db.ProfileReferenceApply) {
	t.Helper()
	before := f.savedRows(t)
	result, err := f.c.ReserveProfileReferenceChildren(f.ctx, identity)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, before, f.savedRows(t), "failed reservation must preserve every prior durable row")
}

func TestProfileReferenceChildrenFullUnion(t *testing.T) {
	f := newProfileChildrenFixture(t, "default")
	require.Len(t, f.before.Resources, 8, "multiple NIC and per-ACL rows exercise deduplication")
	require.Len(t, f.after.Resources, 5)
	result := f.assertReserved(t, f.expected("default", "old", "stable", "new"))
	saved := f.savedRows(t)
	result[0].Name = "caller-mutated"
	result[0].Token = "caller-mutated"
	require.Equal(t, saved, f.savedRows(t), "returned values must not alias persisted children")
	f.assertRejected(t, f.identity)
}

func TestProfileReferenceChildrenProjectResolution(t *testing.T) {
	t.Run("shared-default-survives-project-config-change", func(t *testing.T) {
		f := newProfileChildrenFixture(t, "shared")
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			require.NoError(t, cluster.UpdateProject(ctx, tx.Tx(), "tenant", api.ProjectPut{Config: map[string]string{"features.profiles": "false", "features.networks": "true", "user.changed": "after-claim"}}))
			current, err := project.CaptureProfileReferenceSnapshot(ctx, tx, f.instanceID)
			require.NoError(t, err)
			for _, resource := range current.Resources {
				require.Equal(t, f.projects["tenant"], resource.NetworkProjectID)
			}

			return nil
		})
		f.assertReserved(t, f.expected("default", "old", "stable", "new"))
	})
	t.Run("whole-device-overrides-inherited-references", func(t *testing.T) {
		f := newProfileChildrenFixture(t, "whole-device")
		require.Equal(t, map[string]string{"type": "nic", "network": "stable"}, f.before.ExpandedDevices["shadow"])
		require.Equal(t, f.before.ExpandedDevices["shadow"], f.after.ExpandedDevices["shadow"])
		f.assertReserved(t, f.expected("default", "old", "stable", "new"))
	})
	t.Run("same-names-in-different-projects", func(t *testing.T) {
		f := newProfileChildrenFixture(t, "same-name")
		expected := append(f.expected("default", "old", "stable"), f.expected("tenant", "old", "stable")...)
		f.assertReserved(t, expected)
	})
}

func TestProfileReferenceChildrenEmptyUnion(t *testing.T) {
	for _, mode := range []string{"empty", "bridge"} {
		t.Run(mode, func(t *testing.T) {
			f := newProfileChildrenFixture(t, mode)
			f.assertReserved(t, []db.ProfileReferenceChildNetwork{})
			f.assertRejected(t, f.identity)
		})
	}
}

func TestProfileReferenceChildrenTargetGlobalState(t *testing.T) {
	for _, name := range []string{"new", "stable"} {
		t.Run(name+"-not-created", func(t *testing.T) {
			f := newProfileChildrenFixture(t, "default")
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.Tx().ExecContext(ctx, `UPDATE networks SET state=0 WHERE id=?`, f.networks["default/"+name])
				require.NoError(t, err)
				_, err = tx.Tx().ExecContext(ctx, `CREATE TRIGGER child_prevalidation BEFORE INSERT ON networks_ovn_operations BEGIN SELECT RAISE(ABORT, 'acquired-before-full-validation'); END`)
				return err
			})
			before := f.savedRows(t)
			result, err := f.c.ReserveProfileReferenceChildren(f.ctx, f.identity)
			require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
			require.Nil(t, result)
			require.Equal(t, before, f.savedRows(t))
		})
	}

	t.Run("removed-network-can-be-non-created", func(t *testing.T) {
		f := newProfileChildrenFixture(t, "default")
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			_, err := tx.Tx().ExecContext(ctx, `UPDATE networks SET state=4 WHERE id=?`, f.networks["default/old"])
			require.NoError(t, err)
			// No local-ready or active-member prerequisite belongs to this DB reservation API.
			_, err = tx.Tx().ExecContext(ctx, `UPDATE networks_nodes SET state=0`)
			require.NoError(t, err)
			_, err = tx.Tx().ExecContext(ctx, `UPDATE nodes SET state=1 WHERE id=?`, f.identity.MemberID)
			return err
		})
		f.assertReserved(t, f.expected("default", "old", "stable", "new"))
	})
}

func TestProfileReferenceChildrenLateConflictRollback(t *testing.T) {
	f := newProfileChildrenFixture(t, "default")
	foreign := uuid.NewString()
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcquireOVNNetworkOperation(ctx, "default", "new", foreign, "nic")
	})
	// new has the greatest ID, so old and stable are acquired before this real conflict.
	require.Greater(t, f.networks["default/new"], f.networks["default/stable"])
	require.Greater(t, f.networks["default/stable"], f.networks["default/old"])
	f.assertRejected(t, f.identity)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, name := range []string{"old", "stable", "new"} {
			token, err := tx.OVNNetworkOperationToken(ctx, "default", name)
			require.NoError(t, err)
			if name == "new" {
				require.Equal(t, foreign, token)
			} else {
				require.Empty(t, token)
			}
		}
		return nil
	})
}

func TestProfileReferenceChildrenSealFailureRollback(t *testing.T) {
	f := newProfileChildrenFixture(t, "default")
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER child_seal_failure BEFORE INSERT ON profiles_reference_children WHEN NEW.network_id=%d BEGIN SELECT RAISE(ABORT, 'injected-late-child-seal'); END`, f.networks["default/new"]))
		return err
	})
	before := f.savedRows(t)
	result, err := f.c.ReserveProfileReferenceChildren(f.ctx, f.identity)
	require.ErrorContains(t, err, "injected-late-child-seal")
	require.Nil(t, result)
	require.Equal(t, before, f.savedRows(t), "acquisitions and earlier child inserts share the failing seal transaction")
}

func TestProfileReferenceChildrenSealUsesFullUnionAndOwnsInput(t *testing.T) {
	t.Run("changed-only-rejected", func(t *testing.T) {
		f := newProfileChildrenFixture(t, "default")
		before := f.savedRows(t)
		err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			children := []db.ProfileReferenceChild{}
			for _, name := range []string{"old", "new"} {
				token := uuid.NewString()
				require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, "default", name, token, "nic"))
				children = append(children, db.ProfileReferenceChild{NetworkID: f.networks["default/"+name], ProjectID: f.projects["default"], Name: name, Token: token})
			}

			return tx.SealProfileReferenceChildren(ctx, f.identity, children)
		})
		require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
		require.Equal(t, before, f.savedRows(t))
	})
	t.Run("direct-seal-copies-input-without-target-state-admission", func(t *testing.T) {
		f := newProfileChildrenFixture(t, "default")
		children := []db.ProfileReferenceChild{}
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			_, err := tx.Tx().ExecContext(ctx, `UPDATE networks SET state=0 WHERE id=?`, f.networks["default/new"])
			require.NoError(t, err)
			for _, name := range []string{"new", "stable", "old"} {
				token := uuid.NewString()
				require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, "default", name, token, "nic"))
				children = append(children, db.ProfileReferenceChild{NetworkID: f.networks["default/"+name], ProjectID: f.projects["default"], Name: name, Token: token})
			}

			original := slices.Clone(children)
			require.NoError(t, tx.SealProfileReferenceChildren(ctx, f.identity, children))
			require.Equal(t, original, children)
			return nil
		})
	})
}

func (f *profileChildrenFixture) mutateSnapshot(t *testing.T, column string, mutate func(*db.ProfileReferenceSnapshot)) {
	t.Helper()
	require.Contains(t, []string{"baseline_snapshot", "target_snapshot"}, column)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var data string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT "+column+" FROM profiles_reference_attempts WHERE token=?", f.identity.Token).Scan(&data))
		var snapshot db.ProfileReferenceSnapshot
		require.NoError(t, json.Unmarshal([]byte(data), &snapshot))
		mutate(&snapshot)
		encoded, err := json.Marshal(snapshot)
		require.NoError(t, err)
		_, err = tx.Tx().ExecContext(ctx, "UPDATE profiles_reference_attempts SET "+column+"=? WHERE token=?", string(encoded), f.identity.Token)
		return err
	})
}

func TestProfileReferenceChildrenMalformedSnapshots(t *testing.T) {
	// Only negative validation fixtures corrupt saved snapshots. Positive fixtures always capture them.
	mutations := map[string]func(*db.ProfileReferenceSnapshot){
		"absent-evidenced-snapshot":   func(s *db.ProfileReferenceSnapshot) { *s = db.ProfileReferenceSnapshot{} },
		"unknown-version":             func(s *db.ProfileReferenceSnapshot) { s.Version = 2 },
		"zero-version":                func(s *db.ProfileReferenceSnapshot) { s.Version = 0 },
		"wrong-instance":              func(s *db.ProfileReferenceSnapshot) { s.InstanceID++ },
		"wrong-project":               func(s *db.ProfileReferenceSnapshot) { s.ProjectID++ },
		"wrong-member":                func(s *db.ProfileReferenceSnapshot) { s.MemberID++ },
		"empty-snapshot-project-name": func(s *db.ProfileReferenceSnapshot) { s.Project.Name = "" },
		"wrong-snapshot-project-name": func(s *db.ProfileReferenceSnapshot) { s.Project.Name = "other" },
		"zero-network-id":             func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkID = 0 },
		"negative-network-id":         func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkID = -1 },
		"absent-network-id": func(s *db.ProfileReferenceSnapshot) {
			s.Resources[0].NetworkID = 999999
			s.Resources[0].NetworkName = "absent"
		},
		"zero-network-project-id":     func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkProjectID = 0 },
		"negative-network-project-id": func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkProjectID = -1 },
		"wrong-network-project-id":    func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkProjectID++ },
		"empty-network-project-name":  func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkProject = "" },
		"wrong-network-project-name":  func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkProject = "other" },
		"empty-network-name":          func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkName = "" },
		"wrong-network-name":          func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkName = "other" },
		"empty-type":                  func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkType = "" },
		"unknown-type":                func(s *db.ProfileReferenceSnapshot) { s.Resources[0].NetworkType = "unknown" },
		"empty-device-name":           func(s *db.ProfileReferenceSnapshot) { s.Resources[0].Device = "" },
		"mixed-ovn-and-bridge": func(s *db.ProfileReferenceSnapshot) {
			duplicate := s.Resources[0]
			duplicate.NetworkType = "bridge"
			s.Resources = append(s.Resources, duplicate)
		},
		"same-name-conflicting-id": func(s *db.ProfileReferenceSnapshot) {
			duplicate := s.Resources[0]
			duplicate.NetworkID = 999999
			s.Resources = append(s.Resources, duplicate)
		},
		"same-id-conflicting-name": func(s *db.ProfileReferenceSnapshot) {
			duplicate := s.Resources[0]
			duplicate.NetworkName = "other"
			s.Resources = append(s.Resources, duplicate)
		},
		"same-id-conflicting-project": func(s *db.ProfileReferenceSnapshot) {
			duplicate := s.Resources[0]
			duplicate.NetworkProjectID++
			s.Resources = append(s.Resources, duplicate)
		},
		"same-id-conflicting-project-name": func(s *db.ProfileReferenceSnapshot) {
			duplicate := s.Resources[0]
			duplicate.NetworkProject = "other"
			s.Resources = append(s.Resources, duplicate)
		},
	}

	for _, column := range []string{"baseline_snapshot", "target_snapshot"} {
		for name, mutate := range mutations {
			t.Run(column+"/"+name, func(t *testing.T) {
				f := newProfileChildrenFixture(t, "default")
				f.mutateSnapshot(t, column, mutate)
				f.assertRejected(t, f.identity)
			})
		}
	}
	t.Run("invalid-json", func(t *testing.T) {
		f := newProfileChildrenFixture(t, "default")
		f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
			_, err := tx.Tx().ExecContext(ctx, `UPDATE profiles_reference_attempts SET baseline_snapshot='{' WHERE token=?`, f.identity.Token)
			return err
		})
		f.assertRejected(t, f.identity)
	})
	t.Run("non-ovn-invalid-catalog-is-not-filtered-away", func(t *testing.T) {
		f := newProfileChildrenFixture(t, "bridge")
		f.mutateSnapshot(t, "baseline_snapshot", func(s *db.ProfileReferenceSnapshot) {
			for i := range s.Resources {
				s.Resources[i].NetworkName = "wrong-bridge"
			}
		})
		f.assertRejected(t, f.identity)
	})
}

func TestProfileReferenceChildrenCatalogIdentity(t *testing.T) {
	for _, mutation := range []string{"renamed", "retyped", "wrong-project"} {
		t.Run(mutation, func(t *testing.T) {
			f := newProfileChildrenFixture(t, "default")
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				switch mutation {
				case "renamed":
					_, err := tx.Tx().ExecContext(ctx, `UPDATE networks SET name='renamed-old' WHERE id=?`, f.networks["default/old"])
					require.NoError(t, err)
					// A real replacement with the old name cannot replace the saved old identity.
					replacement, err := tx.CreateNetwork(ctx, "default", "old", "", db.NetworkTypeOVN, nil)
					require.NoError(t, err)
					require.NotEqual(t, f.networks["default/old"], replacement)
				case "retyped":
					_, err := tx.Tx().ExecContext(ctx, `UPDATE networks SET type=? WHERE id=?`, db.NetworkTypeBridge, f.networks["default/old"])
					require.NoError(t, err)
				case "wrong-project":
					projectID, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "other"})
					require.NoError(t, err)
					_, err = tx.Tx().ExecContext(ctx, `UPDATE networks SET project_id=? WHERE id=?`, projectID, f.networks["default/old"])
					require.NoError(t, err)
				}

				return nil
			})
			f.assertRejected(t, f.identity)
		})
	}
}

func TestProfileReferenceChildrenAttemptIdentity(t *testing.T) {
	f := newProfileChildrenFixture(t, "default")
	for name, mutate := range map[string]func(*db.ProfileReferenceApply){
		"token":        func(id *db.ProfileReferenceApply) { id.Token = uuid.NewString() },
		"change-token": func(id *db.ProfileReferenceApply) { id.ChangeToken = "other" },
		"instance":     func(id *db.ProfileReferenceApply) { id.InstanceID++ },
		"project":      func(id *db.ProfileReferenceApply) { id.ProjectID++ },
		"member":       func(id *db.ProfileReferenceApply) { id.MemberID++ },
		"placement":    func(id *db.ProfileReferenceApply) { id.PlacementRevision++ },
		"sequence":     func(id *db.ProfileReferenceApply) { id.Sequence++ },
		"owner":        func(id *db.ProfileReferenceApply) { id.Owner = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			wrong := f.identity
			mutate(&wrong)
			f.assertRejected(t, wrong)
		})
	}

	t.Run("different-local-member", func(t *testing.T) {
		f.c.NodeID(f.identity.MemberID + 1)
		defer f.c.NodeID(f.identity.MemberID)
		f.assertRejected(t, f.identity)
	})
}

func TestProfileReferenceChildrenChangedAttemptState(t *testing.T) {
	for _, mutation := range []string{"active-token", "desired-sequence", "placement-revision", "member", "project", "instance-placement", "missing-baseline-state", "phase-applied", "phase-recovery", "phase-retryable", "phase-completed"} {
		t.Run(mutation, func(t *testing.T) {
			f := newProfileChildrenFixture(t, "default")
			f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
				var statement string
				switch mutation {
				case "active-token":
					statement = `UPDATE instances_profile_reference_apply SET active_token='another-owner'`
				case "desired-sequence":
					statement = `UPDATE instances_profile_reference_apply SET desired_sequence=desired_sequence+1`
				case "placement-revision":
					statement = `UPDATE instances_profile_reference_apply SET placement_revision=placement_revision+1`
				case "member":
					statement = `UPDATE instances_profile_reference_apply SET member_id=member_id+1`
				case "project":
					id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "other"})
					require.NoError(t, err)
					statement = fmt.Sprintf(`UPDATE instances_profile_reference_apply SET project_id=%d`, id)
				case "instance-placement":
					id, err := tx.CreateNode("other-member", "10.2.0.2:8443")
					require.NoError(t, err)
					statement = fmt.Sprintf(`UPDATE instances SET node_id=%d WHERE id=%d`, id, f.instanceID)
				case "missing-baseline-state":
					statement = `DELETE FROM instances_profile_reference_apply`
				case "phase-applied":
					statement = `UPDATE profiles_reference_attempts SET phase='applied'`
				case "phase-recovery":
					statement = `UPDATE profiles_reference_attempts SET phase='recovery'`
				case "phase-retryable":
					statement = `UPDATE profiles_reference_attempts SET phase='retryable'`
				case "phase-completed":
					statement = `UPDATE profiles_reference_attempts SET phase='completed'`
				}

				_, err := tx.Tx().ExecContext(ctx, statement)
				return err
			})
			f.assertRejected(t, f.identity)
		})
	}
}

func TestProfileReferenceChildrenSealRejectsCorruptUnion(t *testing.T) {
	f := newProfileChildrenFixture(t, "default")
	f.mutateSnapshot(t, "baseline_snapshot", func(s *db.ProfileReferenceSnapshot) {
		// A duplicate OVN row later in the snapshot must not hide this conflicting type.
		s.Resources[0].NetworkType = "bridge"
	})
	before := f.savedRows(t)
	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		children := []db.ProfileReferenceChild{}
		for _, name := range []string{"old", "stable", "new"} {
			token := uuid.NewString()
			require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, "default", name, token, "nic"))
			children = append(children, db.ProfileReferenceChild{NetworkID: f.networks["default/"+name], ProjectID: f.projects["default"], Name: name, Token: token})
		}

		return tx.SealProfileReferenceChildren(ctx, f.identity, children)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	require.Equal(t, before, f.savedRows(t))
}
