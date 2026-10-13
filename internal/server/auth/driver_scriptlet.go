package auth

import (
	"context"
	"crypto/x509"
	"net/http"

	"github.com/lxc/incus/v7/internal/server/auth/common"
	"github.com/lxc/incus/v7/internal/server/certificate"
	authScriptlet "github.com/lxc/incus/v7/internal/server/scriptlet/auth"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	localtls "github.com/lxc/incus/v7/shared/tls"
)

// Scriptlet represents a scriptlet authorizer.
type Scriptlet struct {
	commonAuthorizer
	certificates *certificate.Cache
}

// CheckPermission returns an error if the user does not have the given Entitlement on the given Object.
func (s *Scriptlet) CheckPermission(ctx context.Context, r *http.Request, object Object, entitlement Entitlement) error {
	details, err := s.requestDetails(r)
	if err != nil {
		return api.StatusErrorf(http.StatusForbidden, "Failed to extract request details: %v", err)
	}

	if details.isInternalOrUnix() {
		return nil
	}

	actualDetails := details.actualDetails()
	peerCertificates, apiCert := s.clientCertificates(r, actualDetails)

	authorized, err := authScriptlet.AuthorizationRun(logger.Log, actualDetails, peerCertificates, apiCert, object.String(), string(entitlement))
	if err != nil {
		return api.StatusErrorf(http.StatusForbidden, "Authorization scriptlet execution failed with error: %v", err)
	}

	if authorized {
		return nil
	}

	return api.StatusErrorf(http.StatusForbidden, "Permission denied")
}

// GetInstanceAccess returns the list of entities who have access to the instance.
func (s *Scriptlet) GetInstanceAccess(ctx context.Context, projectName string, instanceName string) (*api.Access, error) {
	return authScriptlet.GetInstanceAccessRun(logger.Log, projectName, instanceName)
}

// clientCertificates returns the certificate chain and trust-store entry to expose to the scriptlet.
// Only a genuinely TLS-authenticated client's own certificate is exposed, so a token, OIDC or
// cluster-forwarded client can't have the scriptlet judge it by a certificate it merely presented.
func (s *Scriptlet) clientCertificates(r *http.Request, details *common.RequestDetails) ([]*x509.Certificate, *api.CertificatePut) {
	if r.TLS == nil || details.Protocol != api.AuthenticationMethodTLS || len(r.TLS.PeerCertificates) == 0 {
		return nil, nil
	}

	// Only expose the chain when its leaf is the authenticated certificate.
	if localtls.CertFingerprint(r.TLS.PeerCertificates[0]) != details.Username {
		return nil, nil
	}

	var apiCert *api.CertificatePut
	if s.certificates != nil {
		apiCert = s.certificates.GetAPICertificate(details.Username)
	}

	return r.TLS.PeerCertificates, apiCert
}

// GetPermissionChecker returns a function that can be used to check whether a user has the required entitlement on an authorization object.
func (s *Scriptlet) GetPermissionChecker(ctx context.Context, r *http.Request, entitlement Entitlement, objectType ObjectType) (PermissionChecker, error) {
	allowFunc := func(b bool) func(Object) bool {
		return func(Object) bool {
			return b
		}
	}

	details, err := s.requestDetails(r)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusForbidden, "Failed to extract request details: %v", err)
	}

	if details.isInternalOrUnix() {
		return allowFunc(true), nil
	}

	actualDetails := details.actualDetails()
	peerCertificates, apiCert := s.clientCertificates(r, actualDetails)

	permissionChecker := func(o Object) bool {
		authorized, err := authScriptlet.AuthorizationRun(logger.Log, actualDetails, peerCertificates, apiCert, o.String(), string(entitlement))
		if err != nil {
			logger.Error("Authorization scriptlet execution failed", logger.Ctx{"err": err})
			return false
		}

		return authorized
	}

	return permissionChecker, nil
}

// GetProjectAccess returns the list of entities who have access to the project.
func (s *Scriptlet) GetProjectAccess(ctx context.Context, projectName string) (*api.Access, error) {
	return authScriptlet.GetProjectAccessRun(logger.Log, projectName)
}

func (s *Scriptlet) load(ctx context.Context, certificateCache *certificate.Cache, opts Opts) error {
	s.certificates = certificateCache
	return nil
}
