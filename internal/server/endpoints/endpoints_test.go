package endpoints_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/endpoints"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/api"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/util"
)

// Return a new unstarted Endpoints instance, a Config with stub rest/devIncus
// servers, and a cleanup function that can be used to clear all state
// associated with the endpoints (e.g. the temporary var dir and any
// goroutine that was spawned by the tomb).
func newEndpoints(t *testing.T) (*endpoints.Endpoints, *endpoints.Config, func()) {
	dir, err := os.MkdirTemp("", "incus-endpoints-test-")
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "guestapi"), 0755))

	config := &endpoints.Config{
		Dir:            dir,
		UnixSocket:     filepath.Join(dir, "unix.socket"),
		RestServer:     newServer(),
		DevIncusServer: newServer(),
		Cert:           localtls.TestingKeyPair(),
		VsockServer:    newServer(),
	}

	endpoints := endpoints.Unstarted()

	cleanup := func() {
		assert.NoError(t, endpoints.Down())

		// We need to kick the garbage collector because otherwise FDs
		// will be left open and confuse the http.GetListeners() code
		// that detects socket activation.
		runtime.GC()

		if util.PathExists(dir) {
			require.NoError(t, os.RemoveAll(dir))
		}
	}

	return endpoints, config, cleanup
}

// Perform an HTTP GET "/" over the unix socket at the given path.
func httpGetOverUnixSocket(path string) error {
	dial := func(_ context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("unix", path)
	}

	client := &http.Client{Transport: &http.Transport{DialContext: dial}}
	_, err := client.Get("http://unix.socket/")
	return err
}

// Perform an HTTP GET "/" over TLS, using the given network address and server
// certificate.
func httpGetOverTLSSocket(addr string, cert *localtls.CertInfo) error {
	tlsConfig, _ := localtls.GetTLSConfigMem("", "", "", string(cert.PublicKey()), false)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}
	_, err := client.Get(fmt.Sprintf("https://%s/", addr))
	return err
}

// Returns a minimal stub for the REST API server, just realistic
// enough to make incus.ConnectIncusUnix succeed.
func newServer() *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/1.0/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = localUtil.WriteJSON(w, api.ResponseRaw{}, nil)
	})
	return &http.Server{Handler: mux, ErrorLog: log.New(io.Discard, "", 0)}
}

// Set the environment-variable for socket-based activation using the given
// file.
func setupSocketBasedActivation(endpoints *endpoints.Endpoints, file *os.File) {
	_ = os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	_ = os.Setenv("LISTEN_FDS", "1")
	endpoints.SystemdListenFDsStart(int(file.Fd()))
}

// Assert that there are no socket-based activation variables in the
// environment.
func assertNoSocketBasedActivation(t *testing.T) {
	// The environment variables are automatically cleaned, to avoid
	// confusing child processes or other logic.
	for _, name := range []string{"LISTEN_PID", "LISTEN_FDS"} {
		_, ok := os.LookupEnv(name)
		assert.Equal(t, false, ok)
	}
}
