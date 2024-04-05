package firewall

import (
	"github.com/lxc/incus/v6/internal/server/firewall/drivers"
	"github.com/lxc/incus/v6/shared/logger"
)

// New returns an appropriate firewall implementation.
// Uses xtables if nftables isn't compatible or isn't in use already, otherwise uses nftables.
func New() Firewall {
	nftables := drivers.Nftables{}
	xtables := drivers.Xtables{}

	nftablesInUse, nftablesCompatErr := nftables.Compat()
	if nftablesCompatErr != nil {
		logger.Debugf(`Firewall detected "nftables" incompatibility: %v`, nftablesCompatErr)
	} else if nftablesInUse {
		// If nftables is compatible and already in use, then we prefer to use the nftables driver
		// irrespective of whether xtables is in use or not.
		return nftables
	}

	xtablesInUse, xtablesCompatErr := xtables.Compat()
	if xtablesCompatErr != nil {
		logger.Debugf(`Firewall detected "xtables" incompatibility: %v`, xtablesCompatErr)
	} else if xtablesInUse {
		// If xtables is compatible and already in use, then we prefer to stick with the xtables driver
		// rather than mix the use of firewall drivers on the system.
		return xtables
	}

	// If nftables is compatible, but not in use, and xtables is not compatible or not in use, use nftables.
	if nftablesCompatErr == nil {
		return nftables
	}

	// If neither nftables nor xtables are compatible, we fallback to xtables.
	// This continues the existing behaviour of allowing Incus to start with potentially an incomplete firewall
	// backend, so that only networks and instances using those features may fail to function properly.
	// The most common scenario for this is when xtables is using nft shim commands but the nft command itself
	// is not installed. In this case Incus will use the xtables shim commands but with the potential of problems
	// due to differences between the original xtables commands and the shim commands provided by nft.
	if nftablesCompatErr != nil && xtablesCompatErr != nil {
		logger.Warnf(`Firewall failed to detect any compatible driver, falling back to "xtables" (but some features may not work as expected due to: %v)`, xtablesCompatErr)
		return xtables
	}

	// If xtables is compatible, but not in use, and nftables is not compatible, use xtables.
	return xtables
}
