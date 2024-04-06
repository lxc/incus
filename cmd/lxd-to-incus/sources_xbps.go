package main

import (
	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

type srcXbps struct{}

func (s *srcXbps) present() bool {
	if !util.PathExists("/var/db/xbps/.lxd-files.plist") {
		return false
	}

	if !util.PathExists("/var/service/lxd") {
		return false
	}

	if !util.PathExists("/var/lib/lxd/unix.socket") {
		return false
	}

	return true
}

func (s *srcXbps) name() string {
	return "xbps"
}

func (s *srcXbps) stop() error {
	_, err := subprocess.RunCommand("sv", "stop", "lxd")
	return err
}

func (s *srcXbps) start() error {
	_, err := subprocess.RunCommand("sv", "start", "lxd")
	return err
}

func (s *srcXbps) purge() error {
	_, err := subprocess.RunCommand("xbps-remove", "-R", "-y", "lxd")
	return err
}

func (s *srcXbps) connect() (incus.InstanceServer, error) {
	return incus.ConnectIncusUnix("/var/lib/lxd/unix.socket", &incus.ConnectionArgs{SkipGetServer: true})
}

func (s *srcXbps) paths() (*daemonPaths, error) {
	return &daemonPaths{
		daemon: "/var/lib/lxd",
		logs:   "/var/log/lxd",
		cache:  "/var/cache/lxd",
	}, nil
}
