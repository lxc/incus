package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	incus "github.com/lxc/incus/v6/client"
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

	env, err := osGetEnvironment()
	if err != nil {
		return response.InternalError(err)
	}

	fullSrv := api.Server{ServerUntrusted: srv}
	fullSrv.Environment = *env

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
	if !osGuestAPISupport {
		return nil
	}

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
	if !osGuestAPISupport {
		return nil
	}

	d.DevIncusMu.Lock()
	d.DevIncusRunning = false
	d.DevIncusMu.Unlock()

	if servers["DevIncus"] != nil {
		return servers["DevIncus"].Close()
	}

	return nil
}

func getClient(CID uint32, port int, serverCertificate string) (*http.Client, error) {
	agentCertPath := "agent.crt"
	agentKeyPath := "agent.key"
	if runtime.GOOS == "windows" {
		agentCertPath = "C:\\Incus\\agent.crt"
		agentKeyPath = "C:\\Incus\\agent.key"
	}

	agentCert, err := os.ReadFile(agentCertPath)
	if err != nil {
		return nil, err
	}

	agentKey, err := os.ReadFile(agentKeyPath)
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
	logger.Info("Starting HTTP server for incus-agent")
	
	l, err := osGetListener(ports.HTTPSDefaultPort)
	if err != nil {
		return fmt.Errorf("Failed to get listener: %w", err)
	}
	logger.Infof("Listener created on port %d", ports.HTTPSDefaultPort)

	// Load the expected server certificate.
	logger.Info("Loading server.crt (expected client certificate for validation)")
	certPath := "server.crt"
	if runtime.GOOS == "windows" {
		certPath = "C:\\Incus\\server.crt"
	}
	cert, err := localtls.ReadCert(certPath)
	if err != nil {
		logger.Errorf("Failed to read server.crt: %v", err)
		return fmt.Errorf("Failed to read client certificate: %w", err)
	}
	logger.Infof("Loaded server.crt successfully, Subject: %s, Issuer: %s", cert.Subject, cert.Issuer)
	logger.Infof("server.crt fingerprint SHA256: %x", sha256.Sum256(cert.Raw))

	// Validate system time is within certificate validity window
	currentTime := time.Now()
	logger.Infof("Current system time: %s", currentTime.Format(time.RFC3339))
	logger.Infof("Certificate valid from: %s", cert.NotBefore.Format(time.RFC3339))
	logger.Infof("Certificate valid until: %s", cert.NotAfter.Format(time.RFC3339))
	
	if currentTime.Before(cert.NotBefore) {
		timeDiff := cert.NotBefore.Sub(currentTime)
		logger.Errorf("System time is %.0f minutes before certificate validity period", timeDiff.Minutes())
		logger.Error("CRITICAL: System clock appears to be behind. Certificate is not yet valid.")
		logger.Error("Please sync system time or wait for certificate to become valid.")
		return fmt.Errorf("System time (%s) is before certificate validity (%s). Time sync required", 
			currentTime.Format(time.RFC3339), cert.NotBefore.Format(time.RFC3339))
	}
	
	if currentTime.After(cert.NotAfter) {
		timeDiff := currentTime.Sub(cert.NotAfter)
		logger.Errorf("System time is %.0f minutes after certificate expiry", timeDiff.Minutes())
		logger.Error("CRITICAL: Certificate has expired.")
		return fmt.Errorf("Certificate expired on %s (current time: %s)", 
			cert.NotAfter.Format(time.RFC3339), currentTime.Format(time.RFC3339))
	}
	
	logger.Info("Certificate time validation passed")

	tlsConfig, err := serverTLSConfig()
	if err != nil {
		logger.Errorf("Failed to get TLS config: %v", err)
		return fmt.Errorf("Failed to get TLS config: %w", err)
	}
	logger.Info("TLS configuration created successfully")

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
