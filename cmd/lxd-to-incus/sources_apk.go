package main

import (
	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

type srcAPK struct{}

func (s *srcAPK) present() bool {
	if !util.PathExists("/var/lib/incus/") {
		return false
	}

	_, err := subprocess.RunCommand("rc-service", "--exists", "incusd")
	return err == nil
}

func (s *srcAPK) name() string {
	return "apk package"
}

func (s *srcAPK) stop() error {
	_, err := subprocess.RunCommand("rc-service", "lxd", "stop")
	return err
}

func (s *srcAPK) start() error {
	_, err := subprocess.RunCommand("rc-service", "lxd", "start")
	return err
}

func (s *srcAPK) purge() error {
	_, err := subprocess.RunCommand("apk", "del", "lxd", "lxd-client")
	return err
}

func (s *srcAPK) connect() (incus.InstanceServer, error) {
	return incus.ConnectIncusUnix("/var/lib/lxd/unix.socket", &incus.ConnectionArgs{SkipGetServer: true})
}

func (s *srcAPK) paths() (*daemonPaths, error) {
	return &daemonPaths{
		daemon: "/var/lib/lxd",
		logs:   "/var/log/lxd",
		cache:  "/var/cache/lxd",
	}, nil
}
