package util

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lxc/incus/v6/internal/ports"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// DebugJSON helper to log JSON.
// Accepts a title to prefix the JSON log with, a *bytes.Buffer containing the JSON and a logger to use for
// logging the JSON (allowing for custom context to be added to the log).
func DebugJSON(title string, r *bytes.Buffer, l logger.Logger) {
	pretty := &bytes.Buffer{}
	err := json.Indent(pretty, r.Bytes(), "\t", "\t")
	if err != nil {
		l.Debug("Error indenting JSON", logger.Ctx{"err": err})
		return
	}

	// Print the JSON without the last "\n"
	str := pretty.String()
	l.Debug(fmt.Sprintf("%s\n\t%s", title, str[0:len(str)-1]))
}

// WriteJSON encodes the body as JSON and sends it back to the client
// Accepts optional debugLogger that activates debug logging if non-nil.
func WriteJSON(w http.ResponseWriter, body any, debugLogger logger.Logger) error {
	var output io.Writer
	var captured *bytes.Buffer

	output = w
	if debugLogger != nil {
		captured = &bytes.Buffer{}
		output = io.MultiWriter(w, captured)
	}

	enc := json.NewEncoder(output)
	enc.SetEscapeHTML(false)
	err := enc.Encode(body)

	if captured != nil {
		DebugJSON("WriteJSON", captured, debugLogger)
	}

	return err
}

// EtagHash hashes the provided data and returns the sha256.
func EtagHash(data any) (string, error) {
	etag := sha256.New()
	err := json.NewEncoder(etag).Encode(data)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", etag.Sum(nil)), nil
}

// EtagCheck validates the hash of the current state with the hash
// provided by the client.
func EtagCheck(r *http.Request, data any) error {
	match := r.Header.Get("If-Match")
	if match == "" {
		return nil
	}

	match = strings.Trim(match, "\"")

	hash, err := EtagHash(data)
	if err != nil {
		return err
	}

	if hash != match {
		return api.StatusErrorf(http.StatusPreconditionFailed, "ETag doesn't match: %s vs %s", hash, match)
	}

	return nil
}

// HTTPClient returns an http.Client using the given certificate and proxy.
func HTTPClient(certificate string, proxy proxyFunc) (*http.Client, error) {
	var err error
	var cert *x509.Certificate

	if certificate != "" {
		certBlock, _ := pem.Decode([]byte(certificate))
		if certBlock == nil {
			return nil, fmt.Errorf("Invalid certificate")
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return nil, err
		}
	}

	tlsConfig, err := localtls.GetTLSConfig(cert)
	if err != nil {
		return nil, err
	}

	tr := &http.Transport{
		TLSClientConfig:       tlsConfig,
		DialContext:           localtls.RFC3493Dialer,
		Proxy:                 proxy,
		DisableKeepAlives:     true,
		ExpectContinueTimeout: time.Second * 30,
		ResponseHeaderTimeout: time.Second * 3600,
		TLSHandshakeTimeout:   time.Second * 5,
	}

	myhttp := http.Client{
		Transport: tr,
	}

	// Setup redirect policy
	myhttp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers
		req.Header = via[len(via)-1].Header

		return nil
	}

	return &myhttp, nil
}

// A function capable of proxing an HTTP request.
type proxyFunc func(req *http.Request) (*url.URL, error)

// ContextAwareRequest is an interface implemented by http.Request starting
// from Go 1.8. It supports graceful cancellation using a context.
type ContextAwareRequest interface {
	WithContext(ctx context.Context) *http.Request
}

// CheckTrustState checks whether the given client certificate is trusted
// (i.e. it has a valid time span and it belongs to the given list of trusted
// certificates).
// Returns whether or not the certificate is trusted, and the fingerprint of the certificate.
func CheckTrustState(cert x509.Certificate, trustedCerts map[string]x509.Certificate, networkCert *localtls.CertInfo, trustCACertificates bool) (bool, string) {
	// Extra validity check (should have been caught by TLS stack)
	if time.Now().Before(cert.NotBefore) || time.Now().After(cert.NotAfter) {
		return false, ""
	}

	if networkCert != nil && trustCACertificates {
		ca := networkCert.CA()

		if ca != nil && cert.CheckSignatureFrom(ca) == nil {
			// Check whether the certificate has been revoked.
			crl := networkCert.CRL()

			if crl != nil {
				if crl.CheckSignatureFrom(ca) != nil {
					return false, "" // CRL not signed by CA
				}

				for _, revoked := range crl.RevokedCertificates {
					if cert.SerialNumber.Cmp(revoked.SerialNumber) == 0 {
						return false, "" // Certificate is revoked, so not trusted anymore.
					}
				}
			}

			// Certificate not revoked, so trust it as is signed by CA cert.
			return true, localtls.CertFingerprint(&cert)
		}
	}

	// Check whether client certificate is in trust store.
	for fingerprint, v := range trustedCerts {
		if bytes.Equal(cert.Raw, v.Raw) {
			logger.Debug("Matched trusted cert", logger.Ctx{"fingerprint": fingerprint, "subject": v.Subject})
			return true, fingerprint
		}
	}

	return false, ""
}

