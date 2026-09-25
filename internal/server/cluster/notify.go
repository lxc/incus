package cluster

import (
	"context"
	"errors"
	"fmt"

	"github.com/cowsql/go-cowsql/cluster"
	cowsqltls "github.com/cowsql/go-cowsql/cluster/tls"

	incus "github.com/lxc/incus/v7/client"
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
	}, nil
}
