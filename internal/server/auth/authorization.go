package auth

import (
	"fmt"
	"net/http"

	"github.com/lxc/incus/shared/logger"
)

// ErrUnknownDriver is the "Unknown driver" error.
var ErrUnknownDriver = fmt.Errorf("Unknown driver")

var authorizers = map[string]func() authorizer{}

type authorizer interface {
	Authorizer

	init(name string, config map[string]any, logger logger.Logger, projectsGetFunc func() (map[int64]string, error))
	load() error
}

type Authorizer interface {
	AddProject(projectID int64, name string) error
	DeleteProject(projectID int64) error
	RenameProject(projectID int64, newName string) error

	StopStatusCheck()

	UserAccess(username string) (*UserAccess, error)
	UserIsAdmin(r *http.Request) bool
	UserHasPermission(r *http.Request, projectName string, permission string) bool
}

// UserAccess struct for permission checks.
type UserAccess struct {
	Admin    bool
	Projects map[string][]string
}

func LoadAuthorizer(name string, config map[string]any, logger logger.Logger, projectsGetFunc func() (map[int64]string, error)) (Authorizer, error) {
	driverFunc, ok := authorizers[name]
	if !ok {
		return nil, ErrUnknownDriver
	}

	d := driverFunc()
	d.init(name, config, logger, projectsGetFunc)

	err := d.load()
	if err != nil {
		return nil, err
	}

	return d, nil
}
