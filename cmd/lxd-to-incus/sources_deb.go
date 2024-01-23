package main

import (
	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

type srcDeb struct{}

func (s *srcDeb) present() bool {
	// Validate that the Debian package is installed.
	if !util.PathExists("/var/lib/dpkg/info/lxd.list") {
		return false
	}

	if !util.PathExists("/var/lib/lxd") {
		return false
	}

	return true
}

func (s *srcDeb) name() string {
	return ".deb package"
}

func (s *srcDeb) stop() error {
	_, err := subprocess.RunCommand("systemctl", "stop", "lxd-containers.service", "lxd.service", "lxd.socket")
	return err
}

func (s *srcDeb) start() error {
	_, err := subprocess.RunCommand("systemctl", "start", "lxd.socket", "lxd-containers.service")
	return err
}

func (s *srcDeb) purge() error {
	_, err := subprocess.RunCommand("apt-get", "remove", "--yes", "--purge", "lxd", "lxd-client")
	return err
}

func (s *srcDeb) connect() (incus.InstanceServer, error) {
	return incus.ConnectIncusUnix("/var/lib/lxd/unix.socket", &incus.ConnectionArgs{SkipGetServer: true})
}

func (s *srcDeb) paths() (*daemonPaths, error) {
	return &daemonPaths{
		daemon: "/var/lib/lxd",
		logs:   "/var/log/lxd",
		cache:  "/var/cache/lxd",
	}, nil
}
