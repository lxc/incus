package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// Notifier is a function that invokes the given function against each node in
// the cluster excluding the invoking one.
type Notifier func(hook func(incus.InstanceServer) error) error

// NotifierPolicy can be used to tweak the behavior of NewNotifier in case of
// some nodes are down.
type NotifierPolicy int

// Possible notifcation policies.
const (
	NotifyAll    NotifierPolicy = iota // Requires that all nodes are up.
	NotifyAlive                        // Only notifies nodes that are alive
	NotifyTryAll                       // Attempt to notify all nodes regardless of state.
)

// NewNotifier builds a Notifier that can be used to notify other peers using
// the given policy.
func NewNotifier(state *state.State, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, policy NotifierPolicy) (Notifier, error) {
	localClusterAddress := state.LocalConfig.ClusterAddress()

	// Fast-track the case where we're not clustered at all.
	if localClusterAddress == "" {
		nullNotifier := func(func(incus.InstanceServer) error) error { return nil }
		return nullNotifier, nil
	}

	var err error
	var members []db.NodeInfo
	var offlineThreshold time.Duration
	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		offlineThreshold, err = tx.GetNodeOfflineThreshold(ctx)
		if err != nil {
			return err
		}

		members, err = tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	peers := []string{}
	for _, member := range members {
		if member.Address == localClusterAddress || member.Address == "0.0.0.0" {
			continue // Exclude ourselves
		}

		if member.IsOffline(offlineThreshold) {
			// Even if the heartbeat timestamp is not recent
			// enough, let's try to connect to the node, just in
			// case the heartbeat is lagging behind for some reason
			// and the node is actually up.
			if !HasConnectivity(networkCert, serverCert, member.Address) {
				switch policy {
				case NotifyAll:
					return nil, fmt.Errorf("peer node %s is down", member.Address)
				case NotifyAlive:
					continue // Just skip this node
				case NotifyTryAll:
				}
			}
		}

		peers = append(peers, member.Address)
	}

	notifier := func(hook func(incus.InstanceServer) error) error {
		errs := make([]error, len(peers))
		wg := sync.WaitGroup{}
		wg.Add(len(peers))
		for i, address := range peers {
			logger.Debugf("Notify node %s of state changes", address)
			go func(i int, address string) {
				defer wg.Done()
				client, err := Connect(address, networkCert, serverCert, nil, true)
				if err != nil {
					errs[i] = fmt.Errorf("failed to connect to peer %s: %w", address, err)
					return
				}

				err = hook(client)
				if err != nil {
					errs[i] = fmt.Errorf("failed to notify peer %s: %w", address, err)
				}
			}(i, address)
		}

		wg.Wait()
		// TODO: aggregate all errors?
		for i, err := range errs {
			if err != nil {
				if localtls.IsConnectionError(err) && policy == NotifyAlive {
					logger.Warnf("Could not notify node %s", peers[i])
					continue
				}

				return err
			}
		}
		return nil
	}

	return notifier, nil
}
