package endpoints

import (
	"fmt"
	"net"
	"time"

	"github.com/lxc/incus/v6/internal/ports"
	"github.com/lxc/incus/v6/internal/server/endpoints/listeners"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/logger"
)

// ClusterAddress returns the cluster address of the cluster endpoint, or an
// empty string if there's no cluster endpoint or the cluster endpoint is provided by the network listener.
func (e *Endpoints) clusterAddress() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	listener := e.listeners[cluster]
	if listener == nil {
		return ""
	}

	return listener.Addr().String()
}

// ClusterUpdateAddress updates the address for the cluster endpoint, shutting
// it down and restarting it.
func (e *Endpoints) ClusterUpdateAddress(address string) error {
	networkAddress := e.NetworkAddress()

	if address != "" {
		address = internalUtil.CanonicalNetworkAddress(address, ports.HTTPSDefaultPort)
	}

	oldAddress := e.clusterAddress()
	if address == oldAddress {
		return nil
	}

	logger.Infof("Update cluster address")

	e.mu.Lock()
	defer e.mu.Unlock()

	// Close the previous socket
	_ = e.closeListener(cluster)

	// If turning off listening, we're done
	if address == "" {
		return nil
	}

	// If networkAddress is set and address is covered, we don't need a new listener.
	if networkAddress != "" && internalUtil.IsAddressCovered(address, networkAddress) {
		return nil
	}

	// Attempt to setup the new listening socket
	getListener := func(address string) (*net.Listener, error) {
		var err error
		var listener net.Listener

		for i := 0; i < 10; i++ { // Ten retries over a second seems reasonable.
			listener, err = net.Listen("tcp", address)
			if err == nil {
				break
			}

			time.Sleep(100 * time.Millisecond)
		}

		if err != nil {
			return nil, fmt.Errorf("Cannot listen on cluster HTTPS socket %q: %w", address, err)
		}

		return &listener, nil
	}

	// If setting a new address, setup the listener
	if address != "" {
		listener, err := getListener(address)
		if err != nil {
			// Attempt to revert to the previous address
			listener, err1 := getListener(oldAddress)
			if err1 == nil {
				e.listeners[cluster] = listeners.NewFancyTLSListener(*listener, e.cert)
				e.serve(cluster)
			}

			return err
		}

		e.listeners[cluster] = listeners.NewFancyTLSListener(*listener, e.cert)
		e.serve(cluster)
	}

	return nil
}
