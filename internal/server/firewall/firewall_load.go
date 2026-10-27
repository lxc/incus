package firewall

import (
	"github.com/lxc/incus/v7/internal/server/firewall/drivers"
	"github.com/lxc/incus/v7/shared/logger"
)

// New returns the nftables firewall implementation.
func New() Firewall {
	nftables := drivers.Nftables{}

	_, err := nftables.Compat()
	if err != nil {
		logger.Warnf(`Firewall detected "nftables" incompatibility (some features may not work as expected): %v`, err)
	}

	return nftables
}
