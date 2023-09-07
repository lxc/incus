package shared

import (
	"net"
)

func IsLoopback(iface *net.Interface) bool {
	return int(iface.Flags&net.FlagLoopback) > 0
}

// AllocatePort asks the kernel for a free open port that is ready to use.
func AllocatePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return -1, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return -1, err
	}

	return l.Addr().(*net.TCPAddr).Port, l.Close()
}
