// http://golang.org/src/pkg/crypto/tls/generate_cert.go
// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/proxy"
	"github.com/lxc/incus/v6/shared/util"
)

// KeyPairAndCA returns a CertInfo object with a reference to the key pair and
// (optionally) CA certificate located in the given directory and having the
// given name prefix
//
// The naming conversion for the various PEM encoded files is:
//
// <prefix>.crt -> public key
// <prefix>.key -> private key
// <prefix>.ca  -> CA certificate (optional)
// ca.crl       -> CA certificate revocation list (optional)
//
// If no public/private key files are found, a new key pair will be generated
// and saved on disk.
//
// If a CA certificate is found, it will be returned as well as second return
// value (otherwise it will be nil).
func KeyPairAndCA(dir, prefix string, kind CertKind, addHosts bool) (*CertInfo, error) {
	certFilename := filepath.Join(dir, prefix+".crt")
	keyFilename := filepath.Join(dir, prefix+".key")

	// Ensure that the certificate exists, or create a new one if it does
	// not.
	err := FindOrGenCert(certFilename, keyFilename, kind == CertClient, addHosts)
	if err != nil {
		return nil, err
	}

	// Load the certificate.
	keypair, err := tls.LoadX509KeyPair(certFilename, keyFilename)
	if err != nil {
		return nil, err
	}

	// If available, load the CA data as well.
	caFilename := filepath.Join(dir, prefix+".ca")
	var ca *x509.Certificate
	if util.PathExists(caFilename) {
		ca, err = ReadCert(caFilename)
		if err != nil {
			return nil, err
		}
	}

	crlFilename := filepath.Join(dir, "ca.crl")
	var crl *x509.RevocationList
	if util.PathExists(crlFilename) {
		data, err := os.ReadFile(crlFilename)
		if err != nil {
			return nil, err
		}

		pemData, _ := pem.Decode(data)
		if pemData == nil {
			return nil, errors.New("Invalid revocation list")
		}

		crl, err = x509.ParseRevocationList(pemData.Bytes)
		if err != nil {
			return nil, err
		}
	}

	info := &CertInfo{
		keypair: keypair,
		ca:      ca,
		crl:     crl,
	}

	return info, nil
}

// KeyPairFromRaw returns a CertInfo from the raw certificate and key.
func KeyPairFromRaw(certificate []byte, key []byte) (*CertInfo, error) {
	keypair, err := tls.X509KeyPair(certificate, key)
	if err != nil {
		return nil, err
	}

	return &CertInfo{
		keypair: keypair,
	}, nil
}

// CertInfo captures TLS certificate information about a certain public/private
// keypair and an optional CA certificate and CRL.
//
// Given support for PKI setups, these few bits of information are
// normally used and passed around together, so this structure helps with that
// (see doc/security.md for more details).
type CertInfo struct {
	keypair tls.Certificate
	ca      *x509.Certificate
	crl     *x509.RevocationList
}

// KeyPair returns the public/private key pair.
func (c *CertInfo) KeyPair() tls.Certificate {
	return c.keypair
}

// CA returns the CA certificate.
func (c *CertInfo) CA() *x509.Certificate {
	return c.ca
}

// PublicKey is a convenience to encode the underlying public key to ASCII.
func (c *CertInfo) PublicKey() []byte {
	data := c.KeyPair().Certificate[0]
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: data})
}

// PublicKeyX509 is a convenience to return the underlying public key as an *x509.Certificate.
func (c *CertInfo) PublicKeyX509() (*x509.Certificate, error) {
	return x509.ParseCertificate(c.KeyPair().Certificate[0])
}

// PrivateKey is a convenience to encode the underlying private key.
func (c *CertInfo) PrivateKey() []byte {
	ecKey, ok := c.KeyPair().PrivateKey.(*ecdsa.PrivateKey)
	if ok {
		data, err := x509.MarshalECPrivateKey(ecKey)
		if err != nil {
			return nil
		}

		return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: data})
	}

	rsaKey, ok := c.KeyPair().PrivateKey.(*rsa.PrivateKey)
	if ok {
		data := x509.MarshalPKCS1PrivateKey(rsaKey)
		return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: data})
	}

	return nil
}

