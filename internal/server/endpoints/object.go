package endpoints

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/lxc/incus/v6/internal/ports"
	"github.com/lxc/incus/v6/internal/server/endpoints/listeners"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

func storageBucketsCreateListener(address string, cert *localtls.CertInfo) (net.Listener, error) {
	// Listening on `tcp` network with address 0.0.0.0 will end up with listening
	// on both IPv4 and IPv6 interfaces. Pass `tcp4` to make it
	// work only on 0.0.0.0. https://go-review.googlesource.com/c/go/+/45771/
	listenAddress := internalUtil.CanonicalNetworkAddress(address, ports.HTTPSStorageBucketsDefaultPort)
	protocol := "tcp"

	if strings.HasPrefix(listenAddress, "0.0.0.0") {
		protocol = "tcp4"
	}

	listener, err := net.Listen(protocol, listenAddress)
	if err != nil {
		return nil, fmt.Errorf("Bind network address: %w", err)
	}

	return listeners.NewFancyTLSListener(listener, cert), nil
}

// StorageBucketsAddress returns the network address of the storage buckets endpoint, or an
// empty string if there's no storage buckets endpoint.
func (e *Endpoints) StorageBucketsAddress() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	listener := e.listeners[storageBuckets]
	if listener == nil {
		return ""
	}

	return listener.Addr().String()
}

// StorageBucketsUpdateAddress updates the address for the storage buckets endpoint, shutting it down and
// restarting it.
func (e *Endpoints) StorageBucketsUpdateAddress(address string, cert *localtls.CertInfo) error {
	if address != "" {
		address = internalUtil.CanonicalNetworkAddress(address, ports.HTTPSStorageBucketsDefaultPort)
	}

	oldAddress := e.StorageBucketsAddress()
	if address == oldAddress {
		return nil
	}

	logger.Infof("Update storage buckets address")

	e.mu.Lock()
	defer e.mu.Unlock()

	// Close the previous socket
	_ = e.closeListener(storageBuckets)

	// If turning off listening, we're done
	if address == "" {
		return nil
	}

	// Attempt to setup the new listening socket
	getListener := func(address string) (*net.Listener, error) {
		var err error
		var listener net.Listener

		for i := 0; i < 10; i++ { // Ten retries over a second seems reasonable.
			listener, err = storageBucketsCreateListener(address, cert)
			if err == nil {
				break
			}

			time.Sleep(100 * time.Millisecond)
		}

		if err != nil {
			return nil, fmt.Errorf("Cannot listen on http socket: %w", err)
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
				e.listeners[storageBuckets] = *listener
				e.serve(storageBuckets)
			}

			return err
		}

		e.listeners[storageBuckets] = *listener
		e.serve(storageBuckets)
	}

	return nil
}
