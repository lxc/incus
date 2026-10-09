//go:build linux && cgo && !agent

package project_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

func TestDeviceReferenceCommitExcludesACLDeletion(t *testing.T) {
	s, cleanup := state.NewTestState(t)
	defer cleanup()
	ctx := context.Background()
	devices := deviceConfig.Devices{"eth0": {"type": "nic", "network": "ovn", "security.acls": "new-acl"}}
	var profileID int64
	require.NoError(t, s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "ovn", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		_, err = cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: api.ProjectDefaultName, Name: "new-acl"})
		require.NoError(t, err)
		profileID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: api.ProjectDefaultName, Name: "unused"})
		return err
	}))

	commit := func() error {
		return s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			err := project.ValidateDeviceNetworkReferences(ctx, tx, api.ProjectDefaultName, devices)
			if err != nil {
				return err
			}

			rows, err := cluster.APIToDevices(devices.CloneNative())
			if err != nil {
				return err
			}

			return cluster.UpdateProfileDevices(ctx, tx.Tx(), profileID, rows)
		})
	}

	// The ACL writer has reserved its usage snapshot, but has not deleted the unused ACL yet.
	require.NoError(t, s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcquireOVNPeerOperation(ctx, "delete-acl", "acl-config", false)
	}))
	require.True(t, api.StatusErrorCheck(commit(), http.StatusConflict))
	require.NoError(t, s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		rows, err := cluster.GetProfileDevices(ctx, tx.Tx(), int(profileID))
		require.NoError(t, err)
		require.Empty(t, rows)
		return tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, db.OVNPeerOperationName, "delete-acl")
	}))

	// In the opposite order, committed references are visible before the ACL writer can take its snapshot.
	require.NoError(t, commit())
	item, err := acl.LoadByName(s, api.ProjectDefaultName, "new-acl")
	require.NoError(t, err)
	used, err := item.UsedBy()
	require.NoError(t, err)
	require.Contains(t, used, "/1.0/profiles/unused")
	require.Error(t, item.Delete())

	// Revalidation also rejects an ACL deleted after the caller's earlier device validation.
	require.NoError(t, s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := cluster.UpdateProfileDevices(ctx, tx.Tx(), profileID, nil)
		if err != nil {
			return err
		}

		return cluster.DeleteNetworkACL(ctx, tx.Tx(), int(item.ID()))
	}))
	require.Error(t, commit())
}

func TestDeviceReferenceCommitRevalidatesNetworkState(t *testing.T) {
	networkTypes := map[string]db.NetworkType{
		"bridge":   db.NetworkTypeBridge,
		"macvlan":  db.NetworkTypeMacvlan,
		"sriov":    db.NetworkTypeSriov,
		"ovn":      db.NetworkTypeOVN,
		"physical": db.NetworkTypePhysical,
	}

	for typeName, networkType := range networkTypes {
		states := []db.NetworkState{0, 2, 4} // Pending, Errored, Deleting.
		if networkType == db.NetworkTypeOVN {
			// Local-only or unknown states must not authorize references if stored globally.
			states = append(states, 3, 5, 6, 7, -1)
		}

		for _, networkState := range states {
			status := db.NetworkStateToAPIStatus(networkState)
			t.Run(typeName+"/"+status, func(t *testing.T) {
				c, cleanup := db.NewTestCluster(t)
				defer cleanup()
				ctx := context.Background()
				previous := deviceConfig.Devices{"eth0": {"type": "nic", "network": "previous"}}
				proposed := deviceConfig.Devices{"eth0": {"type": "nic", "network": "target"}}
				var profileID int64
				var networkID int64
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "previous", "", db.NetworkTypeBridge, nil)
					require.NoError(t, err)
					networkID, err = tx.CreateNetwork(ctx, api.ProjectDefaultName, "target", "", networkType, nil)
					require.NoError(t, err)
					profileID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: api.ProjectDefaultName, Name: "reference-state"})
					return err
				}))
				require.NoError(t, commitProfileNetworkDevices(ctx, c, api.ProjectDefaultName, profileID, previous))

				// Earlier validation succeeds before the lifecycle transition in another transaction.
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					return project.ValidateDeviceNetworkReferences(ctx, tx, api.ProjectDefaultName, proposed)
				}))
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					if networkType == db.NetworkTypeOVN && status == api.NetworkStatusDeleting {
						err := tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "target", "delete-token", "delete")
						require.NoError(t, err)
						err = tx.NetworkDeleting(api.ProjectDefaultName, "target")
						require.NoError(t, err)
						return tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "target", "delete-token")
					}

					_, err := tx.Tx().ExecContext(ctx, "UPDATE networks SET state=? WHERE id=?", networkState, networkID)
					return err
				}))

				err := commitProfileNetworkDevices(ctx, c, api.ProjectDefaultName, profileID, proposed)
				require.Error(t, err)
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
				require.Contains(t, err.Error(), status)
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					_, net, _, err := tx.GetNetworkInAnyState(ctx, api.ProjectDefaultName, "target")
					require.NoError(t, err)
					require.Equal(t, status, net.Status)
					operation, err := tx.OVNNetworkOperation(ctx, api.ProjectDefaultName, "target")
					require.NoError(t, err)
					require.Empty(t, operation)
					rows, err := cluster.GetProfileDevices(ctx, tx.Tx(), int(profileID))
					require.NoError(t, err)
					require.Equal(t, previous.CloneNative(), cluster.DevicesToAPI(rows))
					return tx.NetworkCreated(api.ProjectDefaultName, "target")
				}))

				// A reference may be replaced even when its old network is being deleted.
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.NetworkDeleting(api.ProjectDefaultName, "previous")
				}))
				require.NoError(t, commitProfileNetworkDevices(ctx, c, api.ProjectDefaultName, profileID, proposed))
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					rows, err := cluster.GetProfileDevices(ctx, tx.Tx(), int(profileID))
					require.NoError(t, err)
					require.Equal(t, proposed.CloneNative(), cluster.DevicesToAPI(rows))
					return tx.NetworkDeleting(api.ProjectDefaultName, "target")
				}))

				// Removing references does not require the former network to remain Created.
				require.NoError(t, commitProfileNetworkDevices(ctx, c, api.ProjectDefaultName, profileID, nil))
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					rows, err := cluster.GetProfileDevices(ctx, tx.Tx(), int(profileID))
					require.NoError(t, err)
					require.Empty(t, rows)
					return nil
				}))
			})
		}
	}
}

