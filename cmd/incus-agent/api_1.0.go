package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/mdlayher/vsock"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/ports"
	"github.com/lxc/incus/v6/internal/server/response"
	localvsock "github.com/lxc/incus/v6/internal/server/vsock"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	agentAPI "github.com/lxc/incus/v6/shared/api/agent"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
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
	operationWait,
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
		AuthMethods:   []string{api.AuthenticationMethodTLS},
	}

	uname, err := linux.Uname()
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
		Server:             "incus-agent",
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

	d.DevIncusMu.Lock()
	d.serverCID = data.CID
	d.serverPort = data.Port
	d.serverCertificate = data.Certificate
	d.DevIncusEnabled = data.DevIncus
	d.DevIncusMu.Unlock()

	return nil
}

func api10Put(d *Daemon, r *http.Request) response.Response {
	err := setConnectionInfo(d, r.Body)
	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	// Try connecting to the host.
	client, err := getClient(d.serverCID, int(d.serverPort), d.serverCertificate)
	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	server, err := incus.ConnectIncusHTTP(nil, client)
	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	defer server.Disconnect()

	// Let the host know, we were able to connect successfully.
	d.chConnected <- struct{}{}

	if d.DevIncusEnabled {
		err = startDevIncusServer(d)
	} else {
		err = stopDevIncusServer(d)
	}

	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	return response.EmptySyncResponse
}

func startDevIncusServer(d *Daemon) error {
	d.DevIncusMu.Lock()
	defer d.DevIncusMu.Unlock()

	// If a DevIncus server is already running, don't start a second one.
	if d.DevIncusRunning {
		return nil
	}

	servers["DevIncus"] = devIncusServer(d)

	// Prepare the DevIncus server.
	DevIncusListener, err := createDevIncuslListener("/dev")
	if err != nil {
		return err
	}

	d.DevIncusRunning = true

	// Start the DevIncus listener.
	go func() {
		err := servers["DevIncus"].Serve(DevIncusListener)
		if err != nil {
			d.DevIncusMu.Lock()
			d.DevIncusRunning = false
			d.DevIncusMu.Unlock()

			// http.ErrServerClosed can be ignored as this is returned when the server is closed intentionally.
			if !errors.Is(err, http.ErrServerClosed) {
				errChan <- err
			}
		}
	}()

	return nil
}

func stopDevIncusServer(d *Daemon) error {
	d.DevIncusMu.Lock()
	d.DevIncusRunning = false
	d.DevIncusMu.Unlock()

	if servers["DevIncus"] != nil {
		return servers["DevIncus"].Close()
	}

	return nil
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

	client, err := localvsock.HTTPClient(CID, port, string(agentCert), string(agentKey), serverCertificate)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func startHTTPServer(d *Daemon, debug bool) error {
	const CIDAny uint32 = 4294967295 // Equivalent to VMADDR_CID_ANY.

	// Setup the listener on wildcard CID for inbound connections from Incus.
	// We use the VMADDR_CID_ANY CID so that if the VM's CID changes in the future the listener still works.
	// A CID change can occur when restoring a stateful VM that was previously using one CID but is
	// subsequently restored using a different one.
	l, err := vsock.ListenContextID(CIDAny, ports.HTTPSDefaultPort, nil)
	if err != nil {
		return fmt.Errorf("Failed to listen on vsock: %w", err)
	}

	logger.Info("Started vsock listener")

	// Load the expected server certificate.
	cert, err := localtls.ReadCert("server.crt")
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
