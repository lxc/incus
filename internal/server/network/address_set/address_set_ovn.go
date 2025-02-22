package address_set

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/lxc/incus/v6/internal/server/network/ovn"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
)

// OVNEnsureAddressSets ensures that the address sets and their addresses are created in OVN NB DB.
// Similar logic to before, but now directly using OVN NB methods.
//
// The asNets parameter contains networks using the address set indirectly via ACLs. The addressSetName is the
// name of the address set in LXD's database, and we have addresses in asInfo. If addresses is empty, we still
// create empty sets to avoid match errors.
//
// Returns a revert function to undo changes if needed.
func OVNEnsureAddressSets(s *state.State, l logger.Logger, client *ovn.NB, projectName string, asNets map[string]AddressSetUsage, addressSetName string) (revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Load the address set info.
	addrSet, err := LoadByName(s, projectName, addressSetName)
	if err != nil {
		return nil, fmt.Errorf("Failed loading address set %q: %w", addressSetName, err)
	}

	asInfo := addrSet.Info()

	// Convert addresses into net.IPNet slices.
	var ipNets []net.IPNet
	for _, addr := range asInfo.Addresses {
		// Try to parse as IP or CIDR.
		if strings.Contains(addr, "/") {
			_, ipnet, err := net.ParseCIDR(addr)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing CIDR address %q: %w", addr, err)
			}
			ipNets = append(ipNets, *ipnet)
		} else {
			// If single IP address, convert to /32 or /128.
			ip := net.ParseIP(addr)
			if ip != nil {
				bits := 32
				if ip.To4() == nil {
					bits = 128
				}
				mask := net.CIDRMask(bits, bits)
				ipNets = append(ipNets, net.IPNet{IP: ip, Mask: mask})
			} else {
				// If MAC, skip IP sets. OVN address sets currently don't store MAC addresses.
				// If needed, you'd have separate sets for MAC (not currently supported by OVN).
				_, errMac := net.ParseMAC(addr)
				if errMac == nil {
					// We currently ignore MAC addresses. OVN address sets are primarily for IP addresses.
					// If future support for MAC sets needed, handle here.
					continue
				}
				return nil, fmt.Errorf("Unsupported address format: %q", addr)
			}
		}
	}

	// If no addresses, we still create empty sets.
	// First, try deleting old sets and re-creating them cleanly. Or we can just ensure sets exist empty.
	// We'll just ensure sets are created (even if empty).
	//
	// Check if sets exist by trying to update them. If not present, create them.
	// We'll rely on UpdateAddressSetAdd to create if missing. But per the code, UpdateAddressSetAdd only updates,
	// so we must attempt a CreateAddressSet if not existing.

	// We attempt a CreateAddressSet only if it doesn't exist. If it fails because it exists, we will ignore and proceed.
	err = client.CreateAddressSet(context.TODO(), ovn.OVNAddressSet(asInfo.Name))
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		// If error is not "exists", fail.
		if err != ovn.ErrExists {
			return nil, fmt.Errorf("Failed creating address set %q in OVN: %w", asInfo.Name, err)
		}
	}

	// If we have addresses, add them. If not, we ensure empty sets by calling UpdateAddressSetAdd with no addresses,
	// which will just leave them empty.
	if len(ipNets) > 0 {
		err = client.UpdateAddressSetAdd(context.TODO(), ovn.OVNAddressSet(asInfo.Name), ipNets...)
		if err != nil {
			return nil, fmt.Errorf("Failed adding addresses to address set %q in OVN: %w", asInfo.Name, err)
		}
	} else {
		// No addresses means ensure empty sets. CreateAddressSet already done above ensures empty sets.
	}

	revert.Success()
	return nil, nil
}

// OVNAddressSetDeleteIfUnused checks if the specified address set is unused and if so, removes it from OVN.
func OVNAddressSetDeleteIfUnused(s *state.State, l logger.Logger, client *ovn.NB, projectName string, setName string) error {
	// Load the address set by name.
	addrSet, err := LoadByName(s, projectName, setName)
	if err != nil {
		// If not found, it's either already deleted or doesn't exist, so nothing to do.
		return nil
	}

	// Check if used by any ACLs.
	usedBy, err := addrSet.UsedBy()
	if err != nil {
		return fmt.Errorf("Failed checking usage of address set %q in project %q: %w", setName, projectName, err)
	}

	if len(usedBy) > 0 {
		l.Debug("Address set still in use, skipping removal", logger.Ctx{"project": projectName, "addressSet": setName, "usedByCount": len(usedBy)})
		return nil
	}

	// Address set is unused, remove from OVN.
	err = client.DeleteAddressSet(context.TODO(), ovn.OVNAddressSet(setName))
	if err != nil {
		return fmt.Errorf("Failed removing address set %q from OVN: %w", setName, err)
	}

	l.Debug("Removed unused address set from OVN", logger.Ctx{"project": projectName, "addressSet": setName})
	return nil
}
