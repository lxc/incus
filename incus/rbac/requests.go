package rbac

import (
	"net/http"

	"github.com/cyphar/incus/lxd/request"
	"github.com/cyphar/incus/shared"
)

// UserIsAdmin checks whether the requestor is a global admin.
func UserIsAdmin(r *http.Request) bool {
	val := r.Context().Value(request.CtxAccess)
	if val == nil {
		return false
	}

	ua := val.(*UserAccess)
	return ua.Admin
}

// UserHasPermission checks whether the requestor has a specific permission on a project.
func UserHasPermission(r *http.Request, project string, permission string) bool {
	val := r.Context().Value(request.CtxAccess)
	if val == nil {
		return false
	}

	ua := val.(*UserAccess)
	if ua.Admin {
		return true
	}

	return shared.StringInSlice(permission, ua.Projects[project])
}