// Fingerprint returns the fingerprint of the public key.
func (c *CertInfo) Fingerprint() string {
	fingerprint, err := CertFingerprintStr(string(c.PublicKey()))
	// Parsing should never fail, since we generated the cert ourselves,
	// but let's check the error for good measure.
	if err != nil {
		panic("invalid public key material")
	}

	return fingerprint
}

// CRL returns the certificate revocation list.
func (c *CertInfo) CRL() *x509.RevocationList {
	return c.crl
}

// CertKind defines the kind of certificate to generate from scratch in
// KeyPairAndCA when it's not there.
//
// The two possible kinds are client and server, and they differ in the
// ext-key-usage bitmaps. See GenerateMemCert for more details.
type CertKind int

// Possible kinds of certificates.
const (
	CertClient CertKind = iota
	CertServer
)

/*
 * Generate a list of names for which the certificate will be valid.
 * This will include the hostname and ip address.
 */
func mynames() ([]string, error) {
	h, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	ret := []string{h, "127.0.0.1/8", "::1/128"}
	return ret, nil
}

// FindOrGenCert generates a keypair if needed.
// The type argument is false for server, true for client.
func FindOrGenCert(certf string, keyf string, certtype bool, addHosts bool) error {
	if util.PathExists(certf) && util.PathExists(keyf) {
		return nil
	}

	/* If neither stat succeeded, then this is our first run and we
	 * need to generate cert and privkey */
	err := GenCert(certf, keyf, certtype, addHosts)
	if err != nil {
		return err
	}

	return nil
}

// GenCert will create and populate a certificate file and a key file.
func GenCert(certf string, keyf string, certtype bool, addHosts bool) error {
	/* Create the basenames if needed */
	dir := filepath.Dir(certf)
	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		return err
	}

	dir = filepath.Dir(keyf)
	err = os.MkdirAll(dir, 0o750)
	if err != nil {
		return err
	}

	certBytes, keyBytes, err := GenerateMemCert(certtype, addHosts)
	if err != nil {
		return err
	}

	certOut, err := os.Create(certf)
	if err != nil {
		return fmt.Errorf("Failed to open %s for writing: %w", certf, err)
	}

	_, err = certOut.Write(certBytes)
	if err != nil {
		return fmt.Errorf("Failed to write cert file: %w", err)
	}

	err = certOut.Close()
	if err != nil {
		return fmt.Errorf("Failed to close cert file: %w", err)
	}

	keyOut, err := os.OpenFile(keyf, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("Failed to open %s for writing: %w", keyf, err)
	}

	_, err = keyOut.Write(keyBytes)
	if err != nil {
		return fmt.Errorf("Failed to write key file: %w", err)
	}

	err = keyOut.Close()
	if err != nil {
		return fmt.Errorf("Failed to close key file: %w", err)
	}

	return nil
}

// GenerateMemCert creates client or server certificate and key pair,
// returning them as byte arrays in memory.
func GenerateMemCert(client bool, addHosts bool) ([]byte, []byte, error) {
	privk, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to generate key: %w", err)
	}

	validFrom := time.Now()
	validTo := validFrom.Add(10 * 365 * 24 * time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to generate serial number: %w", err)
	}

	userEntry, err := user.Current()
	var username string
	if err == nil {
		username = userEntry.Username
		if username == "" {
			username = "UNKNOWN"
		}
	} else {
		username = "UNKNOWN"
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "UNKNOWN"
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Linux Containers"},
			CommonName:   fmt.Sprintf("%s@%s", username, hostname),
		},
		NotBefore: validFrom,
		NotAfter:  validTo,

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	if client {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}

	if addHosts {
		hosts, err := mynames()
		if err != nil {
			return nil, nil, fmt.Errorf("Failed to get my hostname: %w", err)
		}

		for _, h := range hosts {
			ip, _, err := net.ParseCIDR(h)
			if err == nil {
				if !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() {
					template.IPAddresses = append(template.IPAddresses, ip)
				}
			} else {
				template.DNSNames = append(template.DNSNames, h)
			}
		}
	} else if !client {
		template.DNSNames = []string{"unspecified"}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privk.PublicKey, privk)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create certificate: %w", err)
	}

	data, err := x509.MarshalECPrivateKey(privk)
	if err != nil {
		return nil, nil, err
	}

	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	key := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: data})

	return cert, key, nil
}

