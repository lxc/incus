package util

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// InMemoryNetwork creates a fully in-memory listener and dial function.
//
// Each time the dial function is invoked a new pair of net.Conn objects will
// be created using net.Pipe: the listener's Accept method will unblock and
// return one end of the pipe and the other end will be returned by the dial
// function.
func InMemoryNetwork() (net.Listener, func() net.Conn) {
	listener := &inMemoryListener{
		conns:  make(chan net.Conn, 16),
		closed: make(chan struct{}),
	}

	dialer := func() net.Conn {
		server, client := net.Pipe()
		listener.conns <- server
		return client
	}

	return listener, dialer
}

type inMemoryListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

// Accept waits for and returns the next connection to the listener.
func (l *inMemoryListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, errors.New("closed")
	}
}

// Close closes the listener.
// Any blocked Accept operations will be unblocked and return errors.
func (l *inMemoryListener) Close() error {
	close(l.closed)
	return nil
}

// Addr returns the listener's network address.
func (l *inMemoryListener) Addr() net.Addr {
	return &inMemoryAddr{}
}

type inMemoryAddr struct{}

func (a *inMemoryAddr) Network() string {
	return "memory"
}

func (a *inMemoryAddr) String() string {
	return ""
}

// ServerTLSConfig returns a new server-side tls.Config generated from the give
// certificate info.
func ServerTLSConfig(cert *localtls.CertInfo) *tls.Config {
	config := localtls.InitTLSConfig()
	config.ClientAuth = tls.RequestClientCert
	config.Certificates = []tls.Certificate{cert.KeyPair()}
	config.NextProtos = []string{"h2"} // Required by gRPC

	if cert.CA() != nil {
		pool := x509.NewCertPool()
		pool.AddCert(cert.CA())
		config.RootCAs = pool
		config.ClientCAs = pool

		logger.Infof("Incus is in CA mode, only CA-signed certificates will be allowed")
	}

	return config
}

// SysctlGet retrieves the value of a sysctl file in /proc/sys.
func SysctlGet(path string) (string, error) {
	// Read the current content
	content, err := os.ReadFile(fmt.Sprintf("/proc/sys/%s", path))
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// SysctlSet writes a value to a sysctl file in /proc/sys.
// Requires an even number of arguments as key/value pairs. E.g. SysctlSet("path1", "value1", "path2", "value2").
func SysctlSet(parts ...string) error {
	partsLen := len(parts)
	if partsLen%2 != 0 {
		return errors.New("Requires even number of arguments")
	}

	for i := 0; i < partsLen; i = i + 2 {
		path := parts[i]
		newValue := parts[i+1]

		// Get current value.
		currentValue, err := SysctlGet(path)
		if err == nil && currentValue == newValue {
			// Nothing to update.
			return nil
		}

		err = os.WriteFile(fmt.Sprintf("/proc/sys/%s", path), []byte(newValue), 0)
		if err != nil {
			return err
		}
	}

	return nil
}
