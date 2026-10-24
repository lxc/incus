package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/lxc/incus/v7/internal/server/certificate"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/util"
)

// TLS represents a TLS authorizer.
type TLS struct {
	commonAuthorizer
	certificates      *certificate.Cache
	projectConfigFunc func(ctx context.Context, projectName string) (map[string]string, error)
}

// inheritedObjectFeatures maps the object types that can be inherited from the default project to the project feature controlling it.
var inheritedObjectFeatures = map[ObjectType]string{
	ObjectTypeImage:         "features.images",
	ObjectTypeProfile:       "features.profiles",
	ObjectTypeStorageVolume: "features.storage.volumes",
	ObjectTypeStorageBucket: "features.storage.buckets",
	ObjectTypeNetwork:       "features.networks",
	ObjectTypeNetworkZone:   "features.networks.zones",
}

func (t *TLS) load(ctx context.Context, certificateCache *certificate.Cache, opts Opts) error {
	if certificateCache == nil {
		return errors.New("TLS authorization driver requires a certificate cache")
	}

	t.certificates = certificateCache
	t.projectConfigFunc = opts.projectConfigFunc
	return nil
}

// inheritsFromDefault returns whether any of the projects gets objects of the given type from the default project.
func (t *TLS) inheritsFromDefault(ctx context.Context, projectNames []string, objectType ObjectType) bool {
	feature, ok := inheritedObjectFeatures[objectType]
	if !ok || t.projectConfigFunc == nil {
		return false
	}

	for _, projectName := range projectNames {
		if projectName == api.ProjectDefaultName {
			return true
		}

		config, err := t.projectConfigFunc(ctx, projectName)
		if err != nil {
			continue
		}

		if util.IsFalseOrEmpty(config[feature]) {
			return true
		}
	}

	return false
}

