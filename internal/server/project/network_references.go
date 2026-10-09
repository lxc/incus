package project

import (
	"context"
	"fmt"
	"net/http"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/util"
)

// ValidateDeviceNetworkReferences must run inside the transaction that saves new usage-visible device references.
func ValidateDeviceNetworkReferences(ctx context.Context, tx *db.ClusterTx, projectName string, devices deviceConfig.Devices) error {
	var p *api.Project
	for name, dev := range devices {
		if dev["type"] != "nic" || dev["network"] == "" {
			continue
		}

		if p == nil {
			record, err := cluster.GetProject(ctx, tx.Tx(), projectName)
			if err != nil {
				return err
			}

			p, err = record.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}
		}

		networkProject := NetworkProjectForNameFromRecord(p, dev["network"])
		_, net, _, err := tx.GetNetworkInAnyState(ctx, networkProject, dev["network"])
		if err != nil {
			return fmt.Errorf("Failed validating network reference on device %q: %w", name, err)
		}

		// Recheck global state in the reference transaction; local maintenance does not change it.
		if net.Status != api.NetworkStatusCreated {
			return api.StatusErrorf(http.StatusConflict, "Device %q references network %q in project %q that is not fully created (status %q)", name, dev["network"], networkProject, net.Status)
		}

		acls := util.SplitNTrimSpace(dev["security.acls"], ",", -1, true)
		if net.Type == "ovn" || len(acls) > 0 {
			operation, err := tx.OVNNetworkOperation(ctx, api.ProjectDefaultName, db.OVNPeerOperationName)
			if err != nil {
				return err
			}

			if operation != "" {
				return api.StatusErrorf(http.StatusConflict, "Device %q references an OVN resource with an outstanding shared operation", name)
			}
		}

		if net.Type == "ovn" {
			operation, err := tx.OVNNetworkOperation(ctx, networkProject, dev["network"])
			if err != nil {
				return err
			}

			if operation != "" {
				return api.StatusErrorf(http.StatusConflict, "Device %q references OVN network %q with an outstanding operation", name, dev["network"])
			}
		}

		for _, acl := range acls {
			_, err := cluster.GetNetworkACLID(ctx, tx.Tx(), networkProject, acl)
			if err != nil {
				return fmt.Errorf("Failed validating ACL reference %q on device %q: %w", acl, name, err)
			}
		}
	}

	return nil
}
