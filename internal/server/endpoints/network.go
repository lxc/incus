package endpoints

import (
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/lxc/incus/v6/internal/ports"
	"github.com/lxc/incus/v6/internal/server/endpoints/listeners"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/util"
)

// NetworkPublicKey returns the public key of the TLS certificate used by the
// network endpoint.
func (e *Endpoints) NetworkPublicKey() []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.cert.PublicKey()
}

// NetworkPrivateKey returns the private key of the TLS certificate used by the
// network endpoint.
func (e *Endpoints) NetworkPrivateKey() []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.cert.PrivateKey()
}

// NetworkCert returns the full TLS certificate information for this endpoint.
func (e *Endpoints) NetworkCert() *localtls.CertInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.cert
}

// NetworkAddress returns the network address of the network endpoint, or an
// empty string if there's no network endpoint.
func (e *Endpoints) NetworkAddress() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	listener := e.listeners[network]
	if listener == nil {
		return ""
	}

	return listener.Addr().String()
}

// NetworkUpdateAddress updates the address for the network endpoint, shutting
// it down and restarting it.
func (e *Endpoints) NetworkUpdateAddress(address string) error {
	if address != "" {
		address = internalUtil.CanonicalNetworkAddress(address, ports.HTTPSDefaultPort)
	}

	oldAddress := e.NetworkAddress()
	if address == oldAddress {
		return nil
	}

	clusterAddress := e.clusterAddress()

	logger.Infof("Update network address")

	e.mu.Lock()
	defer e.mu.Unlock()

	// Close the previous socket
	_ = e.closeListener(network)

	// If turning off listening, we're done.
	if address == "" {
		return nil
	}

	// If the new address covers the cluster one, turn off the cluster
	// listener.
	if clusterAddress != "" && internalUtil.IsAddressCovered(clusterAddress, address) {
		_ = e.closeListener(cluster)
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
			return nil, fmt.Errorf("Cannot listen on network HTTPS socket %q: %w", address, err)
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
				e.listeners[network] = listeners.NewFancyTLSListener(*listener, e.cert)
				e.serve(network)
			}

			return err
		}

		e.listeners[network] = listeners.NewFancyTLSListener(*listener, e.cert)
		e.serve(network)
	}

	return nil
}

// NetworkUpdateCert updates the TLS keypair and CA used by the network
// endpoint.
//
// If the network endpoint is active, in-flight requests will continue using
// the old certificate, and only new requests will use the new one.
func (e *Endpoints) NetworkUpdateCert(cert *localtls.CertInfo) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cert = cert

	for _, listenerKey := range []kind{network, cluster, vmvsock, storageBuckets, metrics} {
		listener, found := e.listeners[listenerKey]
		if found {
			listener.(*listeners.FancyTLSListener).Config(cert)
		}
	}
}

// NetworkUpdateTrustedProxy updates the https trusted proxy used by the network endpoint.
func (e *Endpoints) NetworkUpdateTrustedProxy(trustedProxy string) {
	var proxies []net.IP
	for _, p := range util.SplitNTrimSpace(trustedProxy, ",", -1, true) {
		proxyIP := net.ParseIP(p)
		if proxyIP == nil {
			continue
		}

		proxies = append(proxies, proxyIP)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	for _, kind := range []kind{network, cluster} {
		listener, ok := e.listeners[kind]
		if !ok || listener == nil {
			continue
		}

		listener.(*listeners.FancyTLSListener).TrustedProxy(proxies)
	}

	server, ok := e.servers[network]
	if ok && server != nil {
		server.ErrorLog = log.New(networkServerErrorLogWriter{proxies: proxies}, "", 0)
	}
}

// Create a new net.Listener bound to the tcp socket of the network endpoint.
func networkCreateListener(address string, cert *localtls.CertInfo) (net.Listener, error) {
	// Listening on `tcp` network with address 0.0.0.0 will end up with listening
	// on both IPv4 and IPv6 interfaces. Pass `tcp4` to make it
	// work only on 0.0.0.0. https://go-review.googlesource.com/c/go/+/45771/
	listenAddress := internalUtil.CanonicalNetworkAddress(address, ports.HTTPSDefaultPort)
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
