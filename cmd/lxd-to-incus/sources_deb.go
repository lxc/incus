package main

import (
	"github.com/canonical/lxd/client"

	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

type srcDeb struct{}

func (s *srcDeb) Present() bool {
	// Validate that the Debian package is installed.
	if !util.PathExists("/var/lib/dpkg/info/lxd.list") {
		return false
	}

	if !util.PathExists("/var/lib/lxd") {
		return false
	}

	return true
}

func (s *srcDeb) Name() string {
	return ".deb package"
}

func (s *srcDeb) Stop() error {
	_, err := subprocess.RunCommand("systemctl", "stop", "lxd-containers.service", "lxd.service", "lxd.socket")
	return err
}

func (s *srcDeb) Start() error {
	_, err := subprocess.RunCommand("systemctl", "start", "lxd.socket", "lxd-containers.service")
	return err
}

func (s *srcDeb) Purge() error {
	_, err := subprocess.RunCommand("apt-get", "remove", "--yes", "--purge", "lxd", "lxd-client")
	return err
}

func (s *srcDeb) Connect() (lxd.InstanceServer, error) {
	return lxd.ConnectLXDUnix("/var/lib/lxd/unix.socket", nil)
}

func (s *srcDeb) Paths() (*DaemonPaths, error) {
	return &DaemonPaths{
		Daemon: "/var/lib/lxd/",
		Logs:   "/var/log/lxd/",
		Cache:  "/var/cache/lxd/",
	}, nil
}
