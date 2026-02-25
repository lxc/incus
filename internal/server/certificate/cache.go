package certificate

import (
	"crypto/x509"
	"encoding/pem"
	"sync"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// Cache represents an thread-safe in-memory cache of the certificates in the database.
type Cache struct {
	apiCertificates map[string]api.CertificatePut
	certificates    map[string]*x509.Certificate
	mu              sync.RWMutex
}

// SetCertificates sets the certificates on the Cache.
func (c *Cache) SetCertificates(certificates []*api.Certificate) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.apiCertificates = make(map[string]api.CertificatePut, len(certificates))
	c.certificates = make(map[string]*x509.Certificate, len(certificates))

	for _, certificate := range certificates {
		c.apiCertificates[certificate.Fingerprint] = certificate.CertificatePut

		certBlock, _ := pem.Decode([]byte(certificate.Certificate))
		if certBlock == nil {
			logger.Warn("Failed decoding certificate", logger.Ctx{"name": certificate.Name})
			continue
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			logger.Warn("Failed parsing certificate", logger.Ctx{"name": certificate.Name, "err": err})
			continue
		}

		c.certificates[certificate.Fingerprint] = cert
	}
}

// GetCertificatesAndProjects returns certificate and project maps.
func (c *Cache) GetCertificatesAndProjects() (map[Type]map[string]x509.Certificate, map[string][]string) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	certificates := map[Type]map[string]x509.Certificate{}
	projects := map[string][]string{}
	for fingerprint, certificate := range c.apiCertificates {
		certType, err := FromAPIType(certificate.Type)
		if err != nil {
			logger.Warn("Failed getting certificate type", logger.Ctx{"name": certificate.Name, "err": err})
			continue
		}

		cert, ok := c.certificates[fingerprint]
		if !ok {
			logger.Warn("Certificate data not found", logger.Ctx{"name": certificate.Name})
			continue
		}

		_, ok = certificates[certType]
		if !ok {
			certificates[certType] = map[string]x509.Certificate{}
		}

		certificates[certType][fingerprint] = *cert
		if certificate.Restricted {
			projects[fingerprint] = make([]string, len(certificate.Projects))
			copy(projects[fingerprint], certificate.Projects)
		}
	}

	return certificates, projects
}

// GetCertificates returns a certificate map.
func (c *Cache) GetCertificates() map[Type]map[string]x509.Certificate {
	certificates, _ := c.GetCertificatesAndProjects()
	return certificates
}

// GetProjects returns a project map.
func (c *Cache) GetProjects() map[string][]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	projects := map[string][]string{}

	for fingerprint, certificate := range c.apiCertificates {
		if certificate.Restricted {
			projects[fingerprint] = make([]string, len(certificate.Projects))
			copy(projects[fingerprint], certificate.Projects)
		}
	}

	return projects
}
