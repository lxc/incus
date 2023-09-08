package drivers

import (
	"fmt"

	"github.com/lxc/incus/shared/subprocess"
)

// fsExists checks that the Ceph FS instance indeed exists.
func (d *cephfs) fsExists(clusterName string, userName string, fsName string) bool {
	_, err := subprocess.RunCommand("ceph", "--name", fmt.Sprintf("client.%s", userName), "--cluster", clusterName, "fs", "get", fsName)
	return err == nil
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
