package linux

import (
	"net"
	"os"

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

// GetPeerPidfd returns a pidfd for the peer of a unix socket, which stays tied to that process if its PID is reused.
func GetPeerPidfd(conn *net.UnixConn) (int, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return -1, err
	}

	pidfd := -1
	var pidfdErr error
	err = rawConn.Control(func(fd uintptr) {
		pidfd, pidfdErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PEERPIDFD)
	})
	if err != nil {
		return -1, err
	}

	if pidfdErr != nil {
		return -1, pidfdErr
	}

	return pidfd, nil
}

// AcceptSameUser returns the first connection on l whose peer runs as this process's effective
// user, closing any other. Abstract unix sockets have no owner or mode, so any local user can
// connect to them.
func AcceptSameUser(l net.Listener) (net.Conn, error) {
	euid := uint32(os.Geteuid())

	for {
		conn, err := l.Accept()
		if err != nil {
			return nil, err
		}

		unixConn, ok := conn.(*net.UnixConn)
		if !ok {
			_ = conn.Close()
			continue
		}

		ucred, err := GetUcred(unixConn)
		if err != nil {
			_ = conn.Close()
			continue
		}

		if ucred.Uid != euid {
			_ = conn.Close()
			continue
		}

		return conn, nil
	}
}
