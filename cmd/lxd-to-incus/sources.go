package main

import (
	"github.com/lxc/incus/v6/client"
)

type source interface {
	present() bool
	stop() error
	start() error
	purge() error
	connect() (incus.InstanceServer, error)
	paths() (*daemonPaths, error)
	name() string
}

var sources = []source{
	&srcSnap{},
	&srcDeb{},
	&srcCOPR{},
	&srcXbps{},
	&srcAPK{},
	&srcManual{},
}
