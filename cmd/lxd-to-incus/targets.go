package main

import (
	"github.com/lxc/incus/client"
)

type target interface {
	present() bool
	stop() error
	start() error
	connect() (incus.InstanceServer, error)
	paths() (*daemonPaths, error)
	name() string
}

var targets = []target{&targetSystemd{}, &targetOpenRC{}}
