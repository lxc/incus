package linux

import (
	"net"

	"golang.org/x/sys/unix"
)

// GetUcred returns the credentials from the remote end of a unix socket.
func GetUcred(conn *net.UnixConn) (*unix.Ucred, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}

	var ucred *unix.Ucred
	var ucredErr error
	err = rawConn.Control(func(fd uintptr) {
		ucred, ucredErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return nil, err
	}

	if ucredErr != nil {
		return nil, ucredErr
	}

	return ucred, nil
}
