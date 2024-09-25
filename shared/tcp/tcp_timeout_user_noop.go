//go:build !linux && !zos

package tcp

import (
	"net"
	"time"
)

// SetUserTimeout sets the TCP user timeout on a connection's socket.
// Only supported on Linux and ZOS.
func SetUserTimeout(conn *net.TCPConn, timeout time.Duration) error {
	return nil
}