// ReadCert reads a PEM encoded certificate.
func ReadCert(fpath string) (*x509.Certificate, error) {
	cf, err := os.ReadFile(fpath)
	if err != nil {
		return nil, err
	}

	certBlock, _ := pem.Decode(cf)
	if certBlock == nil {
		return nil, errors.New("Invalid certificate file")
	}

	return x509.ParseCertificate(certBlock.Bytes)
}

// CertFingerprint returns the SHA256 fingerprint string of an x509 certificate.
func CertFingerprint(cert *x509.Certificate) string {
	return fmt.Sprintf("%x", sha256.Sum256(cert.Raw))
}

// CertFingerprintStr returns the SHA256 fingerprint of a PEM encoded certificate.
func CertFingerprintStr(c string) (string, error) {
	pemCertificate, _ := pem.Decode([]byte(c))
	if pemCertificate == nil {
		return "", errors.New("invalid certificate")
	}

	cert, err := x509.ParseCertificate(pemCertificate.Bytes)
	if err != nil {
		return "", err
	}

	return CertFingerprint(cert), nil
}

// GetRemoteCertificate gets the x509 certificate from a remote HTTPS server.
func GetRemoteCertificate(address string, useragent string) (*x509.Certificate, error) {
	// Setup a permissive TLS config
	tlsConfig, err := GetTLSConfig(nil)
	if err != nil {
		return nil, err
	}

	tlsConfig.InsecureSkipVerify = true

	tr := &http.Transport{
		TLSClientConfig:       tlsConfig,
		DialContext:           RFC3493Dialer,
		Proxy:                 proxy.FromEnvironment,
		ExpectContinueTimeout: time.Second * 30,
		ResponseHeaderTimeout: time.Second * 3600,
		TLSHandshakeTimeout:   time.Second * 5,
	}

	// Connect
	req, err := http.NewRequest("GET", address, nil)
	if err != nil {
		return nil, err
	}

	if useragent != "" {
		req.Header.Set("User-Agent", useragent)
	}

	client := &http.Client{Transport: tr}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// Retrieve the certificate
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return nil, errors.New("Unable to read remote TLS certificate")
	}

	return resp.TLS.PeerCertificates[0], nil
}

// CertificateTokenDecode decodes a base64 and JSON encoded certificate add token.
func CertificateTokenDecode(input string) (*api.CertificateAddToken, error) {
	joinTokenJSON, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return nil, err
	}

	var j api.CertificateAddToken
	err = json.Unmarshal(joinTokenJSON, &j)
	if err != nil {
		return nil, err
	}

	if j.ClientName == "" {
		return nil, errors.New("No client name in certificate add token")
	}

	if len(j.Addresses) < 1 {
		return nil, errors.New("No server addresses in certificate add token")
	}

	if j.Secret == "" {
		return nil, errors.New("No secret in certificate add token")
	}

	if j.Fingerprint == "" {
		return nil, errors.New("No certificate fingerprint in certificate add token")
	}

	return &j, nil
}

// GenerateTrustCertificate converts the specified serverCert and serverName into an api.Certificate suitable for
// use as a trusted cluster server certificate.
func GenerateTrustCertificate(cert *CertInfo, name string) (*api.Certificate, error) {
	block, _ := pem.Decode(cert.PublicKey())
	if block == nil {
		return nil, errors.New("Failed to decode certificate")
	}

	fingerprint, err := CertFingerprintStr(string(cert.PublicKey()))
	if err != nil {
		return nil, fmt.Errorf("Failed to calculate fingerprint: %w", err)
	}

	certificate := base64.StdEncoding.EncodeToString(block.Bytes)
	apiCert := api.Certificate{
		CertificatePut: api.CertificatePut{
			Certificate: certificate,
			Name:        name,
			Type:        api.CertificateTypeServer, // Server type for intra-member communication.
		},
		Fingerprint: fingerprint,
	}

	return &apiCert, nil
}
