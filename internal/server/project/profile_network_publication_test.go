//go:build linux && cgo && !agent

package project_test

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/shared/api"
)

func publicationFixture(t *testing.T, consumers int) (*db.Cluster, int64, []int64, api.ProfilePut) {
	t.Helper()
	c, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	var profileID int64
	ids := []int64{}
	before := api.ProfilePut{Description: "original", Config: map[string]string{"user.test": "before"}, Devices: map[string]map[string]string{"eth0": {"type": "nic", "network": "old", "security.acls": "old-acl", "mtu": "1400"}}}
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		for _, name := range []string{"old", "new"} {
			_, err := tx.CreateNetwork(ctx, "default", name, "", db.NetworkTypeOVN, nil)
			require.NoError(t, err)
			require.NoError(t, tx.NetworkCreated("default", name))
		}

		_, err := cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "old-acl"})
		require.NoError(t, err)
		profileID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "strict", Description: before.Description})
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileConfig(ctx, tx.Tx(), profileID, before.Config))
		devices, err := cluster.APIToDevices(before.Devices)
		require.NoError(t, err)
		require.NoError(t, cluster.UpdateProfileDevices(ctx, tx.Tx(), profileID, devices))
		var node string
		require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&node))
		for i := 0; i < consumers; i++ {
			id, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{Project: "default", Name: fmt.Sprintf("consumer-%d", i), Node: node, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
			require.NoError(t, err)
			require.NoError(t, cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(id), "default", []string{"strict"}))
			ids = append(ids, id)
		}

		return nil
	}))
	return c, profileID, ids, before
}

func clonePublication(p api.ProfilePut) api.ProfilePut {
	p.Config = maps.Clone(p.Config)
	devices := map[string]map[string]string{}
	for name, device := range p.Devices {
		devices[name] = maps.Clone(device)
	}

	p.Devices = devices
	return p
}

