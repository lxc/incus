package endpoints

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	tomb "gopkg.in/tomb.v2"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/server/endpoints/listeners"
	"github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// Config holds various configuration values that affect endpoints initialization.
type Config struct {
	// The directory to create Unix sockets in.
	Dir string

	// UnixSocket is the path to the Unix socket to bind
	UnixSocket string

	// HTTP server handling requests for the REST API.
	RestServer *http.Server

	// HTTP server for the internal /dev/incus API exposed to containers.
	DevIncusServer *http.Server

	// The TLS keypair and optional CA to use for the network endpoint. It
	// must be always provided, since the pubblic key will be included in
	// the response of the /1.0 REST API as part of the server info.
	//
	// It can be updated after the endpoints are up using NetworkUpdateCert().
	Cert *localtls.CertInfo

	// System group name to which the unix socket for the local endpoint should be
	// chgrp'ed when starting. The default is to use the process group. An empty
	// string means "use the default".
	LocalUnixSocketGroup string

	// NetworkSetAddress sets the address for the network endpoint. If not
	// set, the network endpoint won't be started (unless it's passed via
	// socket-based activation).
	//
	// It can be updated after the endpoints are up using NetworkUpdateAddress().
	NetworkAddress string

	// Optional dedicated network address for clustering traffic. If not
	// set, NetworkAddress will be used.
	//
	// It can be updated after the endpoints are up using ClusterUpdateAddress().
	ClusterAddress string

	// Address of the debug endpoint.
	//
	// It can be updated after the endpoints are up using PprofUpdateAddress().
	DebugAddress string

	// HTTP server handling requests for the metrics API.
	MetricsServer *http.Server

	// HTTP server handling requests for the storage buckets API.
	StorageBucketsServer *http.Server

	// HTTP server handling requests from VMs via the vsock.
	VsockServer *http.Server

	// True if VMs are supported.
	VsockSupport bool
}

// Up brings up all applicable endpoints and starts accepting HTTP
// requests.
//
// The endpoints will be activated in the following order and according to the
// following rules:
//
// local endpoint (unix socket)
// ----------------------------
//
// If socket-based activation is detected, look for a unix socket among the
// inherited file descriptors and use it for the local endpoint (or if no such
// file descriptor exists, don't bring up the local endpoint at all).
//
// If no socket-based activation is detected, create a unix socket using the
// default <var-path>/unix.socket path. The file mode of this socket will be set
// to 660, the file owner will be set to the process' UID, and the file group
// will be set to the process GID, or to the GID of the system group name
// specified via config.LocalUnixSocketGroup.
//
// devIncus endpoint (unix socket)
// ----------------------------
//
// Created using <var-path>/dev_incus/sock, with file mode set to 666 (actual
// authorization will be performed by the HTTP server using the socket ucred
// struct).
//
// remote endpoint (TCP socket with TLS)
// -------------------------------------
//
// If socket-based activation is detected, look for a network socket among the
// inherited file descriptors and use it for the network endpoint.
//
// If a network address was set via config.NetworkAddress, then close any listener
// that was detected via socket-based activation and create a new network
// socket bound to the given address.
//
// The network endpoint socket will use TLS encryption, using the certificate
// keypair and CA passed via config.Cert.
//
// cluster endpoint (TCP socket with TLS)
// -------------------------------------
//
// If a network address was set via config.ClusterAddress, then attach
// config.RestServer to it.
func Up(config *Config) (*Endpoints, error) {
	if config.Dir == "" {
		return nil, fmt.Errorf("No directory configured")
	}

	if config.UnixSocket == "" {
		return nil, fmt.Errorf("No unix socket configured")
	}

	if config.RestServer == nil {
		return nil, fmt.Errorf("No REST server configured")
	}

	if config.DevIncusServer == nil {
		return nil, fmt.Errorf("No devIncus server configured")
	}

	if config.Cert == nil {
		return nil, fmt.Errorf("No TLS certificate configured")
	}

	endpoints := &Endpoints{
		systemdListenFDsStart: linux.SystemdListenFDsStart,
	}

	err := endpoints.up(config)
	if err != nil {
		_ = endpoints.Down()
		return nil, err
	}

	return endpoints, nil
}

