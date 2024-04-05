package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// EventListenerConnection represents an event listener connection.
type EventListenerConnection interface {
	Reader(ctx context.Context, recvFunc EventHandler)
	WriteJSON(event any) error
	Close() error
	LocalAddr() net.Addr  // Used for logging
	RemoteAddr() net.Addr // Used for logging
}

type websockListenerConnection struct {
	*websocket.Conn

	lock         sync.Mutex
	pongsPending int
}

type streamListenerConnection struct {
	net.Conn

	lock sync.Mutex
}

type simpleListenerConnection struct {
	rwc io.ReadWriteCloser

	lock sync.Mutex
}

// NewWebsocketListenerConnection returns a new websocket listener connection.
func NewWebsocketListenerConnection(connection *websocket.Conn) EventListenerConnection {
	return &websockListenerConnection{
		Conn: connection,
	}
}

func (e *websockListenerConnection) Reader(ctx context.Context, recvFunc EventHandler) {
	ctx, cancel := context.WithCancel(ctx)

	close := func() {
		e.lock.Lock()
		defer e.lock.Unlock()

		if ctx.Err() != nil {
			return
		}

		_ = e.Close()
		cancel()
	}

	defer close()

	pingInterval := time.Second * 10
	e.pongsPending = 0

	e.SetPongHandler(func(msg string) error {
		e.lock.Lock()
		e.pongsPending = 0
		e.lock.Unlock()
		return nil
	})

	// Start reader from client.
	go func() {
		defer close()

		if recvFunc != nil {
			for {
				var event api.Event
				err := e.Conn.ReadJSON(&event)
				if err != nil {
					return // This detects if client has disconnected or sent invalid data.
				}

				// Pass received event to the handler.
				recvFunc(event)
			}
		} else {
			// Run a blocking reader to detect if the client has disconnected. We don't expect to get
			// anything from the remote side, so this should remain blocked until disconnected.
			_, _, _ = e.Conn.NextReader()
		}
	}()

	t := time.NewTicker(pingInterval)
	defer t.Stop()

	for {
		if ctx.Err() != nil {
			return
		}

		e.lock.Lock()
		if e.pongsPending > 2 {
			e.lock.Unlock()
			return
		}

		err := e.WriteControl(websocket.PingMessage, []byte("keepalive"), time.Now().Add(5*time.Second))
		if err != nil {
			e.lock.Unlock()
			return
		}

		e.pongsPending++
		e.lock.Unlock()

		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}

func (e *websockListenerConnection) WriteJSON(event any) error {
	e.lock.Lock()
	defer e.lock.Unlock()

	err := e.Conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		return fmt.Errorf("Failed setting write deadline: %w", err)
	}

	return e.Conn.WriteJSON(event)
}

// NewStreamListenerConnection returns a new http stream listener connection.
func NewStreamListenerConnection(connection net.Conn) (EventListenerConnection, error) {
	// Send HTTP response to let the client know what to expect.
	// This is only sent once, and is followed by events.
	//
	// The X-Content-Type-Options response HTTP header is a marker used by the server to indicate
	// that the MIME types advertised in the Content-Type headers should be followed and not be
	// changed. The header allows you to avoid MIME type sniffing by saying that the MIME types are
	// deliberately configured.
	_, err := io.WriteString(connection, `HTTP/1.1 200 OK
Connection: keep-alive
Content-Type: application/json
X-Content-Type-Options: nosniff

`)
	if err != nil {
		return nil, fmt.Errorf("Failed sending initial HTTP response: %w", err)
	}

	return &streamListenerConnection{
		Conn: connection,
	}, nil
}

func (e *streamListenerConnection) Reader(ctx context.Context, recvFunc EventHandler) {
	ctx, cancelFunc := context.WithCancel(ctx)

	close := func() {
		e.lock.Lock()
		defer e.lock.Unlock()

		if ctx.Err() != nil {
			return
		}

		err := e.Close()
		if err != nil {
			logger.Warn("Failed closing connection", logger.Ctx{"err": err})
		}

		cancelFunc()
	}

	defer close()

	// Start reader from client.
	go func() {
		defer close()

		buf := make([]byte, 1)

		// This is used to determine whether the client has terminated.
		_, err := e.Read(buf)
		if err != nil && errors.Is(err, io.EOF) {
			return
		}
	}()

	if ctx.Err() != nil {
		return
	}

	<-ctx.Done()
}

func (e *streamListenerConnection) WriteJSON(event any) error {
	e.lock.Lock()
	defer e.lock.Unlock()

	err := e.SetWriteDeadline(time.Now().Add(5 * (time.Second)))
	if err != nil {
		return fmt.Errorf("Failed setting write deadline: %w", err)
	}

	err = json.NewEncoder(e.Conn).Encode(event)
	if err != nil {
		return fmt.Errorf("Failed sending event: %w", err)
	}

	return nil
}

func (e *streamListenerConnection) Close() error {
	return e.Conn.Close()
}

// NewSimpleListenerConnection returns a new simple listener connection.
func NewSimpleListenerConnection(rwc io.ReadWriteCloser) EventListenerConnection {
	return &simpleListenerConnection{
		rwc: rwc,
	}
}

func (e *simpleListenerConnection) Reader(ctx context.Context, recvFunc EventHandler) {
	ctx, cancelFunc := context.WithCancel(ctx)

	close := func() {
		e.lock.Lock()
		defer e.lock.Unlock()

		if ctx.Err() != nil {
			return
		}

		err := e.Close()
		if err != nil {
			logger.Warn("Failed closing connection", logger.Ctx{"err": err})
		}

		cancelFunc()
	}

	defer close()

	// Start reader from client.
	go func() {
		defer close()

		buf := make([]byte, 1)

		// This is used to determine whether the client has terminated.
		_, err := e.rwc.Read(buf)
		if err != nil && errors.Is(err, io.EOF) {
			return
		}
	}()

	if ctx.Err() != nil {
		return
	}

	<-ctx.Done()
}

func (e *simpleListenerConnection) WriteJSON(event any) error {
	err := json.NewEncoder(e.rwc).Encode(event)
	if err != nil {
		return err
	}

	return nil
}

func (e *simpleListenerConnection) Close() error {
	return e.rwc.Close()
}

func (e *simpleListenerConnection) LocalAddr() net.Addr { // Used for logging
	return nil
}

func (e *simpleListenerConnection) RemoteAddr() net.Addr { // Used for logging
	return nil
}
