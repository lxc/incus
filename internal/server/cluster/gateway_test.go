package cluster_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	cowsqlcluster "github.com/cowsql/go-cowsql/cluster"
	"github.com/cowsql/go-cowsql/cluster/membership"
	"github.com/cowsql/go-cowsql/cluster/options"
	cowsqltls "github.com/cowsql/go-cowsql/cluster/tls"
	"github.com/cowsql/go-cowsql/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/cluster"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/internal/version"
	localtls "github.com/lxc/incus/v7/shared/tls"
	"github.com/lxc/incus/v7/shared/tls/tlstest"
)

func gatewayAccess(t *testing.T, gateway cowsqlcluster.Gateway, trustedCerts func() (map[string]x509.Certificate, error)) func(w http.ResponseWriter, r *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		var certs map[string]x509.Certificate
		if trustedCerts != nil {
			var err error
			certs, err = trustedCerts()
			if err != nil {
				http.Error(w, "403 failed to read trusted certificate cache", http.StatusForbidden)

				return false
			}
		}

		networkCert, ok := gateway.NetworkCert().(*localtls.CertInfo)
		require.True(t, ok)
		serverCert, ok := gateway.ServerCert().(*localtls.CertInfo)
		require.True(t, ok)
		if !cluster.CheckCert(r, networkCert, serverCert, certs) {
			http.Error(w, "403 invalid client certificate", http.StatusForbidden)

			return false
		}

		return true
	}
}

// Basic creation and shutdown. By default, the gateway runs an in-memory gRPC
// server.
func TestGateway_Single(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := tlstest.TestingKeyPair(t)

	s := &state.State{
		ServerCert: func() *localtls.CertInfo { return cert },
	}

	gateway := newGateway(t, node, cert, s)
	defer func() { _ = gateway.ShutdownServer() }()

	handlerFuncs := gateway.HandlerFuncs(gatewayAccess(t, gateway, nil))
	assert.Len(t, handlerFuncs, 1)
	for endpoint, f := range handlerFuncs {
		c, err := x509.ParseCertificate(cert.KeyPair().Certificate[0])
		require.NoError(t, err)
		w := httptest.NewRecorder()
		r := &http.Request{}
		r.Header = http.Header{}
		r.Header.Set("X-Dqlite-Version", "1")
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{c},
		}

		f(w, r)
		assert.Equal(t, 404, w.Code, endpoint)
	}

	dial := gateway.DialFunc()
	netConn, err := dial(context.Background(), "")
	assert.NoError(t, err)
	assert.NotNil(t, netConn)
	require.NoError(t, netConn.Close())

	leader, err := gateway.LeaderAddress()
	assert.Equal(t, "", leader)
	assert.EqualError(t, err, membership.ErrNodeIsNotClustered.Error())

	cowsqlDriver, err := driver.New(
		gateway.NodeStore(),
		driver.WithDialFunc(gateway.DialFunc()),
	)
	require.NoError(t, err)

	conn, err := cowsqlDriver.Open("test.db")
	require.NoError(t, err)

	require.NoError(t, conn.Close())
}

// If there's a network address configured, we expose the cowsql endpoint with
// an HTTP handler.
func TestGateway_SingleWithNetworkAddress(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := tlstest.TestingKeyPair(t)
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, node, address)

	s := &state.State{
		ServerCert: func() *localtls.CertInfo { return cert },
	}

	gateway := newGateway(t, node, cert, s)
	defer func() { _ = gateway.ShutdownServer() }()

	for path, handler := range gateway.HandlerFuncs(gatewayAccess(t, gateway, nil)) {
		mux.HandleFunc(path, handler)
	}

	cowsqlDriver, err := driver.New(
		gateway.NodeStore(),
		driver.WithDialFunc(gateway.DialFunc()),
	)
	require.NoError(t, err)

	conn, err := cowsqlDriver.Open("test.db")
	require.NoError(t, err)

	require.NoError(t, conn.Close())

	leader, err := gateway.LeaderAddress()
	require.NoError(t, err)
	assert.Equal(t, address, leader)
}

// When networked, the grpc and raft endpoints requires the cluster
// certificate.
func TestGateway_NetworkAuth(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := tlstest.TestingKeyPair(t)
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, node, address)

	s := &state.State{
		ServerCert: func() *localtls.CertInfo { return cert },
	}

	gateway := newGateway(t, node, cert, s)
	defer func() { _ = gateway.ShutdownServer() }()

	for path, handler := range gateway.HandlerFuncs(gatewayAccess(t, gateway, nil)) {
		mux.HandleFunc(path, handler)
	}

	// Make a request using a certificate different than the cluster one.
	certAlt := tlstest.TestingAltKeyPair(t)
	config, err := cowsqltls.ClientConfig(certAlt, certAlt, false)
	config.InsecureSkipVerify = true // Skip client-side verification
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}

	for path := range gateway.HandlerFuncs(gatewayAccess(t, gateway, nil)) {
		url := fmt.Sprintf("https://%s%s", address, path)
		response, err := client.Head(url)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, response.StatusCode)
	}
}

// RaftNodes returns all nodes of the cluster.
func TestGateway_RaftNodesNotLeader(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := tlstest.TestingKeyPair(t)
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, node, address)

	s := &state.State{
		ServerCert: func() *localtls.CertInfo { return cert },
	}

	gateway := newGateway(t, node, cert, s)
	defer func() { _ = gateway.ShutdownServer() }()

	nodes, err := gateway.CurrentRaftNodes(context.TODO())
	require.NoError(t, err)

	assert.Len(t, nodes, 1)
	assert.Equal(t, nodes[0].ID, uint64(1))
	assert.Equal(t, nodes[0].Address, address)
}

// Create a new test Gateway with the given parameters, and ensure no error happens.
func newGateway(t *testing.T, node *db.Node, networkCert *localtls.CertInfo, s *state.State, opts ...options.Option) cowsqlcluster.Gateway {
	require.NoError(t, os.Mkdir(filepath.Join(node.Dir(), "global"), 0o755))
	stateFunc := func() *state.State { return s }

	allOpts := []options.Option{
		options.Latency(0.2),
		options.LogLevel("TRACE"),
		options.Version(version.Version),
	}

	allOpts = append(allOpts, opts...)

	gateway, err := cluster.NewGateway(context.Background(), node, stateFunc, networkCert, stateFunc().ServerCert, allOpts...)
	require.NoError(t, err)

	if s.DB != nil {
		cluster.SetClusterDB(gateway, s.DB.Cluster)
	}

	return gateway
}
