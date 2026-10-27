package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/tcp"
	localtls "github.com/lxc/incus/v7/shared/tls"
	"github.com/lxc/incus/v7/shared/ws"
)

// setupWebsocketDialer uses a certificate to parse and configure a websocket.Dialer.
func setupWebsocketDialer(certificate string) (*websocket.Dialer, error) {
	var err error
	var cert *x509.Certificate

	if certificate != "" {
		certBlock, _ := pem.Decode([]byte(certificate))
		if certBlock == nil {
			return nil, errors.New("Failed PEM decoding certificate")
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("Failed parsing certificate: %w", err)
		}
	}

	config, err := localtls.GetTLSConfig(cert)
	if err != nil {
		return nil, fmt.Errorf("Failed configuring TLS: %w", err)
	}

	dialer := &websocket.Dialer{
		TLSClientConfig:  config,
		NetDialContext:   localtls.RFC3493Dialer,
		HandshakeTimeout: time.Second * 5,
	}

	return dialer, nil
}

// newMigrationConn configures a new migration connection handler.
func newMigrationConn(secret string, outgoingDialer *websocket.Dialer, outgoingURL *url.URL) *migrationConn {
	return &migrationConn{
		secret:         secret,
		outgoingDialer: outgoingDialer,
		outgoingURL:    outgoingURL,
		connected:      make(chan struct{}),
	}
}

// migrationConn represents a handler for both accepting and making new migration connections.
type migrationConn struct {
	mu             sync.Mutex
	secret         string
	outgoingDialer *websocket.Dialer
	outgoingURL    *url.URL
	conn           *websocket.Conn
	connected      chan struct{}
	disconnected   bool

	// Additional connections sharing the secret, used by the multifd state transfer.
	extra      chan *websocket.Conn
	extraConns []*websocket.Conn
	extraDone  bool
}

// AllowExtra lets the secret be used for more than one connection, the additional ones are handed out by ExtraWebsocketIO.
func (c *migrationConn) AllowExtra() {
	c.extra = make(chan *websocket.Conn, 16)
}

// upgradeIncoming upgrades an incoming request to a websocket with the usual TCP timeouts.
func upgradeIncoming(r *http.Request, w http.ResponseWriter) (*websocket.Conn, error) {
	conn, err := ws.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed upgrading incoming request to websocket: %w", err)
	}

	// Set TCP timeout options.
	remoteTCP, _ := tcp.ExtractConn(conn.UnderlyingConn())
	if remoteTCP != nil {
		err = tcp.SetTimeouts(remoteTCP, 0)
		if err != nil {
			logger.Warn("Failed setting TCP timeouts on incoming websocket connection", logger.Ctx{"err": err})
		}
	}

	return conn, nil
}

// Secret returns the secret for this connection.
func (c *migrationConn) Secret() string {
	return c.secret
}

// AcceptIncoming takes an incoming HTTP request and upgrades it to a websocket.
func (c *migrationConn) AcceptIncoming(r *http.Request, w http.ResponseWriter) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.disconnected {
		return errors.New("Connection already disconnected")
	}

	if c.conn != nil {
		if c.extra == nil || c.extraDone {
			return api.StatusErrorf(http.StatusConflict, "Connection already established")
		}

		conn, err := upgradeIncoming(r, w)
		if err != nil {
			return err
		}

		select {
		case c.extra <- conn:
		default:
			_ = conn.Close()
			return api.StatusErrorf(http.StatusConflict, "Too many connections")
		}

		return nil
	}

	var err error
	c.conn, err = upgradeIncoming(r, w)
	if err != nil {
		return err
	}

	close(c.connected)

	return nil
}

// WebSocket returns the underlying websocket connection.
// If the connection isn't yet active it will either wait for an incoming connection or if configured, will attempt
// to initiate a new outbound connection. If the context is cancelled before the connection is established it
// will return with an error.
func (c *migrationConn) WebSocket(ctx context.Context) (*websocket.Conn, error) {
	c.mu.Lock()

	if c.disconnected {
		c.mu.Unlock()
		return nil, errors.New("Connection already disconnected")
	}

	if c.conn != nil {
		c.mu.Unlock()
		return c.conn, nil
	}

	if c.outgoingURL != nil && c.outgoingDialer != nil {
		var err error
		c.conn, err = c.dial(ctx)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}

		c.mu.Unlock()
		return c.conn, nil
	}

	c.mu.Unlock()

	select {
	case <-c.connected:
		return c.conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// dial opens an outbound connection using the secret.
func (c *migrationConn) dial(ctx context.Context) (*websocket.Conn, error) {
	q := c.outgoingURL.Query()
	q.Set("secret", c.secret)
	c.outgoingURL.RawQuery = q.Encode()

	conn, _, err := c.outgoingDialer.DialContext(ctx, c.outgoingURL.String(), http.Header{})
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// ExtraWebsocketIO returns the requested number of additional connections sharing the secret.
// Outbound connections are dialed, otherwise it waits for the remote side to connect them.
func (c *migrationConn) ExtraWebsocketIO(ctx context.Context, count int) ([]io.ReadWriteCloser, error) {
	c.mu.Lock()
	extra := c.extra
	disconnected := c.disconnected
	c.mu.Unlock()

	if extra == nil {
		return nil, errors.New("Extra connections aren't allowed on this channel")
	}

	if disconnected {
		return nil, errors.New("Connection already disconnected")
	}

	conns := make([]io.ReadWriteCloser, 0, count)
	for range count {
		var conn *websocket.Conn

		if c.outgoingURL != nil && c.outgoingDialer != nil {
			var err error
			conn, err = c.dial(ctx)
			if err != nil {
				return nil, err
			}
		} else {
			var ok bool

			select {
			case conn, ok = <-extra:
				if !ok {
					return nil, errors.New("Connection already disconnected")
				}

			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		c.mu.Lock()
		if c.disconnected {
			c.mu.Unlock()
			_ = conn.Close()
			return nil, errors.New("Connection already disconnected")
		}

		c.extraConns = append(c.extraConns, conn)
		c.mu.Unlock()

		conns = append(conns, ws.NewWrapper(conn))
	}

	// Refuse further connections on the secret and drop any that were queued beyond the expected count.
	c.mu.Lock()
	c.extraDone = true
	c.mu.Unlock()

	for {
		select {
		case conn, ok := <-extra:
			if !ok {
				return conns, nil
			}

			_ = conn.Close()
		default:
			return conns, nil
		}
	}
}

// WebsocketIO calls WebSocket and returns it wrapped for io.ReadWriteCloser compatibility.
func (c *migrationConn) WebsocketIO(ctx context.Context) (io.ReadWriteCloser, error) {
	wsConn, err := c.WebSocket(ctx)
	if err != nil {
		return nil, err
	}

	return ws.NewWrapper(wsConn), nil
}

// Close closes the connection (if established) and marks it as disconnected so that it cannot be used again.
func (c *migrationConn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.disconnected {
		return
	}

	c.disconnected = true

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	for _, conn := range c.extraConns {
		_ = conn.Close()
	}

	c.extraConns = nil

	// Wake up any pending receiver and drop the extra connections that were never picked up.
	// AcceptIncoming only sends while holding the lock, so nothing can race the close.
	if c.extra != nil {
		close(c.extra)

		for conn := range c.extra {
			_ = conn.Close()
		}
	}
}
