package cluster

import (
	"context"

	"github.com/cowsql/go-cowsql/cluster"
	"github.com/cowsql/go-cowsql/cluster/gateway"
	"github.com/cowsql/go-cowsql/cluster/membership"
	"github.com/cowsql/go-cowsql/cluster/options"
	cowsqltls "github.com/cowsql/go-cowsql/cluster/tls"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/tls"
)

// NewGateway instantiates a new cluster gateway.
func NewGateway(ctx context.Context, database *db.Node, stateFunc func() *state.State, networkCert *tls.CertInfo, serverCertFunc func() *tls.CertInfo, opts ...options.Option) (cluster.Gateway, error) {
	return gateway.NewGateway(
		ctx,
		cowsqlNode(database),
		networkCert,
		func() cowsqltls.CertInfo { return serverCertFunc() },
		cowsqlState(stateFunc),
		opts...,
	)
}

// SetClusterDB applies the cluster database to the gateway.
func SetClusterDB(g cluster.Gateway, database *db.Cluster) {
	if database != nil {
		g.SetClusterDB(cowsqlCluster(database))
	} else {
		g.SetClusterDB(nil)
	}
}

// Enabled returns whether clustering is enabled.
func Enabled(database *db.Node) (bool, error) {
	return membership.Enabled(cowsqlNode(database))
}

// Recover attempts data recovery on the cluster database.
func Recover(database *db.Node) error {
	return membership.Recover(cowsqlNode(database))
}

// Reconfigure replaces the entire cluster configuration.
// Addresses and node roles may be updated. Node IDs are read-only.
func Reconfigure(database *db.Node, raftNodes []db.RaftNode) error {
	return membership.Reconfigure(cowsqlNode(database), raftNodes, createReconfigurePatch)
}

// ListDatabaseNodes returns a list of database node names.
func ListDatabaseNodes(database *db.Node) ([]string, error) {
	return membership.ListDatabaseNodes(cowsqlNode(database))
}
