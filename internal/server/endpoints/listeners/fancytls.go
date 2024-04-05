package listeners

import (
	"crypto/tls"
	"net"
	"sync"

	"github.com/armon/go-proxyproto"

	"github.com/lxc/incus/v6/internal/server/util"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// FancyTLSListener is a variation of the standard tls.Listener that supports
// atomically swapping the underlying TLS configuration and proxy protocol wrapping.
// Requests served before the swap will continue using the old configuration.
type FancyTLSListener struct {
	net.Listener
	mu           sync.RWMutex
	config       *tls.Config
	trustedProxy []net.IP
}

// NewFancyTLSListener creates a new FancyTLSListener.
func NewFancyTLSListener(inner net.Listener, cert *localtls.CertInfo) *FancyTLSListener {
	listener := &FancyTLSListener{
		Listener: inner,
	}

	listener.Config(cert)
	return listener
}

// Accept waits for and returns the next incoming TLS connection then use the
// current TLS configuration to handle it.
func (l *FancyTLSListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	l.mu.RLock()
	defer l.mu.RUnlock()
	config := l.config
	if isProxy(c.RemoteAddr().String(), l.trustedProxy) {
		c = proxyproto.NewConn(c, 0)
	}

	return tls.Server(c, config), nil
}

// Config safely swaps the underlying TLS configuration.
func (l *FancyTLSListener) Config(cert *localtls.CertInfo) {
	config := util.ServerTLSConfig(cert)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.config = config
}

// TrustedProxy sets new the https trusted proxy configuration.
func (l *FancyTLSListener) TrustedProxy(trustedProxy []net.IP) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.trustedProxy = trustedProxy
}

func isProxy(addr string, proxies []net.IP) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}

	hostIP := net.ParseIP(host)

	for _, p := range proxies {
		if hostIP.Equal(p) {
			return true
		}
	}
	return false
}
