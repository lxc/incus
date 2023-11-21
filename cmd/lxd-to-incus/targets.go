package main

import (
	"github.com/lxc/incus/client"
)

type Target interface {
	Present() bool
	Stop() error
	Start() error
	Connect() (incus.InstanceServer, error)
	Paths() (*DaemonPaths, error)
	Name() string
}

var targets = []Target{&targetSystemd{}, &targetOpenRC{}}
