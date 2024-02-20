package main

import (
	"time"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

type targetXbps struct{}

func (s *targetXbps) present() bool {
	if !util.PathExists("/var/db/xbps/.incus-files.plist") {
		return false
	}

	if !util.PathExists("/var/service/incus") {
		return false
	}

	if !util.PathExists("/var/lib/incus/unix.socket") {
		return false
	}

	return true
}

func (s *targetXbps) stop() error {
	_, err := subprocess.RunCommand("sv", "stop", "incus")
	return err
}

func (s *targetXbps) start() error {
	_, err := subprocess.RunCommand("sv", "start", "incus")
	if err != nil {
		return err
	}

	// Wait for the socket to become available.
	time.Sleep(5 * time.Second)

	return nil
}

func (s *targetXbps) connect() (incus.InstanceServer, error) {
	return incus.ConnectIncusUnix("/var/lib/incus/unix.socket", &incus.ConnectionArgs{SkipGetServer: true})
}

func (s *targetXbps) paths() (*daemonPaths, error) {
	return &daemonPaths{
		daemon: "/var/lib/incus",
		logs:   "/var/log/incus",
		cache:  "/var/cache/incus",
	}, nil
}

func (s *targetXbps) name() string {
	return "xbps"
}
