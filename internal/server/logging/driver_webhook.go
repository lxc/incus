package logging

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
	incustls "github.com/lxc/incus/v6/shared/tls"
)

// WebhookLogger represents a webhook logger.
type WebhookLogger struct {
	common

	client   *http.Client
	address  string
	username string
	password string
	retry    int
}

// NewWebhookLogger instantiates a new webhook logger.
func NewWebhookLogger(s *state.State, name string) (*WebhookLogger, error) {
	address, username, password, caCertificate, retry := s.GlobalConfig.LoggingConfigForWebhook(name)

	client := &http.Client{}

	// Set defaults.
	if retry == 0 {
		retry = 3
	}

	// Setup the server for self-signed certirficates.
	if caCertificate != "" {
		// Prepare the TLS config.
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS13,
		}

		// Parse the provided certificate.
		certBlock, _ := pem.Decode([]byte(caCertificate))
		if certBlock == nil {
			return nil, errors.New("Invalid remote certificate")
		}

		serverCert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("Invalid remote certificate: %w", err)
		}

		// Add the certificate to the TLS config.
		incustls.TLSConfigWithTrustedCert(tlsConfig, serverCert)

		// Configure the HTTP client with our TLS config.
		client.Transport = &http.Transport{TLSClientConfig: tlsConfig}
	}

	return &WebhookLogger{
		common:   newCommonLogger(name, s.GlobalConfig),
		client:   client,
		address:  address,
		username: username,
		password: password,
		retry:    retry,
	}, nil
}

// HandleEvent handles the event received from the internal event listener.
func (c *WebhookLogger) HandleEvent(event api.Event) {
	// JSON data.
	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	// Prepare the request.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.address, bytes.NewReader(data))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")

	for range c.retry {
		resp, err := c.client.Do(req)
		if err != nil {
			// Wait 10s and try again.
			time.Sleep(10 * time.Second)

			continue
		}

		_ = resp.Body.Close()
	}
}

// Start starts the webhook logger.
func (c *WebhookLogger) Start() error {
	return nil
}

// Stop cleans up the webhook logger.
func (c *WebhookLogger) Stop() {
}

// Validate checks whether the logger configuration is correct.
func (c *WebhookLogger) Validate() error {
	if c.address == "" {
		return fmt.Errorf("%s: Address cannot be empty", c.name)
	}

	return nil
}
