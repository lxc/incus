package auth

import (
	"context"
	"net/http"

	"github.com/lxc/incus/v7/internal/server/certificate"
	"github.com/lxc/incus/v7/shared/api"
)

// allowDenyAuthorizer is a terminal Authorizer that unconditionally allows or
// denies every request-scoped check.
type allowDenyAuthorizer struct {
	commonAuthorizer

	allowed bool
}

// load is a no-op.
func (a *allowDenyAuthorizer) load(ctx context.Context, certificateCache *certificate.Cache, opts Opts) error {
	return nil
}

// CheckPermission allows or denies the request based on the terminal's setting.
func (a *allowDenyAuthorizer) CheckPermission(ctx context.Context, r *http.Request, object Object, entitlement Entitlement) error {
	if a.allowed {
		return nil
	}

	return api.StatusErrorf(http.StatusForbidden, "User does not have entitlement %q on object %q", entitlement, object)
}

// GetPermissionChecker returns a checker that allows or denies every object based on the terminal's setting.
func (a *allowDenyAuthorizer) GetPermissionChecker(ctx context.Context, r *http.Request, entitlement Entitlement, objectType ObjectType) (PermissionChecker, error) {
	allowed := a.allowed

	return func(Object) bool { return allowed }, nil
}

// GetInstanceAccess returns an empty access list.
func (a *allowDenyAuthorizer) GetInstanceAccess(ctx context.Context, projectName string, instanceName string) (*api.Access, error) {
	return &api.Access{}, nil
}

// GetProjectAccess returns an empty access list.
func (a *allowDenyAuthorizer) GetProjectAccess(ctx context.Context, projectName string) (*api.Access, error) {
	return &api.Access{}, nil
}
