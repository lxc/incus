package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cowsql/go-cowsql/cluster"
	cowsqltls "github.com/cowsql/go-cowsql/cluster/tls"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/logger"
	localtls "github.com/lxc/incus/v7/shared/tls"
)

// Notifier is a function that invokes the given function against each node in
// the cluster excluding the invoking one.
type Notifier func(hook func(incus.InstanceServer) error) error

// NotifierPolicy can be used to tweak the behavior of NewNotifier in case of
// some nodes are down.
type NotifierPolicy = cluster.NotifierPolicy

// Possible notification policies.
const (
	NotifyAll    = cluster.NotifyAll    // Requires that all nodes are up.
	NotifyAlive  = cluster.NotifyAlive  // Only notifies nodes that are alive
	NotifyTryAll = cluster.NotifyTryAll // Attempt to notify all nodes regardless of state.
)

// NewNotifier returns a new cluster notifier for the given policy.
func NewNotifier(s *state.State, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, policy NotifierPolicy) (Notifier, error) {
	if s.Cluster == nil {
		nullNotifier := func(func(incus.InstanceServer) error) error { return nil }
		return nullNotifier, nil
	}

	notifier, err := s.Cluster.NewNotifier(context.TODO(), networkCert, serverCert, policy)
	if err != nil {
		return nil, err
	}

	return wrapNotifier(notifier, policy), nil
}

func wrapNotifier(notifier cluster.Notifier, policy NotifierPolicy) Notifier {
	return func(hook func(incus.InstanceServer) error) error {
		clusterHook := func(ctx context.Context, address string, networkCert, serverCert cowsqltls.CertInfo) error {
			if networkCert == nil || serverCert == nil {
				return errors.New("Failed to emit notification, no certificates provided")
			}

			localNetworkCert := localtls.NewCertInfo(networkCert.KeyPair(), networkCert.CA(), networkCert.CRL())
			localServerCert := localtls.NewCertInfo(serverCert.KeyPair(), networkCert.CA(), networkCert.CRL())
			client, err := Connect(address, localNetworkCert, localServerCert, nil, true)
			if err != nil {
				return fmt.Errorf("failed to connect to peer %s: %w", address, err)
			}

			err = hook(client)
			if err != nil {
				return fmt.Errorf("failed to notify peer %s: %w", address, err)
			}

			return nil
		}

		errs := notifier(clusterHook)
		// TODO: aggregate all errors?
		for _, err := range errs {
			if err != nil {
				if localtls.IsConnectionError(err) && policy == NotifyAlive {
					logger.Warn("Could not notify node", logger.Ctx{"err": err})
					continue
				}

				return err
			}
		}

		return nil
	}
}

// NewNotifierForMembers builds a notifier restricted to the explicitly selected member IDs.
func NewNotifierForMembers(s *state.State, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, policy NotifierPolicy, memberIDs []int64) (Notifier, error) {
	selected := make(map[int64]struct{}, len(memberIDs))
	for _, id := range memberIDs {
		selected[id] = struct{}{}
	}

	return newNotifierForMembers(s, networkCert, serverCert, policy, selected)
}

func newNotifierForMembers(s *state.State, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, policy NotifierPolicy, selected map[int64]struct{}) (Notifier, error) {
	if s.Cluster == nil {
		return func(func(incus.InstanceServer) error) error { return nil }, nil
	}

	localClusterAddress := s.LocalConfig.ClusterAddress()

	// Fast-track the case where we're not clustered at all.
	if localClusterAddress == "" {
		nullNotifier := func(func(incus.InstanceServer) error) error { return nil }
		return nullNotifier, nil
	}

	var err error
	var members []db.NodeInfo
	var offlineThreshold time.Duration
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
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
		if selected != nil {
			_, included := selected[member.ID]
			if !included {
				continue
			}
		}

		if member.Address == localClusterAddress || member.Address == "0.0.0.0" {
			continue // Exclude ourselves
		}

		if member.IsOffline(offlineThreshold) {
			switch policy {
			case NotifyAll:
				// Even if the heartbeat timestamp is not recent
				// enough, let's try to connect to the node, just in
				// case the heartbeat is lagging behind for some reason
				// and the node is actually up.
				if !HasConnectivity(networkCert, serverCert, member.Address, false) {
					return nil, fmt.Errorf("peer node %s is down", member.Address)
				}

			case NotifyAlive:
				continue // Just skip this node
			case NotifyTryAll:
			}
		}

		peers = append(peers, member.Address)
	}

	notifier := func(hook func(context.Context, string, cowsqltls.CertInfo, cowsqltls.CertInfo) error) []error {
		errs := make([]error, len(peers))
		wg := sync.WaitGroup{}
		wg.Add(len(peers))
		for i, address := range peers {
			go func(i int, address string) {
				defer wg.Done()
				errs[i] = hook(context.TODO(), address, networkCert, serverCert)
			}(i, address)
		}

		wg.Wait()
		return errs
	}

	return wrapNotifier(notifier, policy), nil
}
