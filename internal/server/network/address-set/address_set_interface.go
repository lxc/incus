package addressset

import (
	"github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
)

// NetworkAddressSet represents a network address set.
type NetworkAddressSet interface {
	// Initialize.
	init(s *state.State, id int, projectName string, info *api.NetworkAddressSet)

	// Info
	ID() int
	Project() string
	Info() *api.NetworkAddressSet
	Etag() []any
	UsedBy() ([]string, error)

	// Internal validation.
	validateName(name string) error
	validateConfig(config *api.NetworkAddressSetPut) error

	// Modifications.
	Update(config *api.NetworkAddressSetPut, clientType request.ClientType) error
	Rename(newName string) error
	Delete() error
}
