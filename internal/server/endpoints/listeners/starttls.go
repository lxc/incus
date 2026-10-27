package listeners

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/lxc/incus/v7/internal/server/util"
	localtls "github.com/lxc/incus/v7/shared/tls"
)

// StarttlsListener is a variation of the standard tls.Listener that supports
// atomically swapping the underlying TLS configuration. Requests served
// before the swap will continue using the old configuration.
type StarttlsListener struct {
	net.Listener
	mu     sync.RWMutex
	config *tls.Config

	conns chan net.Conn
	done  chan struct{}
	err   error
}

// NewSTARTTLSListener creates a new STARTTLS listener.
func NewSTARTTLSListener(inner net.Listener, cert *localtls.CertInfo) *StarttlsListener {
	listener := &StarttlsListener{
		Listener: inner,
		conns:    make(chan net.Conn),
		done:     make(chan struct{}),
	}

	listener.Config(cert)
	go listener.acceptLoop()

	return listener
}

// acceptLoop accepts connections and classifies each one in its own goroutine,
// so a client that hasn't sent anything yet can't hold up the others.
func (l *StarttlsListener) acceptLoop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			l.err = err
			close(l.done)
			return
		}

		go func() {
			conn, err := l.classify(c)
			if err != nil {
				_ = c.Close()
				return
			}

			select {
			case l.conns <- conn:
			case <-l.done:
				_ = conn.Close()
			}
		}()
	}
}

// classify peeks at the first bytes of the connection to detect a STARTTLS upgrade.
func (l *StarttlsListener) classify(c net.Conn) (net.Conn, error) {
	// Don't wait forever for a silent client, matching the REST server's header read timeout.
	err := c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err != nil {
		return nil, err
	}

	// Setup buffered connection.
	bufConn := BufferedUnixConn{bufio.NewReader(c), c.(*net.UnixConn)}

	// Peek to see if STARTTLS.
	header, err := bufConn.Peek(8)
	if err != nil {
		return nil, err
	}

	err = c.SetReadDeadline(time.Time{})
	if err != nil {
		return nil, err
	}

	if string(header) != "STARTTLS" {
		return bufConn, nil
	}

	discarded, err := bufConn.Discard(9)
	if err != nil {
		return nil, err
	}

	if discarded < 9 {
		return nil, errors.New("Bad STARTTLS header on connection")
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	return tls.Server(bufConn, l.config), nil
}

// Accept returns the next connection once its first bytes have been examined for a STARTTLS upgrade.
func (l *StarttlsListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.done:
		return nil, l.err
	}
}

// Config safely swaps the underlying TLS configuration.
func (l *StarttlsListener) Config(cert *localtls.CertInfo) {
	config := util.ServerTLSConfig(cert)

	// Always use network certificate's DNS name rather than server cert, so that it matches.
	x509Cert, err := cert.PublicKeyX509()
	if err == nil && len(x509Cert.DNSNames) > 0 {
		config.ServerName = x509Cert.DNSNames[0]
	}

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
