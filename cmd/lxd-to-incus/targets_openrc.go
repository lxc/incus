package main

import (
	"time"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

type targetOpenRC struct {
	service string
}

func (s *targetOpenRC) present() bool {
	if !util.PathExists("/var/lib/incus/") {
		return false
	}

	_, err := subprocess.RunCommand("rc-service", "--exists", "incus")
	if err == nil {
		s.service = "incus"
		return true
	}

	_, err = subprocess.RunCommand("rc-service", "--exists", "incusd")
	if err == nil {
		s.service = "incusd"
		return true
	}

	return false
}

func (s *targetOpenRC) stop() error {
	_, err := subprocess.RunCommand("rc-service", s.service, "stop")
	if err != nil {
		return err
	}

	// Wait for the service to fully stop.
	time.Sleep(5 * time.Second)

	return nil
}

func (s *targetOpenRC) start() error {
	_, err := subprocess.RunCommand("rc-service", s.service, "start")
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