func TestDeviceReferenceCommitAllowsLocalOVNStates(t *testing.T) {
	c, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	var profileID int64
	var networkID int64
	require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		networkID, err = tx.CreateNetwork(ctx, api.ProjectDefaultName, "ovn", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		profileID, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{Project: api.ProjectDefaultName, Name: "local-state"})
		return err
	}))

	// Global Created permits durable references independently of member-local lifecycle state.
	for _, localState := range []db.NetworkState{0, 1, 2, 3, 4, 5, 6, 7} {
		t.Run(db.NetworkStateToAPIStatus(localState), func(t *testing.T) {
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_nodes SET state=? WHERE network_id=?", localState, networkID)
				return err
			}))
			devices := deviceConfig.Devices{fmt.Sprintf("eth%d", localState): {"type": "nic", "network": "ovn"}}
			require.NoError(t, commitProfileNetworkDevices(ctx, c, api.ProjectDefaultName, profileID, devices))
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				rows, err := cluster.GetProfileDevices(ctx, tx.Tx(), int(profileID))
				require.NoError(t, err)
				require.Equal(t, devices.CloneNative(), cluster.DevicesToAPI(rows))
				return nil
			}))
		})
	}
}

func TestDeviceReferenceNetworkStateResolvesProject(t *testing.T) {
	for _, mode := range []string{"own", "inherited", "shared"} {
		t.Run(mode, func(t *testing.T) {
			c, cleanup := db.NewTestCluster(t)
			defer cleanup()
			ctx := context.Background()
			networkProject := api.ProjectDefaultName
			config := map[string]string{"features.networks": "false"}
			switch mode {
			case "own":
				config["features.networks"] = "true"
				networkProject = "tenant"
			case "shared":
				config["features.networks"] = "true"
				config["restricted"] = "true"
				config["restricted.networks.access"] = "ovn"
			}

			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "tenant"})
				require.NoError(t, err)
				err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, config)
				require.NoError(t, err)
				for _, name := range []string{api.ProjectDefaultName, "tenant"} {
					_, err = tx.CreateNetwork(ctx, name, "ovn", "", db.NetworkTypeOVN, nil)
					require.NoError(t, err)
				}

				return tx.NetworkDeleting(networkProject, "ovn")
			}))
			devices := deviceConfig.Devices{"eth0": {"type": "nic", "network": "ovn"}}
			err := c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return project.ValidateDeviceNetworkReferences(ctx, tx, "tenant", devices)
			})
			require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				otherProject := "tenant"
				if networkProject == otherProject {
					otherProject = api.ProjectDefaultName
				}

				err := tx.NetworkDeleting(otherProject, "ovn")
				require.NoError(t, err)
				return tx.NetworkCreated(networkProject, "ovn")
			}))
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return project.ValidateDeviceNetworkReferences(ctx, tx, "tenant", devices)
			}))
		})
	}
}

func commitProfileNetworkDevices(ctx context.Context, c *db.Cluster, projectName string, profileID int64, devices deviceConfig.Devices) error {
	return c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := project.ValidateDeviceNetworkReferences(ctx, tx, projectName, devices)
		if err != nil {
			return err
		}

		rows, err := cluster.APIToDevices(devices.CloneNative())
		if err != nil {
			return err
		}

		return cluster.UpdateProfileDevices(ctx, tx.Tx(), profileID, rows)
	})
}
