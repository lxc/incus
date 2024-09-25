package ucred

import (
	"context"
	"fmt"
	"net"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/server/endpoints/listeners"
	"github.com/lxc/incus/v6/internal/server/request"
)

// ErrNotUnixSocket is returned when the underlying connection isn't a unix socket.
var ErrNotUnixSocket = fmt.Errorf("Connection isn't a unix socket")

// GetConnFromContext extracts the connection from the request context on a HTTP listener.
func GetConnFromContext(ctx context.Context) net.Conn {
	return ctx.Value(request.CtxConn).(net.Conn)
}

// GetCredFromContext extracts the unix credentials from the request context on a HTTP listener.
func GetCredFromContext(ctx context.Context) (*unix.Ucred, error) {
	conn := GetConnFromContext(ctx)
	unixConnPtr, ok := conn.(*net.UnixConn)
	if !ok {
		bufferedUnixConnPtr, ok := conn.(listeners.BufferedUnixConn)
		if !ok {
			return nil, ErrNotUnixSocket
		}

		unixConnPtr = bufferedUnixConnPtr.Unix()
	}

	return linux.GetUcred(unixConnPtr)
}