// IsRecursionRequest checks whether the given HTTP request is marked with the
// "recursion" flag in its form values.
func IsRecursionRequest(r *http.Request) bool {
	recursionStr := r.FormValue("recursion")

	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		return false
	}

	return recursion != 0
}

// ListenAddresses returns a list of <host>:<port> combinations at which this machine can be reached.
// It accepts the configured listen address in the following formats: <host>, <host>:<port> or :<port>.
// If a listen port is not specified then then ports.HTTPSDefaultPort is used instead.
// If a non-empty and non-wildcard host is passed in then this functions returns a single element list with the
// listen address specified. Otherwise if an empty host or wildcard address is specified then all global unicast
// addresses actively configured on the host are returned. If an IPv4 wildcard address (0.0.0.0) is specified as
// the host then only IPv4 addresses configured on the host are returned.
func ListenAddresses(configListenAddress string) ([]string, error) {
	addresses := make([]string, 0)

	if configListenAddress == "" {
		return addresses, nil
	}

	// Check if configListenAddress is a bare IP address (wrapped with square brackets or unwrapped) or a
	// hostname (without port). If so then add the default port to the configListenAddress ready for parsing.
	unwrappedConfigListenAddress := strings.Trim(configListenAddress, "[]")
	listenIP := net.ParseIP(unwrappedConfigListenAddress)
	if listenIP != nil || !strings.Contains(unwrappedConfigListenAddress, ":") {
		// Use net.JoinHostPort so that IPv6 addresses are correctly wrapped ready for parsing below.
		configListenAddress = net.JoinHostPort(unwrappedConfigListenAddress, fmt.Sprintf("%d", ports.HTTPSDefaultPort))
	}

	// By this point we should always have the configListenAddress in form <host>:<port>, so lets check that.
	// This also ensures that any wrapped IPv6 addresses are unwrapped ready for comparison below.
	localHost, localPort, err := net.SplitHostPort(configListenAddress)
	if err != nil {
		return nil, err
	}

	if localHost == "" || localHost == "0.0.0.0" || localHost == "::" {
		ifaces, err := net.Interfaces()
		if err != nil {
			return addresses, err
		}

		for _, i := range ifaces {
			addrs, err := i.Addrs()
			if err != nil {
				continue
			}

			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}

				if !ip.IsGlobalUnicast() {
					continue
				}

				if ip.To4() == nil && localHost == "0.0.0.0" {
					continue
				}

				addresses = append(addresses, net.JoinHostPort(ip.String(), localPort))
			}
		}
	} else {
		addresses = append(addresses, net.JoinHostPort(localHost, localPort))
	}

	return addresses, nil
}

// IsJSONRequest returns true if the content type of the HTTP request is JSON.
func IsJSONRequest(r *http.Request) bool {
	for k, vs := range r.Header {
		if strings.ToLower(k) == "content-type" &&
			len(vs) == 1 && strings.ToLower(vs[0]) == "application/json" {
			return true
		}
	}

	return false
}

// CheckJwtToken checks whether the given request has JWT token that is valid and
// signed with client certificate from the trusted certificates.
// Returns whether or not the token is valid, the fingerprint of the certificate and the certificate.
func CheckJwtToken(r *http.Request, trustedCerts map[string]x509.Certificate) (bool, string, *x509.Certificate) {
	// Check if the request has a JWT token.
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return false, "", nil
	}

	parts := strings.Split(auth, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return false, "", nil
	}

	// Get a new JWT parser.
	jwtParser := jwt.NewParser()

	// Parse the token.
	token, tokenParts, err := jwtParser.ParseUnverified(parts[1], &jwt.RegisteredClaims{})
	if err != nil {
		return false, "", nil
	}

	if len(tokenParts) < 2 {
		return false, "", nil
	}

	// Make sure this isn't an OIDC JWT.
	issuer, err := token.Claims.GetIssuer()
	if err != nil {
		return false, "", nil
	}

	if issuer != "" {
		return false, "", nil
	}

	// Check if the token is valid.
	notBefore, err := token.Claims.GetNotBefore()
	if err != nil {
		return false, "", nil
	}

	expiresAt, err := token.Claims.GetExpirationTime()
	if err != nil {
		return false, "", nil
	}

	if (notBefore != nil && time.Now().Before(notBefore.Time)) || (expiresAt != nil && time.Now().After(expiresAt.Time)) {
		return false, "", nil
	}

	// Find the certificate by the token subject.
	subject, err := token.Claims.GetSubject()
	if err != nil {
		return false, "", nil
	}

	tokenCert, ok := trustedCerts[subject]
	if !ok {
		// No matching certificate.
		return false, "", nil
	}

	// Get the token signing string.
	tokenSigningString, err := token.SigningString()
	if err != nil {
		return false, "", nil
	}

	// Extract the token's signature.
	tokenSignature, err := base64.RawURLEncoding.DecodeString(tokenParts[2])
	if err != nil {
		return false, "", nil
	}

	// Validate that the token was signed by the certificate.
	err = token.Method.Verify(tokenSigningString, tokenSignature, tokenCert.PublicKey)
	if err != nil {
		return false, "", nil
	}

	return true, subject, &tokenCert
}
