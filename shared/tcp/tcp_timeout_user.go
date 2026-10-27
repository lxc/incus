//go:build linux || zos

package tcp

import (
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// SetUserTimeout sets the TCP user timeout on a connection's socket.
func SetUserTimeout(conn *net.TCPConn, timeout time.Duration) error {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("Error getting raw connection: %w", err)
	}

	err = rawConn.Control(func(fd uintptr) {
		err = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(timeout/time.Millisecond))
	})
	if err != nil {
		return fmt.Errorf("Error setting TCP_USER_TIMEOUT option on socket: %w", err)
	}

	return nil
}