// Endpoints are in charge of bringing up and down the HTTP endpoints for
// serving the REST API.
type Endpoints struct {
	tomb      *tomb.Tomb            // Controls the HTTP servers shutdown.
	mu        sync.RWMutex          // Serialize access to internal state.
	listeners map[kind]net.Listener // Activer listeners by endpoint type.
	servers   map[kind]*http.Server // HTTP servers by endpoint type.
	cert      *localtls.CertInfo    // Keypair and CA to use for TLS.
	inherited map[kind]bool         // Store whether the listener came through socket activation

	systemdListenFDsStart int // First socket activation FD, for tests.
}

// Up brings up all configured endpoints and starts accepting HTTP requests.
func (e *Endpoints) up(config *Config) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.servers = map[kind]*http.Server{
		devIncus:       config.DevIncusServer,
		local:          config.RestServer,
		network:        config.RestServer,
		cluster:        config.RestServer,
		pprof:          pprofCreateServer(),
		metrics:        config.MetricsServer,
		storageBuckets: config.StorageBucketsServer,
		vmvsock:        config.VsockServer,
	}

	e.cert = config.Cert
	e.inherited = map[kind]bool{}

	var err error

	// Check for socket activation.
	systemdListeners := linux.GetSystemdListeners(e.systemdListenFDsStart)
	if len(systemdListeners) > 0 {
		e.listeners = activatedListeners(systemdListeners, e.cert)
		for kind := range e.listeners {
			e.inherited[kind] = true
		}
	} else {
		e.listeners = map[kind]net.Listener{}

		e.listeners[local], err = localCreateListener(config.UnixSocket, config.LocalUnixSocketGroup)
		if err != nil {
			return fmt.Errorf("local endpoint: %w", err)
		}
	}

	// Setup STARTTLS layer on local listener.
	if e.listeners[local] != nil {
		e.listeners[local] = listeners.NewSTARTTLSListener(e.listeners[local], e.cert)
	}

	// Start the devIncus listener
	e.listeners[devIncus], err = createDevIncuslListener(config.Dir)
	if err != nil {
		return err
	}

	// Start the VM sock listener.
	if config.VsockSupport {
		e.listeners[vmvsock], err = createVsockListener(e.cert)
		if err != nil {
			return err
		}
	}

	if config.NetworkAddress != "" {
		listener, ok := e.listeners[network]
		if ok {
			logger.Infof("Replacing inherited TCP socket with configured one")
			_ = listener.Close()
			e.inherited[network] = false
		}

		// Errors here are not fatal and are just logged (unless we're clustered, see below).
		var networkAddressErr error
		attempts := 0
	againHttps:
		e.listeners[network], networkAddressErr = networkCreateListener(config.NetworkAddress, e.cert)

		isCovered := util.IsAddressCovered(config.ClusterAddress, config.NetworkAddress)
		if config.ClusterAddress != "" {
			if isCovered {
				// In case of clustering we fail if we can't bind the network address.
				if networkAddressErr != nil {
					if attempts == 0 {
						logger.Infof("Unable to bind https address %q, re-trying for a minute", config.NetworkAddress)
					}

					attempts++
					if attempts < 60 {
						time.Sleep(1 * time.Second)
						goto againHttps
					}

					return networkAddressErr
				}

				e.serve(cluster)
			}
		} else if networkAddressErr != nil {
			logger.Error("Cannot currently listen on https socket, re-trying once in 30s...", logger.Ctx{"err": networkAddressErr})

			go func() {
				time.Sleep(30 * time.Second)
				err := e.NetworkUpdateAddress(config.NetworkAddress)
				if err != nil {
					logger.Error("Still unable to listen on https socket", logger.Ctx{"err": err})
				}
			}()
		}
	}

	isCovered := false
	if config.NetworkAddress != "" {
		isCovered = util.IsAddressCovered(config.ClusterAddress, config.NetworkAddress)
	}

	if config.ClusterAddress != "" && !isCovered {
		attempts := 0
	againCluster:
		e.listeners[cluster], err = networkCreateListener(config.ClusterAddress, e.cert)
		if err != nil {
			if attempts == 0 {
				logger.Infof("Unable to bind cluster address %q, re-trying for a minute", config.ClusterAddress)
			}

			attempts++
			if attempts < 60 {
				time.Sleep(1 * time.Second)
				goto againCluster
			}

			return err
		}

		e.serve(cluster)
	}

	if config.DebugAddress != "" {
		e.listeners[pprof], err = pprofCreateListener(config.DebugAddress)
		if err != nil {
			return err
		}

		e.serve(pprof)
	}

	for kind := range e.listeners {
		e.serve(kind)
	}

	return nil
}

