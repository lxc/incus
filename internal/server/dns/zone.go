package dns

import (
	"github.com/lxc/incus/v7/shared/api"
)

// Zone represents a DNS zone configuration and its content.
type Zone struct {
	Info    api.NetworkZone
	Content string
}
