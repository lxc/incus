package drivers

import (
	"fmt"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/shared/subprocess"
)

// fsExists checks that the Ceph FS instance indeed exists.
func (d *cephfs) fsExists(clusterName string, userName string, fsName string) (bool, error) {
	_, err := subprocess.RunCommand("ceph", "--name", fmt.Sprintf("client.%s", userName), "--cluster", clusterName, "fs", "get", fsName)
	if err != nil {
		status, _ := linux.ExitStatus(err)
		// If the error status code is 2, the fs definitely doesn't exist.
		if status == 2 {
			return false, nil
		}

		// Else, the error status is not 0 or 2,
		// so we can't be sure if the fs exists or not
		// as it might be a network issue, an internal ceph issue, etc.
		return false, err
	}

	return true, nil
}

// osdPoolExists checks that the Ceph OSD Pool indeed exists.
func (d *cephfs) osdPoolExists(clusterName string, userName string, osdPoolName string) (bool, error) {
	_, err := subprocess.RunCommand("ceph", "--name", fmt.Sprintf("client.%s", userName), "--cluster", clusterName, "osd", "pool", "get", osdPoolName, "size")
	if err != nil {
		status, _ := linux.ExitStatus(err)
		// If the error status code is 2, the pool definitely doesn't exist.
		if status == 2 {
			return false, nil
		}

		// Else, the error status is not 0 or 2,
		// so we can't be sure if the pool exists or not
		// as it might be a network issue, an internal ceph issue, etc.
		return false, err
	}

	return true, nil
}

// getConfig parses the Ceph configuration file and returns the list of monitors and secret key.
func (d *cephfs) getConfig(clusterName string, userName string) ([]string, string, error) {
	// Get the monitor list.
	monitors, err := CephMonitors(clusterName)
	if err != nil {
		return nil, "", err
	}

	// Get the keyring entry.
	secret, err := CephKeyring(clusterName, userName)
	if err != nil {
		return nil, "", err
	}

	return monitors, secret, nil
}
