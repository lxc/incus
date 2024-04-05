package listeners

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"sync"

	"github.com/lxc/incus/v6/internal/server/util"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// StarttlsListener is a variation of the standard tls.Listener that supports
// atomically swapping the underlying TLS configuration. Requests served
// before the swap will continue using the old configuration.
type StarttlsListener struct {
	net.Listener
	mu     sync.RWMutex
	config *tls.Config
}

// NewSTARTTLSListener creates a new STARTTLS listener.
func NewSTARTTLSListener(inner net.Listener, cert *localtls.CertInfo) *StarttlsListener {
	listener := &StarttlsListener{
		Listener: inner,
	}

	listener.Config(cert)
	return listener
}

// Accept waits for and returns the next incoming TLS connection then use the
// current TLS configuration to handle it.
func (l *StarttlsListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	// Setup bufferred connection.
	bufConn := BufferedUnixConn{bufio.NewReader(c), c.(*net.UnixConn)}

	// Peak to see if STARTTLS.
	header, err := bufConn.Peek(8)
	if err == nil && string(header) == "STARTTLS" {
		discarded, err := bufConn.Discard(9)
		if err != nil {
			return nil, err
		}

		if discarded < 9 {
			return nil, fmt.Errorf("Bad STARTTLS header on connection")
		}

		l.mu.RLock()
		defer l.mu.RUnlock()

		config := l.config
		return tls.Server(bufConn, config), nil
	}

	return bufConn, nil
}

// Config safely swaps the underlying TLS configuration.
func (l *StarttlsListener) Config(cert *localtls.CertInfo) {
	config := util.ServerTLSConfig(cert)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.config = config
}

// BufferedUnixConn is a UnixConn wrapped in a Bufio Reader.
type BufferedUnixConn struct {
	r *bufio.Reader
	*net.UnixConn
}

// Discard allows discarding some bytes from the buffer.
func (b BufferedUnixConn) Discard(n int) (int, error) {
	return b.r.Discard(n)
}

// Peek allows reading some bytes without moving the read pointer.
func (b BufferedUnixConn) Peek(n int) ([]byte, error) {
	return b.r.Peek(n)
}

// Read allows normal reads on the buffered connection.
func (b BufferedUnixConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

// Unix returns the inner UnixConn.
func (b BufferedUnixConn) Unix() *net.UnixConn {
	return b.UnixConn
}