func TestStrictProfilePublicationEffectiveReferenceBoundary(t *testing.T) {
	for _, change := range []string{"remove", "repoint", "acl-remove", "non-reference", "local-override", "later-override", "unused", "stale-content", "stale-id"} {
		t.Run(change, func(t *testing.T) {
			consumers := 1
			if change == "unused" {
				consumers = 0
			}

			c, id, instances, before := publicationFixture(t, consumers)
			proposed := clonePublication(before)
			refuse := true
			switch change {
			case "remove", "unused", "local-override", "later-override":
				delete(proposed.Devices, "eth0")
			case "repoint":
				proposed.Devices["eth0"]["network"] = "new"
			case "acl-remove":
				delete(proposed.Devices["eth0"], "security.acls")
			case "non-reference":
				proposed.Devices["eth0"]["mtu"] = "1450"
				refuse = false
			case "stale-content":
				proposed.Description = "new"
				before.Description = "stale"
			case "stale-id":
				id++
			}

			if change == "unused" {
				refuse = false
			}

			if change == "local-override" || change == "later-override" {
				require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					devs, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "none"}})
					if err != nil {
						return err
					}

					if change == "local-override" {
						return cluster.UpdateInstanceDevices(ctx, tx.Tx(), instances[0], devs)
					}

					later, err := cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: "default", Name: "later"})
					if err != nil {
						return err
					}

					err = cluster.UpdateProfileDevices(ctx, tx.Tx(), later, devs)
					if err != nil {
						return err
					}

					return cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(instances[0]), "default", []string{"strict", "later"})
				}))
				refuse = false
			}

			err := c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return project.CommitProfileNetworkUpdate(ctx, tx, "default", "strict", id, before, proposed)
			})
			if refuse {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				record, err := cluster.GetProfile(ctx, tx.Tx(), "default", "strict")
				if err != nil {
					return err
				}

				current, err := record.ToAPI(ctx, tx.Tx(), nil, nil)
				if err != nil {
					return err
				}

				want := proposed
				if refuse {
					want = clonePublication(before)
					want.Description = "original"
				}

				require.Equal(t, want, current.ProfilePut)
				var count int
				require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM instances_profile_reference_apply`).Scan(&count))
				require.Zero(t, count)
				return nil
			}))
		})
	}
}

func assertPublicationUnchanged(t *testing.T, c *db.Cluster, before api.ProfilePut) {
	t.Helper()
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		record, err := cluster.GetProfile(ctx, tx.Tx(), "default", "strict")
		if err != nil {
			return err
		}

		current, err := record.ToAPI(ctx, tx.Tx(), nil, nil)
		if err != nil {
			return err
		}

		require.Equal(t, before, current.ProfilePut)
		for _, table := range []string{"instances_profile_reference_apply", "profiles_reference_changes", "profiles_reference_usage", "profiles_reference_attempts"} {
			var count int
			require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count))
			require.Zero(t, count)
		}

		return nil
	}))
}

func TestStrictProfilePublicationChecksEveryConsumer(t *testing.T) {
	c, id, instances, before := publicationFixture(t, 2)
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		devices, err := cluster.APIToDevices(map[string]map[string]string{"eth0": {"type": "none"}})
		if err != nil {
			return err
		}

		return cluster.UpdateInstanceDevices(ctx, tx.Tx(), instances[0], devices)
	}))
	proposed := clonePublication(before)
	delete(proposed.Devices, "eth0")
	err := c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return project.CommitProfileNetworkUpdate(ctx, tx, "default", "strict", id, before, proposed)
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	require.ErrorContains(t, err, fmt.Sprintf("instance ID %d", instances[1]))
	assertPublicationUnchanged(t, c, before)
}

func TestStrictProfilePublicationSharedProjectNumericResolution(t *testing.T) {
	for _, mode := range []string{"default-networks", "shared-default-network", "tenant-network"} {
		t.Run(mode, func(t *testing.T) {
			c, id, instances, before := publicationFixture(t, 1)
			expectedProject := "default"
			if mode == "tenant-network" {
				expectedProject = "tenant"
			}

			var expectedProjectID, expectedNetworkID, expectedACLID int64
			require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				tenant, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "tenant"})
				if err != nil {
					return err
				}

				config := map[string]string{"features.profiles": "false", "features.networks": "false"}
				if mode != "default-networks" {
					config["features.networks"] = "true"
				}

				if mode == "shared-default-network" {
					config["restricted"] = "true"
					config["restricted.networks.access"] = "old,new"
				}

				err = cluster.CreateProjectConfig(ctx, tx.Tx(), tenant, config)
				if err != nil {
					return err
				}

				_, err = tx.CreateNetwork(ctx, "tenant", "old", "", db.NetworkTypeOVN, nil)
				if err != nil {
					return err
				}

				err = tx.NetworkCreated("tenant", "old")
				if err != nil {
					return err
				}

				_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "tenant", Name: "old-acl"})
				if err != nil {
					return err
				}

				_, err = tx.Tx().ExecContext(ctx, `UPDATE instances SET project_id=? WHERE id=?`, tenant, instances[0])
				if err != nil {
					return err
				}

				expectedProjectID, err = cluster.GetProjectID(ctx, tx.Tx(), expectedProject)
				if err != nil {
					return err
				}

				expectedNetworkID, _, _, err = tx.GetNetworkInAnyState(ctx, expectedProject, "old")
				if err != nil {
					return err
				}

				expectedACLID, err = cluster.GetNetworkACLID(ctx, tx.Tx(), expectedProject, "old-acl")
				if err != nil {
					return err
				}

				snapshot, err := project.CaptureProfileReferenceSnapshot(ctx, tx, instances[0])
				if err != nil {
					return err
				}

				require.Len(t, snapshot.Resources, 2)
				require.Equal(t, expectedProjectID, snapshot.Resources[0].NetworkProjectID)
				require.Equal(t, expectedNetworkID, snapshot.Resources[0].NetworkID)
				require.Equal(t, expectedACLID, snapshot.Resources[1].ACLID)
				require.Equal(t, expectedProjectID, snapshot.Resources[1].ACLProjectID)
				return nil
			}))
			// A same-named tenant operation must not block a default-resolved consumer.
			if expectedProject == "default" {
				require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.AcquireOVNNetworkOperation(ctx, "tenant", "old", "other-project", "update")
				}))
			}

			proposed := clonePublication(before)
			proposed.Description = "permitted"
			require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return project.CommitProfileNetworkUpdate(ctx, tx, "default", "strict", id, before, proposed)
			}))
			before = proposed
			proposed = clonePublication(before)
			delete(proposed.Devices["eth0"], "security.acls")
			err := c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return project.CommitProfileNetworkUpdate(ctx, tx, "default", "strict", id, before, proposed)
			})
			require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
			require.ErrorContains(t, err, "effective OVN reference")
			assertPublicationUnchanged(t, c, before)
		})
	}
}

func TestStrictProfilePublicationOutstandingOperationAdmission(t *testing.T) {
	for _, kind := range []string{"network", "shared"} {
		t.Run(kind, func(t *testing.T) {
			c, id, _, before := publicationFixture(t, 1)
			require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				if kind == "shared" {
					return tx.AcquireOVNPeerOperation(ctx, "outstanding", "acl-config", true)
				}

				return tx.AcquireOVNNetworkOperation(ctx, "default", "old", "outstanding", "update")
			}))
			proposed := clonePublication(before)
			proposed.Description = "blocked"
			err := c.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return project.CommitProfileNetworkUpdate(ctx, tx, "default", "strict", id, before, proposed)
			})
			require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
			assertPublicationUnchanged(t, c, before)
		})
	}
}
