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
	Name() string
}

var sources = []Source{
	&srcSnap{},
	&srcDeb{},
	&srcCOPR{},
	&srcManual{},
}
