package main

import (
	"github.com/canonical/lxd/client"

	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

type srcSnap struct{}

func (s *srcSnap) Present() bool {
	// Validate that the snap is installed.
	if !util.PathExists("/snap/lxd") {
		return false
	}

	if !util.PathExists("/var/snap/lxd") {
		return false
	}

	return true
}

func (s *srcSnap) Name() string {
	return "snap package"
}

func (s *srcSnap) Stop() error {
	_, err := subprocess.RunCommand("snap", "stop", "lxd")
	return err
}

func (s *srcSnap) Start() error {
	_, err := subprocess.RunCommand("snap", "start", "lxd")
	return err
}

func (s *srcSnap) Purge() error {
	_, err := subprocess.RunCommand("snap", "remove", "lxd", "--purge")
	return err
}

func (s *srcSnap) Connect() (lxd.InstanceServer, error) {
	return lxd.ConnectLXDUnix("/var/snap/lxd/common/lxd/unix.socket", nil)
}

func (s *srcSnap) Paths() (*DaemonPaths, error) {
	return &DaemonPaths{
		Daemon: "/var/snap/lxd/common/lxd/",
		Logs:   "/var/snap/lxd/common/lxd/logs/",
		Cache:  "/var/snap/lxd/common/lxd/cache/",
	}, nil
}
