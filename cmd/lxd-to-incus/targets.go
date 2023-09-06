package main

import (
	"time"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared"
)

type Target interface {
	Present() bool
	Stop() error
	Start() error
	Connect() (incus.InstanceServer, error)
	Paths() (*DaemonPaths, error)
}

var targets = []Target{&targetSystemd{}}

type targetSystemd struct{}

func (s *targetSystemd) Present() bool {
	if !shared.PathExists("/var/lib/incus/") {
		return false
	}

	if !shared.PathExists("/etc/systemd/system/incus.service") {
		return false
	}

	return true
}

func (s *targetSystemd) Stop() error {
	_, err := shared.RunCommand("systemctl", "stop", "incus")
	return err
}

func (s *targetSystemd) Start() error {
	_, err := shared.RunCommand("systemctl", "start", "incus")
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
