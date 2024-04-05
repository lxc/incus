package cluster_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cowsql/go-cowsql/client"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/util"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// Set the cluster.https_address config key to the given address, and insert the
// address into the raft_nodes table.
//
// This effectively makes the node act as a database raft node.
func setRaftRole(t *testing.T, database *db.Node, address string) client.NodeStore {
	require.NoError(t, database.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		err := tx.UpdateConfig(map[string]string{"cluster.https_address": address})
		if err != nil {
			return err
		}

		_, err = tx.CreateRaftNode(address, "test")
		return err
	}))

	store := client.NewNodeStore(database.DB(), "main", "raft_nodes", "address")
	return store
}

// Create a new test HTTP server configured with the given TLS certificate and
// using the given handler.
func newServer(cert *localtls.CertInfo, handler http.Handler) *httptest.Server {
	server := httptest.NewUnstartedServer(handler)
	server.TLS = util.ServerTLSConfig(cert)
	server.StartTLS()
	return server
}
