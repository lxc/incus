package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/mdlayher/vsock"

	"github.com/canonical/lxd/client"
	agentAPI "github.com/canonical/lxd/lxd-agent/api"
	"github.com/canonical/lxd/lxd/response"
	lxdvsock "github.com/canonical/lxd/lxd/vsock"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

var api10Cmd = APIEndpoint{
	Get: APIEndpointAction{Handler: api10Get},
	Put: APIEndpointAction{Handler: api10Put},
}

var api10 = []APIEndpoint{
	api10Cmd,
	execCmd,
	eventsCmd,
	metricsCmd,
	operationsCmd,
	operationCmd,
	operationWebsocket,
	sftpCmd,
	stateCmd,
}

func api10Get(d *Daemon, r *http.Request) response.Response {
	srv := api.ServerUntrusted{
		APIExtensions: version.APIExtensions,
		APIStatus:     "stable",
		APIVersion:    version.APIVersion,
		Public:        false,
		Auth:          "trusted",
		AuthMethods:   []string{"tls"},
	}

	uname, err := shared.Uname()
	if err != nil {
		return response.InternalError(err)
	}

	serverName, err := os.Hostname()
	if err != nil {
		return response.SmartError(err)
	}

	env := api.ServerEnvironment{
		Kernel:             uname.Sysname,
		KernelArchitecture: uname.Machine,
		KernelVersion:      uname.Release,
		Server:             "lxd-agent",
		ServerPid:          os.Getpid(),
		ServerVersion:      version.Version,
		ServerName:         serverName,
	}

	fullSrv := api.Server{ServerUntrusted: srv}
	fullSrv.Environment = env

	return response.SyncResponseETag(true, fullSrv, fullSrv)
}

func setConnectionInfo(d *Daemon, rd io.Reader) error {
	var data agentAPI.API10Put

	err := json.NewDecoder(rd).Decode(&data)
	if err != nil {
		return err
	}

	d.devlxdMu.Lock()
	d.serverCID = data.CID
	d.serverPort = data.Port
	d.serverCertificate = data.Certificate
	d.devlxdEnabled = data.Devlxd
	d.devlxdMu.Unlock()

	return nil
}

func api10Put(d *Daemon, r *http.Request) response.Response {
	err := setConnectionInfo(d, r.Body)
	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	// Try connecting to LXD server.
	client, err := getClient(d.serverCID, int(d.serverPort), d.serverCertificate)
	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	server, err := lxd.ConnectLXDHTTP(nil, client)
	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	defer server.Disconnect()

	// Let LXD know, we were able to connect successfully.
	d.chConnected <- struct{}{}

	if d.devlxdEnabled {
		err = startDevlxdServer(d)
	} else {
		err = stopDevlxdServer(d)
	}

	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	return response.EmptySyncResponse
}

func startDevlxdServer(d *Daemon) error {
	d.devlxdMu.Lock()
	defer d.devlxdMu.Unlock()

	// If a devlxd server is already running, don't start a second one.
	if d.devlxdRunning {
		return nil
	}

	servers["devlxd"] = devLxdServer(d)

	// Prepare the devlxd server.
	devlxdListener, err := createDevLxdlListener("/dev")
	if err != nil {
		return err
	}

	d.devlxdRunning = true

	// Start the devlxd listener.
	go func() {
		err := servers["devlxd"].Serve(devlxdListener)
		if err != nil {
			d.devlxdMu.Lock()
			d.devlxdRunning = false
			d.devlxdMu.Unlock()

			// http.ErrServerClosed can be ignored as this is returned when the server is closed intentionally.
			if !errors.Is(err, http.ErrServerClosed) {
				errChan <- err
			}
		}
	}()

	return nil
}

func stopDevlxdServer(d *Daemon) error {
	d.devlxdMu.Lock()
	d.devlxdRunning = false
	d.devlxdMu.Unlock()

	return servers["devlxd"].Close()
}

func getClient(CID uint32, port int, serverCertificate string) (*http.Client, error) {
	agentCert, err := os.ReadFile("agent.crt")
	if err != nil {
		return nil, err
	}

	agentKey, err := os.ReadFile("agent.key")
	if err != nil {
		return nil, err
	}

	client, err := lxdvsock.HTTPClient(CID, port, string(agentCert), string(agentKey), serverCertificate)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func startHTTPServer(d *Daemon, debug bool) error {
	// Setup the listener on VM's context ID for inbound connections from LXD.
	l, err := vsock.Listen(shared.HTTPSDefaultPort, nil)
	if err != nil {
		return fmt.Errorf("Failed to listen on vsock: %w", err)
	}

	logger.Info("Started vsock listener")

	// Load the expected server certificate.
	cert, err := shared.ReadCert("server.crt")
	if err != nil {
		return fmt.Errorf("Failed to read client certificate: %w", err)
	}

	tlsConfig, err := serverTLSConfig()
	if err != nil {
		return fmt.Errorf("Failed to get TLS config: %w", err)
	}

	// Prepare the HTTP server.
	servers["http"] = restServer(tlsConfig, cert, debug, d)

	// Start the server.
	go func() {
		err := servers["http"].Serve(networkTLSListener(l, tlsConfig))
		if !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}

		l.Close()
	}()

	return nil
}
