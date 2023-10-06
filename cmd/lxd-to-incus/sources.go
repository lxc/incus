package main

import (
	"github.com/canonical/lxd/client"
)

type Source interface {
	Present() bool
	Stop() error
	Start() error
	Purge() error
	Connect() (lxd.InstanceServer, error)
	Paths() (*DaemonPaths, error)
}

var sources = []Source{
	&srcSnap{},
	&srcDeb{},
}
