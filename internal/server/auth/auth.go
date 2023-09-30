package auth

import (
	"net/http"

	"github.com/lxc/incus/incusd/request"
	"github.com/lxc/incus/shared/util"
)

// UserAccess struct for permission checks.
type UserAccess struct {
	Admin    bool
	Projects []string
}

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

	return util.ValueInSlice(project, ua.Projects)
}
