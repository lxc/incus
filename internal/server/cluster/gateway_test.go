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

	"github.com/cowsql/go-cowsql/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/certificate"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/state"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// Basic creation and shutdown. By default, the gateway runs an in-memory gRPC
// server.
func TestGateway_Single(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := localtls.TestingKeyPair()

	s := &state.State{
		ServerCert: func() *localtls.CertInfo { return cert },
	}

	gateway := newGateway(t, node, cert, s)
	defer func() { _ = gateway.Shutdown() }()

	trustedCerts := func() map[certificate.Type]map[string]x509.Certificate {
		return nil
	}

	handlerFuncs := gateway.HandlerFuncs(nil, trustedCerts)
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
	assert.EqualError(t, err, cluster.ErrNodeIsNotClustered.Error())

	driver, err := driver.New(
		gateway.NodeStore(),
		driver.WithDialFunc(gateway.DialFunc()),
	)
	require.NoError(t, err)

	conn, err := driver.Open("test.db")
	require.NoError(t, err)

	require.NoError(t, conn.Close())
}

// If there's a network address configured, we expose the dqlite endpoint with
// an HTTP handler.
func TestGateway_SingleWithNetworkAddress(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := localtls.TestingKeyPair()
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, node, address)

	s := &state.State{
		ServerCert: func() *localtls.CertInfo { return cert },
	}

	gateway := newGateway(t, node, cert, s)
	defer func() { _ = gateway.Shutdown() }()

	trustedCerts := func() map[certificate.Type]map[string]x509.Certificate {
		return nil
	}

	for path, handler := range gateway.HandlerFuncs(nil, trustedCerts) {
		mux.HandleFunc(path, handler)
	}

	driver, err := driver.New(
		gateway.NodeStore(),
		driver.WithDialFunc(gateway.DialFunc()),
	)
	require.NoError(t, err)

	conn, err := driver.Open("test.db")
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

	cert := localtls.TestingKeyPair()
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, node, address)

	s := &state.State{
		ServerCert: func() *localtls.CertInfo { return cert },
	}

	gateway := newGateway(t, node, cert, s)
	defer func() { _ = gateway.Shutdown() }()

	trustedCerts := func() map[certificate.Type]map[string]x509.Certificate {
		return nil
	}

	for path, handler := range gateway.HandlerFuncs(nil, trustedCerts) {
		mux.HandleFunc(path, handler)
	}

	// Make a request using a certificate different than the cluster one.
	certAlt := localtls.TestingAltKeyPair()
	config, err := cluster.TLSClientConfig(certAlt, certAlt)
	config.InsecureSkipVerify = true // Skip client-side verification
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}

	for path := range gateway.HandlerFuncs(nil, trustedCerts) {
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

	cert := localtls.TestingKeyPair()
	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	setRaftRole(t, node, address)

	s := &state.State{
		ServerCert: func() *localtls.CertInfo { return cert },
	}

	gateway := newGateway(t, node, cert, s)
	defer func() { _ = gateway.Shutdown() }()

	nodes, err := gateway.RaftNodes()
	require.NoError(t, err)

	assert.Len(t, nodes, 1)
	assert.Equal(t, nodes[0].ID, uint64(1))
	assert.Equal(t, nodes[0].Address, address)
}

// Create a new test Gateway with the given parameters, and ensure no error happens.
func newGateway(t *testing.T, node *db.Node, networkCert *localtls.CertInfo, s *state.State) *cluster.Gateway {
	require.NoError(t, os.Mkdir(filepath.Join(node.Dir(), "global"), 0755))
	stateFunc := func() *state.State { return s }
	gateway, err := cluster.NewGateway(context.Background(), node, networkCert, stateFunc, cluster.Latency(0.2), cluster.LogLevel("TRACE"))
	require.NoError(t, err)
	return gateway
}
