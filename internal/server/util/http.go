package util

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/lxc/incus/v7/internal/ports"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	localtls "github.com/lxc/incus/v7/shared/tls"
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
	hash256 := sha256.New()
	err := json.NewEncoder(hash256).Encode(data)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash256.Sum(nil)), nil
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
			return nil, errors.New("Invalid certificate")
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

				for _, revoked := range crl.RevokedCertificateEntries {
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

// CheckTrustStateIssuer checks whether the first certificate in the chain was issued by a CA certificate from
// the trust store and returns the fingerprint of that trust store entry.
func CheckTrustStateIssuer(chain []*x509.Certificate, trustedCerts map[string]x509.Certificate, networkCert *localtls.CertInfo) (bool, string) {
	if len(chain) == 0 {
		return false, ""
	}

	// Never trust a certificate that the revocation list rejects.
	if networkCert != nil {
		ca := networkCert.CA()
		crl := networkCert.CRL()
		if ca != nil && crl != nil && chain[0].CheckSignatureFrom(ca) == nil && crl.CheckSignatureFrom(ca) == nil {
			for _, revoked := range crl.RevokedCertificateEntries {
				if chain[0].SerialNumber.Cmp(revoked.SerialNumber) == 0 {
					return false, ""
				}
			}
		}
	}

	intermediates := x509.NewCertPool()
	for _, cert := range chain[1:] {
		intermediates.AddCert(cert)
	}

	for fingerprint, v := range trustedCerts {
		// Only CA certificates can vouch for other certificates.
		if !v.BasicConstraintsValid || !v.IsCA {
			continue
		}

		roots := x509.NewCertPool()
		roots.AddCert(&v)

		_, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
		if err != nil {
			continue
		}

		logger.Debug("Matched trusted CA cert", logger.Ctx{"fingerprint": fingerprint, "subject": chain[0].Subject})
		return true, fingerprint
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

// BearerToken returns the bearer token from the request's Authorization header, if any.
func BearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}

	parts := strings.Split(auth, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return ""
	}

	return parts[1]
}

// CheckJwtToken checks whether the given request has JWT token that is valid and
// signed with client certificate from the trusted certificates.
// The token is taken from the Authorization header, the access_token query parameter or the tls_jwt cookie.
// Returns whether or not the token is valid, the fingerprint of the certificate and the certificate.
func CheckJwtToken(r *http.Request, trustedCerts map[string]x509.Certificate) (bool, string, *x509.Certificate) {
	// Try to get token from Authorization header.
	tokenString := BearerToken(r)

	// If no token from header, check access_token query parameter.
	if tokenString == "" {
		tokenString = r.URL.Query().Get("access_token")
	}

	// If still no token, check the cookie set through the TLS JWT login endpoint.
	if tokenString == "" {
		cookie, err := r.Cookie("tls_jwt")
		if err == nil {
			tokenString = cookie.Value
		}
	}

	// Return if no token found.
	if tokenString == "" {
		return false, "", nil
	}

	cert, _, err := VerifyJwtToken(tokenString, trustedCerts)
	if err != nil {
		return false, "", nil
	}

	return true, localtls.CertFingerprint(cert), cert
}

// VerifyJwtToken checks that the JWT token is currently valid and signed by one of the trusted certificates.
// Returns the signing certificate and the token expiry, if any.
func VerifyJwtToken(tokenString string, trustedCerts map[string]x509.Certificate) (*x509.Certificate, *time.Time, error) {
	// Get a new JWT parser.
	jwtParser := jwt.NewParser()

	// Parse the token.
	token, tokenParts, err := jwtParser.ParseUnverified(tokenString, &jwt.RegisteredClaims{})
	if err != nil {
		return nil, nil, err
	}

	if len(tokenParts) != 3 {
		return nil, nil, errors.New("Malformed token")
	}

	// Make sure this isn't an OIDC JWT.
	issuer, err := token.Claims.GetIssuer()
	if err != nil {
		return nil, nil, err
	}

	if issuer != "" {
		return nil, nil, errors.New("Token has an issuer")
	}

	// Check if the token is valid (not before / expiration).
	notBefore, err := token.Claims.GetNotBefore()
	if err != nil {
		return nil, nil, err
	}

	expiresAt, err := token.Claims.GetExpirationTime()
	if err != nil {
		return nil, nil, err
	}

	if notBefore != nil && time.Now().Before(notBefore.Time) {
		return nil, nil, errors.New("Token isn't valid yet")
	}

	if expiresAt != nil && time.Now().After(expiresAt.Time) {
		return nil, nil, errors.New("Token has expired")
	}

	// Find the certificate by the token subject.
	subject, err := token.Claims.GetSubject()
	if err != nil {
		return nil, nil, err
	}

	tokenCert, ok := trustedCerts[subject]
	if !ok {
		return nil, nil, errors.New("Token subject isn't a trusted certificate")
	}

	// Verify token signature against the original signing input.
	// Re-serializing the parsed claims would reorder or drop fields and
	// yield a different string than the one that was actually signed.
	tokenSignature, err := base64.RawURLEncoding.DecodeString(tokenParts[2])
	if err != nil {
		return nil, nil, err
	}

	tokenSigningString := tokenParts[0] + "." + tokenParts[1]

	err = token.Method.Verify(tokenSigningString, tokenSignature, tokenCert.PublicKey)
	if err != nil {
		return nil, nil, err
	}

	var expiry *time.Time
	if expiresAt != nil {
		expiry = &expiresAt.Time
	}

	return &tokenCert, expiry, nil
}
