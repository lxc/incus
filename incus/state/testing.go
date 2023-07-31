//go:build linux && cgo && !agent

package state

import (
	"context"
	"testing"

	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/firewall"
	"github.com/canonical/lxd/lxd/sys"
)

// NewTestState returns a State object initialized with testable instances of
// the node/cluster databases and of the OS facade.
//
// Return the newly created State object, along with a function that can be
// used for cleaning it up.
func NewTestState(t *testing.T) (*State, func()) {
	node, nodeCleanup := db.NewTestNode(t)
	cluster, clusterCleanup := db.NewTestCluster(t)
	os, osCleanup := sys.NewTestOS(t)

	cleanup := func() {
		nodeCleanup()
		clusterCleanup()
		osCleanup()
	}

	state := &State{
		ShutdownCtx:            context.TODO(),
		DB:                     &db.DB{Node: node, Cluster: cluster},
		OS:                     os,
		Firewall:               firewall.New(),
		UpdateCertificateCache: func() {},
		GlobalConfig:           &clusterConfig.Config{},
	}

	return state, cleanup
}
