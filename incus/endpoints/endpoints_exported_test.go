package endpoints

import (
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
)

// New creates a new Endpoints instance without bringing it up.
func Unstarted() *Endpoints {
	return &Endpoints{
		systemdListenFDsStart: util.SystemdListenFDsStart,
	}
}

func (e *Endpoints) Up(config *Config) error {
	return e.up(config)
}

// Return the path to the devlxd socket file.
func (e *Endpoints) DevLxdSocketPath() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	listener := e.listeners[devlxd]
	return listener.Addr().String()
}

func (e *Endpoints) LocalSocketPath() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	listener := e.listeners[local]
	return listener.Addr().String()
}

// Return the network addresss and server certificate of the network
// endpoint. This method is supposed to be used in conjunction with
// the httpGetOverTLSSocket test helper.
func (e *Endpoints) NetworkAddressAndCert() (string, *shared.CertInfo) {
	return e.NetworkAddress(), e.cert
}

// Return the cluster addresss and server certificate of the network
// endpoint. This method is supposed to be used in conjunction with
// the httpGetOverTLSSocket test helper.
func (e *Endpoints) ClusterAddressAndCert() (string, *shared.CertInfo) {
	return e.clusterAddress(), e.cert
}

// Set the file descriptor number marker that will be used when detecting
// socket activation. Needed because "go test" might open unrelated file
// descriptor starting at number 3.
func (e *Endpoints) SystemdListenFDsStart(start int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.systemdListenFDsStart = start
}
