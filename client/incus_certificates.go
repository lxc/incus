package incus

import (
	"fmt"
	"net/url"

	"github.com/cyphar/incus/shared/api"
)

// Certificate handling functions

// GetCertificateFingerprints returns a list of certificate fingerprints.
func (r *ProtocolLXD) GetCertificateFingerprints() ([]string, error) {
	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/certificates"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetCertificates returns a list of certificates.
func (r *ProtocolLXD) GetCertificates() ([]api.Certificate, error) {
	certificates := []api.Certificate{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/certificates?recursion=1", nil, "", &certificates)
	if err != nil {
		return nil, err
	}

	return certificates, nil
}

// GetCertificate returns the certificate entry for the provided fingerprint.
func (r *ProtocolLXD) GetCertificate(fingerprint string) (*api.Certificate, string, error) {
	certificate := api.Certificate{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/certificates/%s", url.PathEscape(fingerprint)), nil, "", &certificate)
	if err != nil {
		return nil, "", err
	}

	return &certificate, etag, nil
}

// CreateCertificate adds a new certificate to the LXD trust store.
func (r *ProtocolLXD) CreateCertificate(certificate api.CertificatesPost) error {
	// Send the request
	_, _, err := r.query("POST", "/certificates", certificate, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateCertificate updates the certificate definition.
func (r *ProtocolLXD) UpdateCertificate(fingerprint string, certificate api.CertificatePut, ETag string) error {
	if !r.HasExtension("certificate_update") {
		return fmt.Errorf("The server is missing the required \"certificate_update\" API extension")
	}

	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/certificates/%s", url.PathEscape(fingerprint)), certificate, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteCertificate removes a certificate from the LXD trust store.
func (r *ProtocolLXD) DeleteCertificate(fingerprint string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/certificates/%s", url.PathEscape(fingerprint)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// CreateCertificateToken requests a certificate add token.
func (r *ProtocolLXD) CreateCertificateToken(certificate api.CertificatesPost) (Operation, error) {
	if !r.HasExtension("certificate_token") {
		return nil, fmt.Errorf("The server is missing the required \"certificate_token\" API extension")
	}

	if !certificate.Token {
		return nil, fmt.Errorf("Token needs to be true if requesting a token")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", "/certificates", certificate, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}
