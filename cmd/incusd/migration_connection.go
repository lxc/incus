package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/tcp"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/ws"
)

// setupWebsocketDialer uses a certificate to parse and configure a websocket.Dialer.
func setupWebsocketDialer(certificate string) (*websocket.Dialer, error) {
	var err error
	var cert *x509.Certificate

	if certificate != "" {
		certBlock, _ := pem.Decode([]byte(certificate))
		if certBlock == nil {
			return nil, fmt.Errorf("Failed PEM decoding certificate")
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
		return fmt.Errorf("Connection already disconnected")
	}

	if c.conn != nil {
		return api.StatusErrorf(http.StatusConflict, "Connection already established")
	}

	var err error
	c.conn, err = ws.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return fmt.Errorf("Failed upgrading incoming request to websocket: %w", err)
	}

	// Set TCP timeout options.
	remoteTCP, _ := tcp.ExtractConn(c.conn.UnderlyingConn())
	if remoteTCP != nil {
		err = tcp.SetTimeouts(remoteTCP, 0)
		if err != nil {
			logger.Warn("Failed setting TCP timeouts on incoming websocket connection", logger.Ctx{"err": err})
		}
	}

	close(c.connected)

	return nil
}

// WebSocket returns the underlying websocket connection.
// If the connection isn't yet active it will either wait for an incoming connection or if configured, will atempt
// to initiate a new outbound connection. If the context is cancelled before the connection is established it
// will return with an error.
func (c *migrationConn) WebSocket(ctx context.Context) (*websocket.Conn, error) {
	c.mu.Lock()

	if c.disconnected {
		c.mu.Unlock()
		return nil, fmt.Errorf("Connection already disconnected")
	}

	if c.conn != nil {
		c.mu.Unlock()
		return c.conn, nil
	}

	if c.outgoingURL != nil && c.outgoingDialer != nil {
		var err error
		q := c.outgoingURL.Query()
		q.Set("secret", c.secret)
		c.outgoingURL.RawQuery = q.Encode()
		c.conn, _, err = c.outgoingDialer.DialContext(ctx, c.outgoingURL.String(), http.Header{})
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

	c.disconnected = true

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}
