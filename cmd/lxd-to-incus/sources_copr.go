package main

import (
	"github.com/canonical/lxd/client"

	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

type srcCOPR struct{}

func (s *srcCOPR) Present() bool {
	// Validate that the RPM package is installed.
	_, err := subprocess.RunCommand("rpm", "-q", "lxd")
	if err != nil {
		return false
	}

	if !util.PathExists("/run/lxd.socket") {
		return false
	}

	return true
}

func (s *srcCOPR) Name() string {
	return "COPR package"
}

func (s *srcCOPR) Stop() error {
	_, err := subprocess.RunCommand("systemctl", "stop", "lxd-containers.service", "lxd.service", "lxd.socket")
	return err
}

func (s *srcCOPR) Start() error {
	_, err := subprocess.RunCommand("systemctl", "start", "lxd.socket", "lxd-containers.service")
	return err
}

func (s *srcCOPR) Purge() error {
	_, err := subprocess.RunCommand("dnf", "remove", "-y", "lxd")
	return err
}

func (s *srcCOPR) Connect() (lxd.InstanceServer, error) {
	return lxd.ConnectLXDUnix("/run/lxd.socket", nil)
}

func (s *srcCOPR) Paths() (*DaemonPaths, error) {
	return &DaemonPaths{
		Daemon: "/var/lib/lxd/",
		Logs:   "/var/log/lxd/",
		Cache:  "/var/cache/lxd/",
	}, nil
}
