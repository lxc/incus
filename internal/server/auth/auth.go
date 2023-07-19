package auth

import (
	"net/http"

	"github.com/lxc/incus/internal/server/request"
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

// UserHasPermission checks whether the requestor has access to a project.
func UserHasPermission(r *http.Request, project string) bool {
	val := r.Context().Value(request.CtxAccess)
	if val == nil {
		return false
	}

	ua := val.(*UserAccess)
	if ua.Admin {
		return true
	}

	_, ok := ua.Projects[project]
	return ok
}