// CheckPermission returns an error if the user does not have the given Entitlement on the given Object.
func (t *TLS) CheckPermission(ctx context.Context, r *http.Request, object Object, entitlement Entitlement) error {
	details, err := t.requestDetails(r)
	if err != nil {
		return api.StatusErrorf(http.StatusForbidden, "Failed to extract request details: %v", err)
	}

	if details.isInternalOrUnix() {
		return nil
	}

	authenticationProtocol := details.authenticationProtocol()
	if authenticationProtocol != api.AuthenticationMethodTLS {
		// Return nil. If the server has been configured with an authentication method but no associated authorization driver,
		// the default is to give these authenticated users admin privileges.
		return nil
	}

	certType, isNotRestricted, projectNames, err := t.certificateDetails(details.username())
	if err != nil {
		return err
	}

	// Metrics certificates only ever get metrics access.
	if certType == certificate.TypeMetrics {
		if entitlement == EntitlementCanViewMetrics {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	if isNotRestricted {
		return nil
	}

	if details.IsAllProjectsRequest {
		// Only admins (users with non-restricted certs) can use the all-projects parameter.
		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Check server level object types
	switch object.Type() {
	case ObjectTypeServer:
		if entitlement == EntitlementCanView || entitlement == EntitlementCanViewResources || entitlement == EntitlementCanViewMetrics {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	case ObjectTypeStoragePool, ObjectTypeCertificate:
		if entitlement == EntitlementCanView {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Don't allow project modifications.
	if object.Type() == ObjectTypeProject && entitlement == EntitlementCanEdit {
		return api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
	}

	// Check project level permissions against the certificates project list.
	projectName := object.Project()
	if slices.Contains(projectNames, projectName) {
		return nil
	}

	// Also allow read-only access to inherited resources, but only through a trusted project that inherits them.
	if object.Project() == api.ProjectDefaultName && entitlement == EntitlementCanView && slices.Contains(projectNames, details.ProjectName) && t.inheritsFromDefault(ctx, []string{details.ProjectName}, object.Type()) {
		return nil
	}

	return api.StatusErrorf(http.StatusForbidden, "User does not have permission for project %q", projectName)
}

// GetPermissionChecker returns a function that can be used to check whether a user has the required entitlement on an authorization object.
func (t *TLS) GetPermissionChecker(ctx context.Context, r *http.Request, entitlement Entitlement, objectType ObjectType) (PermissionChecker, error) {
	allowFunc := func(b bool) func(Object) bool {
		return func(Object) bool {
			return b
		}
	}

	details, err := t.requestDetails(r)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusForbidden, "Failed to extract request details: %v", err)
	}

	if details.isInternalOrUnix() {
		return allowFunc(true), nil
	}

	authenticationProtocol := details.authenticationProtocol()
	if authenticationProtocol != api.AuthenticationMethodTLS {
		// Allow all. If the server has been configured with an authentication method but no associated authorization driver,
		// the default is to give these authenticated users admin privileges.
		return allowFunc(true), nil
	}

	certType, isNotRestricted, projectNames, err := t.certificateDetails(details.username())
	if err != nil {
		return nil, err
	}

	// Metrics certificates only ever get metrics access.
	if certType == certificate.TypeMetrics {
		if entitlement != EntitlementCanViewMetrics {
			return nil, api.StatusErrorf(http.StatusForbidden, "Certificate is restricted")
		}

		if isNotRestricted {
			return allowFunc(true), nil
		}

		return func(o Object) bool {
			return slices.Contains(projectNames, o.Project())
		}, nil
	}

	if isNotRestricted {
		return allowFunc(true), nil
	}

	// Check server level object types
	switch objectType {
	case ObjectTypeServer:
		if entitlement == EntitlementCanView || entitlement == EntitlementCanViewResources || entitlement == EntitlementCanViewMetrics {
			return allowFunc(true), nil
		}

		return allowFunc(false), nil
	case ObjectTypeStoragePool, ObjectTypeCertificate:
		if entitlement == EntitlementCanView {
			return allowFunc(true), nil
		}

		return allowFunc(false), nil
	}

	// Error if user does not have access to the project (unless we're getting projects, where we want to filter the results).
	if !details.IsAllProjectsRequest && !slices.Contains(projectNames, details.ProjectName) && objectType != ObjectTypeProject {
		return allowFunc(false), nil
	}

	// Objects from the default project are only visible when the relevant certificate projects inherit them.
	inherited := false
	if entitlement == EntitlementCanView {
		if details.IsAllProjectsRequest {
			inherited = t.inheritsFromDefault(ctx, projectNames, objectType)
		} else if slices.Contains(projectNames, details.ProjectName) {
			inherited = t.inheritsFromDefault(ctx, []string{details.ProjectName}, objectType)
		}
	}

	// Filter objects by project.
	return func(object Object) bool {
		// Allow if the project is in the allowed set.
		if slices.Contains(projectNames, object.Project()) {
			return true
		}

		// Also allow read-only access to inherited resources.
		return inherited && object.Project() == api.ProjectDefaultName
	}, nil
}

// certificateDetails returns the certificate type, a boolean indicating if the certificate is *not* restricted, a slice of
// project names for this certificate, or an error if the certificate could not be found.
func (t *TLS) certificateDetails(fingerprint string) (certificate.Type, bool, []string, error) {
	certs, projects := t.certificates.GetCertificatesAndProjects()
	clientCerts := certs[certificate.TypeClient]
	_, ok := clientCerts[fingerprint]
	if ok {
		projectNames, ok := projects[fingerprint]
		if !ok {
			// Certificate is not restricted.
			return certificate.TypeClient, true, nil, nil
		}

		return certificate.TypeClient, false, projectNames, nil
	}

	// If not a client cert, could be a metrics cert. Only need to check one entitlement.
	metricCerts := certs[certificate.TypeMetrics]
	_, ok = metricCerts[fingerprint]
	if ok {
		projectNames, ok := projects[fingerprint]
		if !ok {
			// Certificate is not restricted.
			return certificate.TypeMetrics, true, nil, nil
		}

		return certificate.TypeMetrics, false, projectNames, nil
	}

	return -1, false, nil, api.StatusErrorf(http.StatusForbidden, "Client certificate not found")
}

// GetInstanceAccess returns the list of entities who have access to the instance.
func (t *TLS) GetInstanceAccess(ctx context.Context, projectName string, instanceName string) (*api.Access, error) {
	var access api.Access

	certificates, projects := t.certificates.GetCertificatesAndProjects()

	// client (type = 1)
	clientCertificates := certificates[1]

	for fingerprint := range clientCertificates {
		certificateProjects := projects[fingerprint]

		// Unrestricted
		if certificateProjects == nil {
			access = append(access, api.AccessEntry{
				Identifier: fingerprint,
				Role:       "admin",
				Provider:   "tls",
			})
		}

		// Restricted
		if slices.Contains(certificateProjects, projectName) {
			access = append(access, api.AccessEntry{
				Identifier: fingerprint,
				Role:       "operator",
				Provider:   "tls",
			})
		}
	}

	// metric (type = 3)
	metricCertificates := certificates[3]

	for fingerprint := range metricCertificates {
		certificateProjects := projects[fingerprint]

		// Unrestricted
		if certificateProjects == nil {
			access = append(access, api.AccessEntry{
				Identifier: fingerprint,
				Role:       "view",
				Provider:   "tls",
			})
		}

		// Restricted
		if slices.Contains(certificateProjects, projectName) {
			access = append(access, api.AccessEntry{
				Identifier: fingerprint,
				Role:       "view",
				Provider:   "tls",
			})
		}
	}

	return &access, nil
}

// GetProjectAccess returns the list of entities who have access to the project.
func (t *TLS) GetProjectAccess(ctx context.Context, projectName string) (*api.Access, error) {
	var access api.Access

	certificates, projects := t.certificates.GetCertificatesAndProjects()

	clientCerts := certificates[certificate.TypeClient]

	for fingerprint := range clientCerts {
		certificateProjects := projects[fingerprint]

		if certificateProjects == nil {
			access = append(access, api.AccessEntry{
				Identifier: fingerprint,
				Role:       "admin",
				Provider:   "tls",
			})
		}

		if slices.Contains(certificateProjects, projectName) {
			access = append(access, api.AccessEntry{
				Identifier: fingerprint,
				Role:       "operator",
				Provider:   "tls",
			})
		}
	}

	for fingerprint := range projects {
		certificateProjects := projects[fingerprint]

		if certificateProjects == nil {
			access = append(access, api.AccessEntry{
				Identifier: fingerprint,
				Role:       "view",
				Provider:   "tls",
			})
		}

		if slices.Contains(certificateProjects, projectName) {
			access = append(access, api.AccessEntry{
				Identifier: fingerprint,
				Role:       "view",
				Provider:   "tls",
			})
		}
	}

	return &access, nil
}
