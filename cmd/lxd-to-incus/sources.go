package main

import (
	"github.com/lxc/incus/client"
)

type Source interface {
	Present() bool
	Stop() error
	Start() error
	Purge() error
	Connect() (incus.InstanceServer, error)
	Paths() (*DaemonPaths, error)
	Name() string
}

var sources = []Source{
	&srcSnap{},
	&srcDeb{},
	&srcCOPR{},
	&srcManual{},
}
