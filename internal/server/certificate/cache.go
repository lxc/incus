package certificate

import (
	"crypto/x509"
	"sync"
)

// Cache represents an thread-safe in-memory cache of the certificates in the database.
type Cache struct {
	// certificates is a map of certificate Type to map of certificate fingerprint to x509.Certificate.
	certificates map[Type]map[string]x509.Certificate

	// projects is a map of certificate fingerprint to slice of projects the certificate is restricted to.
	// If a certificate fingerprint is present in certificates, but not present in projects, it means the certificate is
	// not restricted.
	projects map[string][]string
	mu       sync.RWMutex
}

// SetCertificatesAndProjects sets both certificates and projects on the Cache.
func (c *Cache) SetCertificatesAndProjects(certificates map[Type]map[string]x509.Certificate, projects map[string][]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.certificates = certificates
	c.projects = projects
}

// SetCertificates sets the certificates on the Cache.
func (c *Cache) SetCertificates(certificates map[Type]map[string]x509.Certificate) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.certificates = certificates
}

// SetProjects sets the projects on the Cache.
func (c *Cache) SetProjects(projects map[string][]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.projects = projects
}

// GetCertificatesAndProjects returns a read-only copy of the certificate and project maps.
func (c *Cache) GetCertificatesAndProjects() (map[Type]map[string]x509.Certificate, map[string][]string) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	certificates := make(map[Type]map[string]x509.Certificate, len(c.certificates))
	for t, m := range c.certificates {
		certificates[t] = make(map[string]x509.Certificate, len(m))
		for f, cert := range m {
			certificates[t][f] = cert
		}
	}

	projects := make(map[string][]string, len(c.projects))
	for f, projectNames := range c.projects {
		projectNamesCopy := make([]string, 0, len(projectNames))
		projectNamesCopy = append(projectNamesCopy, projectNames...)
		projects[f] = projectNamesCopy
	}

	return certificates, projects
}

// GetCertificates returns a read-only copy of the certificate map.
func (c *Cache) GetCertificates() map[Type]map[string]x509.Certificate {
	c.mu.RLock()
	defer c.mu.RUnlock()

	certificates := make(map[Type]map[string]x509.Certificate, len(c.certificates))
	for t, m := range c.certificates {
		certificates[t] = make(map[string]x509.Certificate, len(m))
		for f, cert := range m {
			certificates[t][f] = cert
		}
	}

	return certificates
}

// GetProjects returns a read-only copy of the project map.
func (c *Cache) GetProjects() map[string][]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	projects := make(map[string][]string, len(c.projects))
	for f, projectNames := range c.projects {
		projectNamesCopy := make([]string, 0, len(projectNames))
		projectNamesCopy = append(projectNamesCopy, projectNames...)
		projects[f] = projectNamesCopy
	}

	return projects
}
