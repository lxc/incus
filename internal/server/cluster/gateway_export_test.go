package cluster

import (
	"github.com/lxc/incus/v7/internal/server/db"
	localtls "github.com/lxc/incus/v7/shared/tls"
)

// ServerCert returns the gateway's internal TLS server certificate information.
func (g *Gateway) ServerCert() *localtls.CertInfo {
	return g.networkCert
}

// NetworkCert returns the gateway's internal TLS NetworkCert certificate information.
func (g *Gateway) NetworkCert() *localtls.CertInfo {
	return g.networkCert
}

// RaftNodes returns the nodes currently part of the raft cluster.
func (g *Gateway) RaftNodes() ([]db.RaftNode, error) {
	return g.currentRaftNodes()
}
