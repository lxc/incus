package main

import (
	"time"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

type targetOpenRC struct{}

func (s *targetOpenRC) Present() bool {
	if !util.PathExists("/var/lib/incus/") {
		return false
	}

	_, err := subprocess.RunCommand("rc-service", "--exists", "incus")
	if err != nil {
		return false
	}

	return true
}

func (s *targetOpenRC) Stop() error {
	_, err := subprocess.RunCommand("rc-service", "incus", "stop")
	return err
}

func (s *targetOpenRC) Start() error {
	_, err := subprocess.RunCommand("rc-service", "incus", "start")
	if err != nil {
		return err
	}

	// Wait for the socket to become available.
	time.Sleep(5 * time.Second)

	return nil
}

func (s *targetOpenRC) Connect() (incus.InstanceServer, error) {
	return incus.ConnectIncusUnix("/var/lib/incus/unix.socket", nil)
}

func (s *targetOpenRC) Paths() (*DaemonPaths, error) {
	return &DaemonPaths{
		Daemon: "/var/lib/incus/",
		Logs:   "/var/log/incus/",
		Cache:  "/var/cache/incus/",
	}, nil
}

func (s *targetOpenRC) Name() string {
	return "openrc"
}
