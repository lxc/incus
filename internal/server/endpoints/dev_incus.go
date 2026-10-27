//go:build linux && cgo

package endpoints

import (
	"net"
	"path/filepath"
)

// Create a new net.Listener bound to the unix socket of the devIncus endpoint.
func createDevIncuslListener(dir string) (net.Listener, error) {
	path := filepath.Join(dir, "guestapi", "sock")

	// If this socket exists, that means a previous daemon died and
	// didn't clean up. We assume that such a daemon is actually dead
	// if we get this far, since localCreateListener() tries to connect to
	// the actual socket to make sure that it is actually dead. So, it
	// is safe to remove it here without any checks.
	//
	// Also, it would be nice to SO_REUSEADDR here so we don't have to
	// delete the socket, but we can't:
	//   http://stackoverflow.com/questions/15716302/so-reuseaddr-and-af-unix
	//
	// Note that this will force clients to reconnect on restart.
	err := socketUnixRemoveStale(path)
	if err != nil {
		return nil, err
	}

	listener, err := socketUnixListen(path)
	if err != nil {
		return nil, err
	}

	err = socketUnixSetPermissions(path, 0o666)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}

	return listener, nil
}
