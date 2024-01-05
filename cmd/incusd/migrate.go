// Package migration provides the primitives for server to server migration.
//
// See https://github.com/lxc/incus/blob/main/doc/migration.md for a complete
// description.

package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/lxc/incus/internal/jmap"
	"github.com/lxc/incus/internal/migration"
	"github.com/lxc/incus/internal/server/instance"
	localMigration "github.com/lxc/incus/internal/server/migration"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/idmap"
)

type migrationFields struct {
	controlLock sync.Mutex

	conns map[string]*migrationConn

	// container specific fields
	live         bool
	instanceOnly bool
	instance     instance.Instance

	// storage specific fields
	volumeOnly        bool
	allowInconsistent bool
}

func (c *migrationFields) send(m proto.Message) error {
	/* gorilla websocket doesn't allow concurrent writes, and
	 * panic()s if it sees them (which is reasonable). If e.g. we
	 * happen to fail, get scheduled, start our write, then get
	 * unscheduled before the write is bit to a new thread which is
	 * receiving an error from the other side (due to our previous
	 * close), we can engage in these concurrent writes, which
	 * casuses the whole daemon to panic.
	 *
	 * Instead, let's lock sends to the controlConn so that we only ever
	 * write one message at the time.
	 */
	c.controlLock.Lock()
	defer c.controlLock.Unlock()

	conn, err := c.conns[api.SecretNameControl].WebSocket(context.TODO())
	if err != nil {
		return fmt.Errorf("Control connection not initialized: %w", err)
	}

	_ = conn.SetWriteDeadline(time.Now().Add(time.Second * 30))
	err = migration.ProtoSend(conn, m)
	if err != nil {
		return err
	}

	return nil
}

func (c *migrationFields) recv(m proto.Message) error {
	conn, err := c.conns[api.SecretNameControl].WebSocket(context.TODO())
	if err != nil {
		return fmt.Errorf("Control connection not initialized: %w", err)
	}

	return migration.ProtoRecv(conn, m)
}

func (c *migrationFields) disconnect() {
	c.controlLock.Lock()
	ctx, cancel := context.WithTimeout(context.TODO(), time.Second)
	defer cancel()
	conn, _ := c.conns[api.SecretNameControl].WebSocket(ctx)
	if conn != nil {
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second * 30))
		_ = conn.WriteMessage(websocket.CloseMessage, closeMsg)
	}

	c.controlLock.Unlock()

	/* Below we just Close(), which doesn't actually write to the
	 * websocket, it just closes the underlying connection. If e.g. there
	 * is still a filesystem transfer going on, but the other side has run
	 * out of disk space, writing an actual CloseMessage here will cause
	 * gorilla websocket to panic. Instead, we just force close this
	 * connection, since we report the error over the control channel
	 * anyway.
	 */
	for _, conn := range c.conns {
		conn.Close()
	}
}

func (c *migrationFields) sendControl(err error) {
	c.controlLock.Lock()
	conn, _ := c.conns[api.SecretNameControl].WebSocket(context.TODO())
	if conn != nil {
		migration.ProtoSendControl(conn, err)
	}

	c.controlLock.Unlock()

	if err != nil {
		c.disconnect()
	}
}

func (c *migrationFields) controlChannel() <-chan *localMigration.ControlResponse {
	ch := make(chan *localMigration.ControlResponse)
	go func() {
		resp := localMigration.ControlResponse{}
		err := c.recv(&resp.MigrationControl)
		if err != nil {
			resp.Err = err
			ch <- &resp

			return
		}

		ch <- &resp
	}()

	return ch
}

type migrationSourceWs struct {
	migrationFields

	clusterMoveSourceName string

	pushCertificate  string
	pushOperationURL string
	pushSecrets      map[string]string
}

func (s *migrationSourceWs) Metadata() any {
	secrets := make(jmap.Map, len(s.conns))
	for connName, conn := range s.conns {
		secrets[connName] = conn.Secret()
	}

	return secrets
}

func (s *migrationSourceWs) Connect(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
	incomingSecret := r.FormValue("secret")
	if incomingSecret == "" {
		return api.StatusErrorf(http.StatusBadRequest, "Missing migration source secret")
	}

	for connName, conn := range s.conns {
		if incomingSecret != conn.Secret() {
			continue
		}

		err := conn.AcceptIncoming(r, w)
		if err != nil {
			return fmt.Errorf("Failed accepting incoming migration source %q connection: %w", connName, err)
		}

		return nil
	}

	// If we didn't find the right secret, the user provided a bad one, so return 403, not 404, since this
	// operation actually exists.
	return api.StatusErrorf(http.StatusForbidden, "Invalid migration source secret")
}

type migrationSink struct {
	migrationFields

	url                   string
	push                  bool
	clusterMoveSourceName string
	refresh               bool
}

// MigrationSinkArgs arguments to configure migration sink.
type migrationSinkArgs struct {
	// General migration fields
	Dialer  *websocket.Dialer
	Push    bool
	Secrets map[string]string
	URL     string

	// Instance specific fields
	Instance              instance.Instance
	InstanceOnly          bool
	Idmap                 *idmap.Set
	Live                  bool
	Refresh               bool
	ClusterMoveSourceName string
	Snapshots             []*migration.Snapshot

	// Storage specific fields
	VolumeOnly bool
	VolumeSize int64

	// Transport specific fields
	RsyncFeatures []string
}

// Metadata returns metadata for the migration sink.
func (s *migrationSink) Metadata() any {
	secrets := make(jmap.Map, len(s.conns))
	for connName, conn := range s.conns {
		secrets[connName] = conn.Secret()
	}

	return secrets
}

// Connect connects to the migration source.
func (s *migrationSink) Connect(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
	incomingSecret := r.FormValue("secret")
	if incomingSecret == "" {
		return api.StatusErrorf(http.StatusBadRequest, "Missing migration sink secret")
	}

	for connName, conn := range s.conns {
		if incomingSecret != conn.Secret() {
			continue
		}

		err := conn.AcceptIncoming(r, w)
		if err != nil {
			return fmt.Errorf("Failed accepting incoming migration sink %q connection: %w", connName, err)
		}

		return nil
	}

	// If we didn't find the right secret, the user provided a bad one, so return 403, not 404, since this
	// operation actually exists.
	return api.StatusErrorf(http.StatusForbidden, "Invalid migration sink secret")
}
