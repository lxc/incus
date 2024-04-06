package main

import (
	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

type srcCOPR struct{}

func (s *srcCOPR) present() bool {
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

func (s *srcCOPR) name() string {
	return "COPR package"
}

func (s *srcCOPR) stop() error {
	_, err := subprocess.RunCommand("systemctl", "stop", "lxd-containers.service", "lxd.service", "lxd.socket")
	return err
}

func (s *srcCOPR) start() error {
	_, err := subprocess.RunCommand("systemctl", "start", "lxd.socket", "lxd-containers.service")
	return err
}

func (s *srcCOPR) purge() error {
	_, err := subprocess.RunCommand("dnf", "remove", "-y", "lxd")
	return err
}

func (s *srcCOPR) connect() (incus.InstanceServer, error) {
	return incus.ConnectIncusUnix("/run/lxd.socket", &incus.ConnectionArgs{SkipGetServer: true})
}

func (s *srcCOPR) paths() (*daemonPaths, error) {
	return &daemonPaths{
		daemon: "/var/lib/lxd",
		logs:   "/var/log/lxd",
		cache:  "/var/cache/lxd",
	}, nil
}
