package acl

import (
	"github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
)

// NetworkACL represents a Network ACL.
type NetworkACL interface {
	// Initialize.
	init(state *state.State, id int64, projectName string, aclInfo *api.NetworkACL)

	// Info.
	ID() int64
	Project() string
	Info() *api.NetworkACL
	Etag() []any
	UsedBy() ([]string, error)

	// GetLog.
	GetLog(clientType request.ClientType) (string, error)

	// Internal validation.
	validateName(name string) error
	validateConfig(config *api.NetworkACLPut) error

	// Modifications.
	Update(config *api.NetworkACLPut, clientType request.ClientType) error
	Rename(newName string) error
	Delete() error
}