// UpMetrics brings up metrics listener on specified address.
func (e *Endpoints) UpMetrics(listenAddress string) error {
	var err error
	e.listeners[metrics], err = metricsCreateListener(listenAddress, e.cert)
	if err != nil {
		return fmt.Errorf("Failed starting metrics listener: %w", err)
	}

	e.serve(metrics)

	return nil
}

// UpStorageBuckets brings up storage buvkets listener on specified address.
func (e *Endpoints) UpStorageBuckets(listenAddress string) error {
	var err error
	e.listeners[storageBuckets], err = storageBucketsCreateListener(listenAddress, e.cert)
	if err != nil {
		return fmt.Errorf("Failed starting storage buckets listener: %w", err)
	}

	e.serve(storageBuckets)

	return nil
}

// Down brings down all endpoints and stops serving HTTP requests.
func (e *Endpoints) Down() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.listeners[network] != nil || e.listeners[local] != nil {
		err := e.closeListener(network)
		if err != nil {
			return err
		}

		err = e.closeListener(local)
		if err != nil {
			return err
		}
	}

	if e.listeners[cluster] != nil {
		err := e.closeListener(cluster)
		if err != nil {
			return err
		}
	}

	if e.listeners[devIncus] != nil {
		err := e.closeListener(devIncus)
		if err != nil {
			return err
		}
	}

	if e.listeners[pprof] != nil {
		err := e.closeListener(pprof)
		if err != nil {
			return err
		}
	}

	if e.listeners[metrics] != nil {
		err := e.closeListener(metrics)
		if err != nil {
			return err
		}
	}

	if e.listeners[storageBuckets] != nil {
		err := e.closeListener(storageBuckets)
		if err != nil {
			return err
		}
	}

	if e.listeners[vmvsock] != nil {
		err := e.closeListener(vmvsock)
		if err != nil {
			return err
		}
	}

	if e.tomb != nil {
		e.tomb.Kill(nil)
		_ = e.tomb.Wait()
	}

	return nil
}

// Start an HTTP server for the endpoint associated with the given code.
func (e *Endpoints) serve(kind kind) {
	listener := e.listeners[kind]

	if listener == nil {
		return
	}

	ctx := logger.Ctx{"type": kind.String(), "socket": listener.Addr()}
	if e.inherited[kind] {
		ctx["inherited"] = true
	}

	logger.Info("Binding socket", ctx)

	server := e.servers[kind]

	// Defer the creation of the tomb, so Down() doesn't wait on it unless
	// we actually have spawned at least a server.
	if e.tomb == nil {
		e.tomb = &tomb.Tomb{}
	}

	e.tomb.Go(func() error {
		return server.Serve(listener)
	})
}

// Stop the HTTP server of the endpoint associated with the given code. The
// associated socket will be shutdown too.
func (e *Endpoints) closeListener(kind kind) error {
	listener := e.listeners[kind]
	if listener == nil {
		return nil
	}

	delete(e.listeners, kind)

	logger.Info("Closing socket", logger.Ctx{"type": kind.String(), "socket": listener.Addr()})

	return listener.Close()
}

// Use the listeners associated with the file descriptors passed via
// socket-based activation.
func activatedListeners(systemdListeners []net.Listener, cert *localtls.CertInfo) map[kind]net.Listener {
	activatedListeners := map[kind]net.Listener{}
	for _, listener := range systemdListeners {
		var kind kind
		switch listener.(type) {
		case *net.UnixListener:
			kind = local
		case *net.TCPListener:
			kind = network
			listener = listeners.NewFancyTLSListener(listener, cert)
		default:
			continue
		}

		activatedListeners[kind] = listener
	}

	return activatedListeners
}

// Numeric code identifying a specific API endpoint type.
type kind int

// String returns human readable name of endpoint kind.
func (k kind) String() string {
	return descriptions[k]
}

// Numeric codes identifying the various endpoints.
const (
	local kind = iota
	devIncus
	network
	pprof
	cluster
	metrics
	vmvsock
	storageBuckets
)

// Human-readable descriptions of the various kinds of endpoints.
var descriptions = map[kind]string{
	local:          "REST API Unix socket",
	devIncus:       "devIncus socket",
	network:        "REST API TCP socket",
	pprof:          "pprof socket",
	cluster:        "cluster socket",
	metrics:        "metrics socket",
	vmvsock:        "VM socket",
	storageBuckets: "Storage buckets socket",
}
