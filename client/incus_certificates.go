package incus

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/lxc/incus/v6/shared/api"
)

// Certificate handling functions

// GetCertificateFingerprints returns a list of certificate fingerprints.
func (r *ProtocolIncus) GetCertificateFingerprints() ([]string, error) {
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
func (r *ProtocolIncus) GetCertificates() ([]api.Certificate, error) {
	certificates := []api.Certificate{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/certificates?recursion=1", nil, "", &certificates)
	if err != nil {
		return nil, err
	}

	return certificates, nil
}

// GetCertificatesWithFilter returns a filtered list of certificates.
func (r *ProtocolIncus) GetCertificatesWithFilter(filters []string) ([]api.Certificate, error) {
	certificates := []api.Certificate{}

	v := url.Values{}
	v.Set("recursion", "1")
	v.Set("filter", parseFilters(filters))

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/certificates?%s", v.Encode()), nil, "", &certificates)
	if err != nil {
		return nil, err
	}

	return certificates, nil
}

// GetCertificate returns the certificate entry for the provided fingerprint.
func (r *ProtocolIncus) GetCertificate(fingerprint string) (*api.Certificate, string, error) {
	certificate := api.Certificate{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/certificates/%s", url.PathEscape(fingerprint)), nil, "", &certificate)
	if err != nil {
		return nil, "", err
	}

	return &certificate, etag, nil
}

// CreateCertificate adds a new certificate to the Incus trust store.
func (r *ProtocolIncus) CreateCertificate(certificate api.CertificatesPost) error {
	// Send the request
	_, _, err := r.query("POST", "/certificates", certificate, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateCertificate updates the certificate definition.
func (r *ProtocolIncus) UpdateCertificate(fingerprint string, certificate api.CertificatePut, ETag string) error {
	if !r.HasExtension("certificate_update") {
		return errors.New("The server is missing the required \"certificate_update\" API extension")
	}

	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/certificates/%s", url.PathEscape(fingerprint)), certificate, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteCertificate removes a certificate from the Incus trust store.
func (r *ProtocolIncus) DeleteCertificate(fingerprint string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/certificates/%s", url.PathEscape(fingerprint)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// CreateCertificateToken requests a certificate add token.
func (r *ProtocolIncus) CreateCertificateToken(certificate api.CertificatesPost) (Operation, error) {
	if !r.HasExtension("certificate_token") {
		return nil, errors.New("The server is missing the required \"certificate_token\" API extension")
	}

	if !certificate.Token {
		return nil, errors.New("Token needs to be true if requesting a token")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", "/certificates", certificate, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}
