//go:build linux && cgo

package endpoints

import (
	"net"
)

// Create a new net.Listener bound to the unix socket of the local endpoint.
func localCreateListener(path string, group string, label string) (net.Listener, error) {
	err := CheckAlreadyRunning(path)
	if err != nil {
		return nil, err
	}

	err = socketUnixRemoveStale(path)
	if err != nil {
		return nil, err
	}

	listener, err := socketUnixListen(path)
	if err != nil {
		return nil, err
	}

	err = localSetAccess(path, group, label)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}

	return listener, nil
}

// Change the file mode and ownership of the local endpoint unix socket file,
// so access is granted only to the process user and to the given group (or the
// process group if group is empty).
func localSetAccess(path string, group string, label string) error {
	err := socketUnixSetPermissions(path, 0660)
	if err != nil {
		return err
	}

	err = socketUnixSetOwnership(path, group)
	if err != nil {
		return err
	}

	err = socketUnixSetLabel(path, label)
	if err != nil {
		return err
	}

	return nil
}
