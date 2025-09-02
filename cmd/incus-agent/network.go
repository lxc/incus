package main

import (
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"net"
	"runtime"
	"sync"
	"time"

	"github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// A variation of the standard tls.Listener that supports atomically swapping
// the underlying TLS configuration. Requests served before the swap will
// continue using the old configuration.
type networkListener struct {
	net.Listener
	mu     sync.RWMutex
	config *tls.Config
}

func networkTLSListener(inner net.Listener, config *tls.Config) *networkListener {
	listener := &networkListener{
		Listener: inner,
		config:   config,
	}

	return listener
}

// Accept waits for and returns the next incoming TLS connection then use the
// current TLS configuration to handle it.
func (l *networkListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	return tls.Server(c, l.config), nil
}

func serverTLSConfig() (*tls.Config, error) {
	logger.Info("Creating server TLS configuration")
	certDir := "."
	if runtime.GOOS == "windows" {
		certDir = "C:\\Incus"
	}
	logger.Infof("Looking for certificates in directory: %s", certDir)
	
	certInfo, err := localtls.KeyPairAndCA(certDir, "agent", localtls.CertServer, false)
	if err != nil {
		logger.Errorf("Failed to load key pair and CA: %v", err)
		return nil, err
	}
	
	logger.Info("Successfully loaded agent.crt and agent.key")
	ca := certInfo.CA()
	if ca != nil {
		logger.Infof("CA certificate loaded, Subject: %s", ca.Subject)
	} else {
		logger.Info("No CA certificate found (agent.ca)")
	}
	
	keyPair := certInfo.KeyPair()
	if keyPair.Leaf != nil {
		logger.Infof("Agent certificate Subject: %s, Issuer: %s", 
			keyPair.Leaf.Subject, keyPair.Leaf.Issuer)
		logger.Infof("Agent certificate fingerprint SHA256: %x", 
			sha256.Sum256(keyPair.Leaf.Raw))
		
		// Validate system time is within agent certificate validity window
		currentTime := time.Now()
		logger.Infof("Checking agent cert validity - Current time: %s", currentTime.Format(time.RFC3339))
		logger.Infof("Agent cert valid from: %s", keyPair.Leaf.NotBefore.Format(time.RFC3339))
		logger.Infof("Agent cert valid until: %s", keyPair.Leaf.NotAfter.Format(time.RFC3339))
		
		if currentTime.Before(keyPair.Leaf.NotBefore) {
			timeDiff := keyPair.Leaf.NotBefore.Sub(currentTime)
			logger.Errorf("System time is %.0f minutes before agent certificate validity", timeDiff.Minutes())
			return nil, fmt.Errorf("System time is before agent certificate validity. Time sync required")
		}
		
		if currentTime.After(keyPair.Leaf.NotAfter) {
			timeDiff := currentTime.Sub(keyPair.Leaf.NotAfter)
			logger.Errorf("System time is %.0f minutes after agent certificate expiry", timeDiff.Minutes())
			return nil, fmt.Errorf("Agent certificate has expired")
		}
		
		logger.Info("Agent certificate time validation passed")
	} else {
		logger.Info("Agent certificate leaf not yet parsed")
	}

	tlsConfig := util.ServerTLSConfig(certInfo)
	logger.Info("TLS configuration created with server certificate")
	return tlsConfig, nil
}
