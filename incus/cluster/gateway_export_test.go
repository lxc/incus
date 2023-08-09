package cluster

import (
	"github.com/lxc/incus/incus/db"
	"github.com/lxc/incus/shared"
)

// IsLeader returns true if this node is the leader.
func (g *Gateway) IsLeader() (bool, error) {
	return g.isLeader()
}

// ServerCert returns the gateway's internal TLS server certificate information.
func (g *Gateway) ServerCert() *shared.CertInfo {
	return g.networkCert
}

// NetworkCert returns the gateway's internal TLS NetworkCert certificate information.
func (g *Gateway) NetworkCert() *shared.CertInfo {
	return g.networkCert
}

// RaftNodes returns the nodes currently part of the raft cluster.
func (g *Gateway) RaftNodes() ([]db.RaftNode, error) {
	return g.currentRaftNodes()
}
