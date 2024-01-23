package main

import (
	"time"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

type targetOpenRC struct{}

func (s *targetOpenRC) present() bool {
	if !util.PathExists("/var/lib/incus/") {
		return false
	}

	_, err := subprocess.RunCommand("rc-service", "--exists", "incus")
	return err == nil
}

func (s *targetOpenRC) stop() error {
	_, err := subprocess.RunCommand("rc-service", "incus", "stop")
	return err
}

func (s *targetOpenRC) start() error {
	_, err := subprocess.RunCommand("rc-service", "incus", "start")
	if err != nil {
		return err
	}

	// Wait for the socket to become available.
	time.Sleep(5 * time.Second)

	return nil
}

func (s *targetOpenRC) connect() (incus.InstanceServer, error) {
	return incus.ConnectIncusUnix("/var/lib/incus/unix.socket", &incus.ConnectionArgs{SkipGetServer: true})
}

func (s *targetOpenRC) paths() (*daemonPaths, error) {
	return &daemonPaths{
		daemon: "/var/lib/incus",
		logs:   "/var/log/incus",
		cache:  "/var/cache/incus",
	}, nil
}

func (s *targetOpenRC) name() string {
	return "openrc"
}
