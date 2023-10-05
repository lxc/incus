package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"

	log "github.com/sirupsen/logrus"

	"github.com/lxc/incus/internal/linux"
	internalUtil "github.com/lxc/incus/internal/util"
	localtls "github.com/lxc/incus/shared/tls"
	"github.com/lxc/incus/shared/util"
)

func tlsConfig(uid uint32) (*tls.Config, error) {
	// Load the client certificate.
	content, err := os.ReadFile(internalUtil.VarPath("users", fmt.Sprintf("%d", uid), "client.crt"))
	if err != nil {
		return nil, fmt.Errorf("Unable to open client certificate: %w", err)
	}

	tlsClientCert := string(content)

	// Load the client key.
	content, err = os.ReadFile(internalUtil.VarPath("users", fmt.Sprintf("%d", uid), "client.key"))
	if err != nil {
		return nil, fmt.Errorf("Unable to open client key: %w", err)
	}

	tlsClientKey := string(content)

	// Load the server certificate.
	certPath := internalUtil.VarPath("cluster.crt")
	if !util.PathExists(certPath) {
		certPath = internalUtil.VarPath("server.crt")
	}

	content, err = os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("Unable to open server certificate: %w", err)
	}

	tlsServerCert := string(content)

	return localtls.GetTLSConfigMem(tlsClientCert, tlsClientKey, "", tlsServerCert, false)
}

func proxyConnection(conn *net.UnixConn) {
	defer func() {
		_ = conn.Close()

		mu.Lock()
		connections -= 1
		mu.Unlock()
	}()

	// Increase counters.
	mu.Lock()
	transactions += 1
	connections += 1
	mu.Unlock()

	// Get credentials.
	creds, err := linux.GetUcred(conn)
	if err != nil {
		log.Errorf("Unable to get user credentials: %s", err)
		return
	}

	// Setup logging context.
	logger := log.WithFields(log.Fields{
		"uid": creds.Uid,
		"gid": creds.Gid,
		"pid": creds.Pid,
	})

	logger.Debug("Connected")
	defer logger.Debug("Disconnected")

	// Check if the user was setup.
	if !util.PathExists(internalUtil.VarPath("users", fmt.Sprintf("%d", creds.Uid))) {
		log.Infof("Setting up for uid %d", creds.Uid)
		err := serverSetupUser(creds.Uid)
		if err != nil {
			log.Errorf("Failed to setup new user: %v", err)
			return
		}
	}

	// Connect to the daemon.
	unixAddr, err := net.ResolveUnixAddr("unix", internalUtil.VarPath("unix.socket"))
	if err != nil {
		log.Errorf("Unable to resolve the target server: %v", err)
		return
	}

	client, err := net.DialUnix("unix", nil, unixAddr)
	if err != nil {
		log.Errorf("Unable to connect to target server: %v", err)
		return
	}

	defer func() { _ = client.Close() }()

	// Get the TLS configuration
	tlsConfig, err := tlsConfig(creds.Uid)
	if err != nil {
		log.Errorf("Failed to load TLS connection settings: %v", err)
		return
	}

	// Setup TLS.
	_, err = client.Write([]byte("STARTTLS\n"))
	if err != nil {
		log.Errorf("Failed to setup TLS connection to target server: %v", err)
		return
	}

	tlsClient := tls.Client(client, tlsConfig)

	// Establish the TLS handshake.
	err = tlsClient.Handshake()
	if err != nil {
		_ = conn.Close()
		log.Errorf("Failed TLS handshake with target server: %v", err)
		return
	}

	// Start proxying.
	go func() { _, _ = io.Copy(conn, tlsClient) }()
	_, _ = io.Copy(tlsClient, conn)
}
