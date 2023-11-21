package main

import (
	"time"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

type targetSystemd struct{}

func (s *targetSystemd) Present() bool {
	if !util.PathExists("/var/lib/incus/") {
		return false
	}

	_, err := subprocess.RunCommand("systemctl", "list-unit-files", "incus.service")
	if err != nil {
		return false
	}

	return true
}

func (s *targetSystemd) Stop() error {
	_, err := subprocess.RunCommand("systemctl", "stop", "incus.service", "incus.socket")
	return err
}

func (s *targetSystemd) Start() error {
	_, err := subprocess.RunCommand("systemctl", "start", "incus.service", "incus.socket")
	if err != nil {
		return err
	}

	// Wait for the socket to become available.
	time.Sleep(5 * time.Second)

	return nil
}

func (s *targetSystemd) Connect() (incus.InstanceServer, error) {
	return incus.ConnectIncusUnix("/var/lib/incus/unix.socket", nil)
}

func (s *targetSystemd) Paths() (*DaemonPaths, error) {
	return &DaemonPaths{
		Daemon: "/var/lib/incus/",
		Logs:   "/var/log/incus/",
		Cache:  "/var/cache/incus/",
	}, nil
}

func (s *targetSystemd) Name() string {
	return "systemd"
}
