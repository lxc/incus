package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"

	internalIO "github.com/lxc/incus/v6/internal/io"
	"github.com/lxc/incus/v6/internal/server/response"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/logger"
)

func restServer(tlsConfig *tls.Config, cert *x509.Certificate, debug bool, d *Daemon) *http.Server {
	router := http.NewServeMux()

	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = response.SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for _, c := range api10 {
		createCmd(router, "1.0", c, cert, debug, d)
	}

	return &http.Server{Handler: router, TLSConfig: tlsConfig}
}

func createCmd(restAPI *http.ServeMux, version string, c APIEndpoint, cert *x509.Certificate, debug bool, d *Daemon) {
	var uri string
	if c.Path == "" {
		uri = fmt.Sprintf("/%s", version)
	} else {
		uri = fmt.Sprintf("/%s/%s", version, c.Path)
	}

	restAPI.HandleFunc(uri, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if !authenticate(r, cert) {
			logger.Error("Not authorized")
			_ = response.InternalError(errors.New("Not authorized")).Render(w)
			return
		}

		// Dump full request JSON when in debug mode
		if r.Method != "GET" && localUtil.IsJSONRequest(r) {
			newBody := &bytes.Buffer{}
			captured := &bytes.Buffer{}
			multiW := io.MultiWriter(newBody, captured)
			_, err := io.Copy(multiW, r.Body)
			if err != nil {
				_ = response.InternalError(err).Render(w)
				return
			}

			r.Body = internalIO.BytesReadCloser{Buf: newBody}
			localUtil.DebugJSON("API Request", captured, logger.Log)
		}

		// Actually process the request
		var resp response.Response

		handleRequest := func(action APIEndpointAction) response.Response {
			if action.Handler == nil {
				return response.NotImplemented(nil)
			}

			return action.Handler(d, r)
		}

		switch r.Method {
		case "GET":
			resp = handleRequest(c.Get)
		case "PUT":
			resp = handleRequest(c.Put)
		case "POST":
			resp = handleRequest(c.Post)
		case "DELETE":
			resp = handleRequest(c.Delete)
		case "PATCH":
			resp = handleRequest(c.Patch)
		default:
			resp = response.NotFound(fmt.Errorf("Method %q not found", r.Method))
		}

		// Handle errors
		err := resp.Render(w)
		if err != nil {
			writeErr := response.InternalError(err).Render(w)
			if writeErr != nil {
				logger.Error("Failed writing error for HTTP response", logger.Ctx{"url": uri, "error": err, "writeErr": writeErr})
			}
		}
	})
}

func authenticate(r *http.Request, cert *x509.Certificate) bool {
	logger.Info("=== Authentication attempt ===")
	logger.Infof("Expected client cert Subject: %s", cert.Subject)
	logger.Infof("Expected client cert fingerprint SHA256: %x", sha256.Sum256(cert.Raw))
	
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(cert.Raw))
	clientCerts := map[string]x509.Certificate{fingerprint: *cert}
	logger.Infof("Added expected cert to trust store with fingerprint: %s", fingerprint)

	if r.TLS == nil {
		logger.Error("No TLS connection information available")
		return false
	}
	
	logger.Infof("Number of peer certificates received: %d", len(r.TLS.PeerCertificates))
	
	for i, peerCert := range r.TLS.PeerCertificates {
		logger.Infof("Peer cert %d Subject: %s", i, peerCert.Subject)
		peerFingerprint := fmt.Sprintf("%x", sha256.Sum256(peerCert.Raw))
		logger.Infof("Peer cert %d fingerprint SHA256: %s", i, peerFingerprint)
		logger.Infof("Comparing peer fingerprint %s with expected %s", peerFingerprint, fingerprint)
		logger.Infof("Raw bytes equal: %v", bytes.Equal(peerCert.Raw, cert.Raw))
		logger.Infof("Trust store size: %d", len(clientCerts))
		for k, v := range clientCerts {
			logger.Infof("Trust store entry: key=%s, cert subject=%s", k, v.Subject)
		}
		
		trusted, returnedFingerprint := localUtil.CheckTrustState(*peerCert, clientCerts, nil, false)
		if returnedFingerprint != "" {
			logger.Infof("Trust check returned fingerprint: %s", returnedFingerprint)
		}
		
		if trusted {
			logger.Info("=== Authentication SUCCESSFUL ===")
			return true
		} else {
			logger.Infof("Peer cert %d NOT trusted", i)
		}
	}

	logger.Error("=== Authentication FAILED - no trusted certificates ===")
	return false
}
