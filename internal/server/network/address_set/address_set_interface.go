package address_set

import (
	"github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
)

// NetworkAddressSet represents a Network Address Set.
type NetworkAddressSet interface {
	// Initialize.
	init(state *state.State, id int64, projectName string, info *api.NetworkAddressSet)

	// Info
	ID() int64
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
