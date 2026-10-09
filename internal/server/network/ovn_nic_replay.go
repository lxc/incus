package network

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/util"
)

// OVNCheckPhysicalUnused admits deletion before origin notifications and local teardown.
func OVNCheckPhysicalUnused(ctx context.Context, n Network) error {
	driver, ok := n.(*ovn)
	if !ok {
		return errors.New("Physical OVN admission requires its original network driver")
	}

	// A parent's router is shared by its children; refuse before any member stops the network.
	children, err := driver.childNetworks()
	if err != nil {
		return err
	}

	if len(children) > 0 {
		return api.StatusErrorf(http.StatusBadRequest, "Network is the parent of %d other network(s)", len(children))
	}

	peers, err := driver.deletePeerPolicies()
	if err != nil {
		return err
	}

	return driver.ovnnb.WithNetworkTunnelPorts(driver.tunnelLspNames(driver.config)...).CheckNetworkPhysicalUnused(ctx, n.ID(), string(driver.getRouterIntPortName()), peers...)
}

func (n *ovn) nicConfigCandidate(ctx context.Context, instanceUUID, device string, config map[string]string) (networkOVN.NICConfigPublication, error) {
	p := networkOVN.NICConfigPublication{NetworkID: n.ID(), InstanceUUID: instanceUUID, Device: device, Source: n.state.ServerName, Input: networkOVN.NICConfigInputs(config), ACLIDs: map[string]int64{}}
	err := n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		referenceErr := tx.CheckOVNReferencePublication(ctx, n.ovnnb.BackendID())
		if referenceErr != nil {
			return referenceErr
		}

		var err error
		p.ProjectID, err = dbCluster.GetProjectID(ctx, tx.Tx(), n.Project())
		if err != nil {
			return err
		}

		id, info, _, err := tx.GetNetworkInAnyState(ctx, n.Project(), n.Name())
		if err != nil {
			return err
		}

		if id != n.ID() || info.Type != "ovn" {
			return fmt.Errorf("NIC producer network identity changed")
		}

		names := util.SplitNTrimSpace(config["security.acls"], ",", -1, true)
		for _, name := range util.SplitNTrimSpace(n.config["security.acls"], ",", -1, true) {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}

		for _, name := range names {
			id, err := dbCluster.GetNetworkACLID(ctx, tx.Tx(), n.Project(), name)
			if err != nil {
				return err
			}

			p.ACLIDs[name] = id
		}

		return nil
	})
	return p, err
}

func (n *ovn) nicReplayCandidates(ctx context.Context) (map[networkOVN.OVNSwitchPort]networkOVN.NICConfigPublication, error) {
	var targets map[networkOVN.OVNSwitchPort]networkOVN.NICConfigPublication
	err := n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		referenceErr := tx.CheckOVNReferencePublication(ctx, n.ovnnb.BackendID())
		if referenceErr != nil {
			return referenceErr
		}

		var err error
		targets, err = project.OVNNICReplayTargets(ctx, tx, n.Project(), n.ID())
		return err
	})
	return targets, err
}

// publishNICAddConfig publishes only a completed normal disabled Add allocation snapshot.
func (n *ovn) publishNICAddConfig(ctx context.Context, port networkOVN.OVNSwitchPort, p networkOVN.NICConfigPublication, opts *networkOVN.OVNSwitchPortOpts) error {
	p.Input = maps.Clone(p.Input)
	p.Generation = uuid.NewString()
	p.Phase = "add"
	return n.ovnnb.PublishNICAddConfig(ctx, n.getIntSwitchName(), port, p, opts)
}

func (n *ovn) publishNICConfig(ctx context.Context, port networkOVN.OVNSwitchPort, p networkOVN.NICConfigPublication, phase, generation string) error {
	p.Input = maps.Clone(p.Input)
	if generation == "" {
		generation = uuid.NewString()
	}

	p.Generation = generation
	p.Phase = phase
	return n.ovnnb.PublishNICConfig(ctx, n.getIntSwitchName(), port, p)
}

// nicPlannedReplay pins the proposed network ACL identity before any shared reload effects.
func (n *ovn) nicPlannedReplay(ctx context.Context, targets map[networkOVN.OVNSwitchPort]networkOVN.NICConfigPublication, config map[string]string) (map[networkOVN.OVNSwitchPort]networkOVN.NICConfigPublication, error) {
	planned := maps.Clone(targets)
	err := n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		for port, target := range targets {
			target.ACLIDs = map[string]int64{}
			names := util.SplitNTrimSpace(target.Input["security.acls"], ",", -1, true)
			for _, name := range util.SplitNTrimSpace(config["security.acls"], ",", -1, true) {
				if !slices.Contains(names, name) {
					names = append(names, name)
				}
			}

			for _, name := range names {
				id, err := dbCluster.GetNetworkACLID(ctx, tx.Tx(), n.Project(), name)
				if err != nil {
					return err
				}

				target.ACLIDs[name] = id
			}

			planned[port] = target
		}

		return nil
	})
	return planned, err
}
