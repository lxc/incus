package main

import (
	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

type srcSnap struct{}

func (s *srcSnap) present() bool {
	// Validate that the snap is installed.
	if !util.PathExists("/snap/lxd") && !util.PathExists("/var/lib/snapd/snap/lxd") {
		return false
	}

	if !util.PathExists("/var/snap/lxd") {
		return false
	}

	return true
}

func (s *srcSnap) name() string {
	return "snap package"
}

func (s *srcSnap) stop() error {
	_, err := subprocess.RunCommand("snap", "stop", "lxd")
	return err
}

func (s *srcSnap) start() error {
	_, err := subprocess.RunCommand("snap", "start", "lxd")
	return err
}

func (s *srcSnap) purge() error {
	_, err := subprocess.RunCommand("snap", "remove", "lxd", "--purge")
	return err
}

func (s *srcSnap) connect() (incus.InstanceServer, error) {
	return incus.ConnectIncusUnix("/var/snap/lxd/common/lxd/unix.socket", &incus.ConnectionArgs{SkipGetServer: true})
}

func (s *srcSnap) paths() (*daemonPaths, error) {
	return &daemonPaths{
		daemon: "/var/snap/lxd/common/lxd",
		logs:   "/var/snap/lxd/common/lxd/logs",
		cache:  "/var/snap/lxd/common/lxd/cache",
	}, nil
}
