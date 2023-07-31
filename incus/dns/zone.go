package dns

import (
	"github.com/canonical/lxd/shared/api"
)

// Zone represents a DNS zone configuration and its content.
type Zone struct {
	Info    api.NetworkZone
	Content string
}
