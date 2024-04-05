package operations

import (
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/shared/ws"
)

type operationWebSocket struct {
	req *http.Request
	op  *Operation
}

// OperationWebSocket returns a new websocket operation.
func OperationWebSocket(req *http.Request, op *Operation) response.Response {
	return &operationWebSocket{req, op}
}

func (r *operationWebSocket) Render(w http.ResponseWriter) error {
	chanErr, err := r.op.Connect(r.req, w)
	if err != nil {
		return err
	}

	err = <-chanErr
	return err
}

func (r *operationWebSocket) String() string {
	_, md, err := r.op.Render()
	if err != nil {
		return fmt.Sprintf("error: %s", err)
	}

	return md.ID
}

type forwardedOperationWebSocket struct {
	req    *http.Request
	id     string
	source *websocket.Conn // Connection to the node were the operation is running
}

// ForwardedOperationWebSocket returns a new forwarted websocket operation.
func ForwardedOperationWebSocket(req *http.Request, id string, source *websocket.Conn) response.Response {
	return &forwardedOperationWebSocket{req, id, source}
}

func (r *forwardedOperationWebSocket) Render(w http.ResponseWriter) error {
	// Upgrade target connection to websocket.
	target, err := ws.Upgrader.Upgrade(w, r.req, nil)
	if err != nil {
		return err
	}

	// Start proxying between sockets.
	<-ws.Proxy(r.source, target)

	// Make sure both sides are closed.
	_ = r.source.Close()
	_ = target.Close()

	return nil
}

func (r *forwardedOperationWebSocket) String() string {
	return r.id
}
