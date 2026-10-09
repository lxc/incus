//go:build linux && cgo && !agent

package project

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/shared/util"
)

// OVNNICReplayTargets supplies current candidates for comparison with independent backend evidence.
func OVNNICReplayTargets(ctx context.Context, tx *db.ClusterTx, resourceProject string, networkID int64) (map[ovn.OVNSwitchPort]ovn.NICConfigPublication, error) {
	instances, err := cluster.GetInstances(ctx, tx.Tx())
	if err != nil {
		return nil, err
	}

	targets := map[ovn.OVNSwitchPort]ovn.NICConfigPublication{}
	for _, inst := range instances {
		snapshot, err := CaptureProfileReferenceSnapshot(ctx, tx, int64(inst.ID))
		if err != nil {
			return nil, err
		}

		for _, ref := range snapshot.Resources {
			if ref.NetworkType != "ovn" || ref.NetworkProject != resourceProject || networkID > 0 && ref.NetworkID != networkID {
				continue
			}

			key := ovn.OVNSwitchPort(fmt.Sprintf("incus-net%d-instance-%s-%s", ref.NetworkID, snapshot.LocalConfig["volatile.uuid"], ref.Device))
			_, exists := targets[key]
			if exists {
				continue
			}

			id, info, _, err := tx.GetNetworkInAnyState(ctx, resourceProject, ref.NetworkName)
			if err != nil {
				return nil, err
			}

			if id != ref.NetworkID || info.Type != "ovn" {
				return nil, fmt.Errorf("NIC replay network identity changed")
			}

			projectID, err := cluster.GetProjectID(ctx, tx.Tx(), resourceProject)
			if err != nil {
				return nil, err
			}

			input := maps.Clone(ref.Config)
			if input["hwaddr"] == "" {
				input["hwaddr"] = snapshot.LocalConfig["volatile."+ref.Device+".hwaddr"]
			}

			aclIDs := map[string]int64{}
			names := util.SplitNTrimSpace(input["security.acls"], ",", -1, true)
			for _, name := range util.SplitNTrimSpace(info.Config["security.acls"], ",", -1, true) {
				if !slices.Contains(names, name) {
					names = append(names, name)
				}
			}

			for _, name := range names {
				id, err := cluster.GetNetworkACLID(ctx, tx.Tx(), resourceProject, name)
				if err != nil {
					return nil, err
				}

				aclIDs[name] = id
			}

			targets[key] = ovn.NICConfigPublication{NetworkID: ref.NetworkID, ProjectID: projectID, InstanceUUID: snapshot.LocalConfig["volatile.uuid"], Device: ref.Device, Source: inst.Node, Input: ovn.NICConfigInputs(input), ACLIDs: aclIDs}
		}
	}

	return targets, nil
}
