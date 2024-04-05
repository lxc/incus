package zone

import (
	"strings"

	"github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
)

// NetworkZone represents a Network zone.
type NetworkZone interface {
	// Initialize.
	init(state *state.State, id int64, projectName string, zoneInfo *api.NetworkZone)

	// Info.
	ID() int64
	Project() string
	Info() *api.NetworkZone
	Etag() []any
	UsedBy() ([]string, error)
	Content() (*strings.Builder, error)
	SOA() (*strings.Builder, error)

	// Records.
	AddRecord(req api.NetworkZoneRecordsPost) error
	GetRecords() ([]api.NetworkZoneRecord, error)
	GetRecord(name string) (*api.NetworkZoneRecord, error)
	UpdateRecord(name string, req api.NetworkZoneRecordPut, clientType request.ClientType) error
	DeleteRecord(name string) error

	// Internal validation.
	validateName(name string) error
	validateConfig(config *api.NetworkZonePut) error

	// Modifications.
	Update(config *api.NetworkZonePut, clientType request.ClientType) error
	Delete() error
}
