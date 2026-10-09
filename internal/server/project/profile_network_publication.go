//go:build linux && cgo && !agent

package project

import (
	"context"
	"fmt"
	"net/http"
	"reflect"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/shared/api"
)

// CommitProfileNetworkUpdate refuses effective OVN reference loss before publishing a profile.
func CommitProfileNetworkUpdate(ctx context.Context, tx *db.ClusterTx, projectName, name string, expectedID int64, expected api.ProfilePut, proposed api.ProfilePut) error {
	record, err := cluster.GetProfile(ctx, tx.Tx(), projectName, name)
	if err != nil {
		return err
	}

	current, err := record.ToAPI(ctx, tx.Tx(), nil, nil)
	if err != nil {
		return err
	}

	if int64(record.ID) != expectedID || !reflect.DeepEqual(current.ProfilePut, expected) {
		return api.StatusErrorf(http.StatusConflict, "Profile changed before publication; retry from its current configuration")
	}

	rows, err := tx.Tx().QueryContext(ctx, `SELECT instance_id FROM instances_profiles WHERE profile_id=? ORDER BY instance_id`, expectedID)
	if err != nil {
		return err
	}

	ids := []int64{}
	for rows.Next() {
		var id int64
		err = rows.Scan(&id)
		if err != nil {
			_ = rows.Close()
			return err
		}

		ids = append(ids, id)
	}

	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}

	replacement := &api.Profile{Name: name, Project: projectName, ProfilePut: proposed}
	for _, id := range ids {
		before, err := CaptureProfileReferenceSnapshot(ctx, tx, id)
		if err != nil {
			return err
		}

		after, err := captureProfileReferenceSnapshot(ctx, tx, id, replacement, 0)
		if err != nil {
			return err
		}

		for _, old := range before.Resources {
			if old.NetworkType != "ovn" {
				continue
			}

			retained := false
			for _, target := range after.Resources {
				if old.Device == target.Device && old.NetworkID == target.NetworkID && old.NetworkProjectID == target.NetworkProjectID && old.ACLID == target.ACLID && old.ACLProjectID == target.ACLProjectID {
					retained = true
					break
				}
			}

			if !retained {
				return api.StatusErrorf(http.StatusConflict, "Profile change removes an effective OVN reference from instance ID %d device %q; first retire or override that NIC through a successful instance update", id, old.Device)
			}
		}

		err = tx.CheckProfileReferenceWriters(ctx, *before, *after)
		if err != nil {
			return err
		}

		err = ValidateDeviceNetworkReferences(ctx, tx, before.Project.Name, deviceConfig.NewDevices(after.ExpandedDevices))
		if err != nil {
			return err
		}
	}

	err = ValidateDeviceNetworkReferences(ctx, tx, projectName, deviceConfig.NewDevices(proposed.Devices))
	if err != nil {
		return err
	}

	devices, err := cluster.APIToDevices(proposed.Devices)
	if err != nil {
		return err
	}

	err = cluster.UpdateProfile(ctx, tx.Tx(), projectName, name, cluster.Profile{Project: projectName, Name: name, Description: proposed.Description})
	if err != nil {
		return err
	}

	err = cluster.UpdateProfileConfig(ctx, tx.Tx(), expectedID, proposed.Config)
	if err != nil {
		return fmt.Errorf("Save profile config: %w", err)
	}

	return cluster.UpdateProfileDevices(ctx, tx.Tx(), expectedID, devices)
}
