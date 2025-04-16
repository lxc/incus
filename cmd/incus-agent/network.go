package main

import (
	"crypto/tls"
	"net"
	"sync"

	"github.com/lxc/incus/v6/internal/server/util"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// A variation of the standard tls.Listener that supports atomically swapping
// the underlying TLS configuration. Requests served before the swap will
// continue using the old configuration.
type networkListener struct {
	net.Listener
	mu     sync.RWMutex
	config *tls.Config
}

func networkTLSListener(inner net.Listener, config *tls.Config) *networkListener {
	listener := &networkListener{
		Listener: inner,
		config:   config,
	}

	return listener
}

// Accept waits for and returns the next incoming TLS connection then use the
// current TLS configuration to handle it.
func (l *networkListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	return tls.Server(c, l.config), nil
}

func serverTLSConfig() (*tls.Config, error) {
	certInfo, err := localtls.KeyPairAndCA(".", "agent", localtls.CertServer, false)
	if err != nil {
		return nil, err
	}

	tlsConfig := util.ServerTLSConfig(certInfo)
	return tlsConfig, nil
}
