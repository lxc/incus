package dns

import (
	"github.com/cyphar/incus/shared/api"
)

// Zone represents a DNS zone configuration and its content.
type Zone struct {
	Info    api.NetworkZone
	Content string
}
