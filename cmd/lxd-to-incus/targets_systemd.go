package main

import (
	"time"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

type targetSystemd struct{}

func (s *targetSystemd) present() bool {
	if !util.PathExists("/var/lib/incus/") {
		return false
	}

	_, err := subprocess.RunCommand("systemctl", "list-unit-files", "incus.service")
	return err == nil
}

func (s *targetSystemd) stop() error {
	_, err := subprocess.RunCommand("systemctl", "stop", "incus.service", "incus.socket")
	return err
}

func (s *targetSystemd) start() error {
	_, err := subprocess.RunCommand("systemctl", "start", "incus.service", "incus.socket")
	if err != nil {
		return err
	}

	// Wait for the socket to become available.
	time.Sleep(5 * time.Second)

	return nil
}

func (s *targetSystemd) connect() (incus.InstanceServer, error) {
	return incus.ConnectIncusUnix("/var/lib/incus/unix.socket", &incus.ConnectionArgs{SkipGetServer: true})
}

func (s *targetSystemd) paths() (*daemonPaths, error) {
	return &daemonPaths{
		daemon: "/var/lib/incus",
		logs:   "/var/log/incus",
		cache:  "/var/cache/incus",
	}, nil
}

func (s *targetSystemd) name() string {
	return "systemd"
}
