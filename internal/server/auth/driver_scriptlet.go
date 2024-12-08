package auth

import (
	"context"
	"net/http"

	"github.com/lxc/incus/v6/internal/server/certificate"
	authScriptlet "github.com/lxc/incus/v6/internal/server/scriptlet/auth"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// Scriptlet represents a scriptlet authorizer.
type Scriptlet struct {
	commonAuthorizer
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

	authorized, err := authScriptlet.AuthorizationRun(logger.Log, details.actualDetails(), object.String(), string(entitlement))
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

	permissionChecker := func(o Object) bool {
		authorized, err := authScriptlet.AuthorizationRun(logger.Log, details.actualDetails(), o.String(), string(entitlement))
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
	return nil
}
