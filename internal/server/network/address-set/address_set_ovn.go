package addressset

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/network/ovn"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
)

// OVNEnsureAddressSetsViaACLs ensure that every address set referenced by given acls are created in OVN NB DB.
func OVNEnsureAddressSetsViaACLs(s *state.State, l logger.Logger, client *ovn.NB, projectName string, ACLNames []string) (revert.Hook, error) {
	// Build address set usage from network ACLs
	setsNames, err := GetAddressSetsForACLs(s, projectName, ACLNames)
	if err != nil {
		return nil, err
	}
	// Then call OVNEnsureAddressSet
	return OVNEnsureAddressSets(s, l, client, projectName, setsNames)
}

// OVNDeleteAddressSetsViaACLs remove address sets used by network ACLS.
func OVNDeleteAddressSetsViaACLs(s *state.State, l logger.Logger, client *ovn.NB, projectName string, ACLNames []string) error {
	setsNames, err := GetAddressSetsForACLs(s, projectName, ACLNames)
	if err != nil {
		return err
	}

	for _, setName := range setsNames {
		err = client.DeleteAddressSet(context.TODO(), ovn.OVNAddressSet(setName))
		if err != nil {
			return fmt.Errorf("Failed removing address set %q from OVN: %w", setName, err)
		}

		l.Debug("Removed unused address set from OVN", logger.Ctx{"project": projectName, "addressSet": setName})
	}

	return nil
}

// OVNEnsureAddressSets ensures that the address sets and their addresses are created in OVN NB DB.
// Returns a revert function to undo changes if needed.
func OVNEnsureAddressSets(s *state.State, l logger.Logger, client *ovn.NB, projectName string, addressSetNames []string) (revert.Hook, error) {
	revertion := revert.New()
	defer revertion.Fail()

	for _, addressSetName := range addressSetNames {
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
				if ip == nil {
					// If MAC, skip IP sets. OVN address sets currently store MAC addresses but we don't use them.
					// If needed, you'd have separate sets for MAC.
					_, errMac := net.ParseMAC(addr)
					if errMac == nil {
						// We currently ignore MAC addresses. OVN address sets are primarily for IP addresses.
						// If future support for MAC sets needed, handle here.
						continue
					}

					return nil, fmt.Errorf("Unsupported address format: %q", addr)
				}

				bits := 32
				if ip.To4() == nil {
					bits = 128
				}

				mask := net.CIDRMask(bits, bits)
				ipNets = append(ipNets, net.IPNet{IP: ip, Mask: mask})
			}
		}

		// Check if the address set exists in OVN.
		existingIPv4Set, existingIPv6Set, err := client.GetAddressSet(context.TODO(), ovn.OVNAddressSet(asInfo.Name))

		// If address sets do not exist, create them.
		if errors.Is(err, ovn.ErrNotFound) {
			err = client.CreateAddressSet(context.TODO(), ovn.OVNAddressSet(asInfo.Name), ipNets...)
			ipNetStrings := make([]string, len(ipNets))
			if err != nil {
				for i, ipNet := range ipNets {
					ipNetStrings[i] = ipNet.String()
				}

				return nil, fmt.Errorf("Failed creating address set %q with networks %s in OVN: %w", asInfo.Name, strings.Join(ipNetStrings, "-"), err)
			}

			revertion.Add(func() { _ = client.DeleteAddressSet(context.TODO(), ovn.OVNAddressSet(asInfo.Name)) })
		} else {
			if err != nil && !errors.Is(err, ovn.ErrNotFound) {
				return nil, fmt.Errorf("Failed fetching address set %q (IPv4) from OVN: %w", asInfo.Name, err)
			}

			if err != nil && !errors.Is(err, ovn.ErrNotFound) {
				return nil, fmt.Errorf("Failed fetching address set %q (IPv6) from OVN: %w", asInfo.Name, err)
			}

			// Compute differences.
			existingIPv4Map := make(map[string]bool)
			existingIPv6Map := make(map[string]bool)
			for _, addr := range existingIPv4Set.Addresses {
				existingIPv4Map[addr] = true
			}

			for _, addr := range existingIPv6Set.Addresses {
				existingIPv6Map[addr] = true
			}

			var addIPv4, removeIPv4, addIPv6, removeIPv6 []net.IPNet

			for _, newIP := range ipNets {
				if newIP.IP.To4() != nil {
					if !existingIPv4Map[newIP.String()] {
						addIPv4 = append(addIPv4, newIP)
					}
				} else {
					if !existingIPv6Map[newIP.String()] {
						addIPv6 = append(addIPv6, newIP)
					}
				}
			}

			for existingIP := range existingIPv4Map {
				found := false
				for _, newIP := range ipNets {
					if newIP.String() == existingIP {
						found = true
						break
					}
				}

				if !found {
					// OVN always register CIDR in address set.
					_, network, err := net.ParseCIDR(existingIP)
					if err != nil {
						return nil, fmt.Errorf("Failed parsing existing IP in set %s err: %w", existingIP, err)
					}

					removeIPv4 = append(removeIPv4, *network)
				}
			}

			for existingIP := range existingIPv6Map {
				found := false
				for _, newIP := range ipNets {
					if newIP.String() == existingIP {
						found = true
						break
					}
				}

				if !found {
					_, network, err := net.ParseCIDR(existingIP)
					if err != nil {
						return nil, fmt.Errorf("Failed parsing existing IP in set %s err: %w", existingIP, err)
					}

					removeIPv6 = append(removeIPv6, *network)
				}
			}

			// Update OVN sets.
			if len(addIPv4) > 0 || len(addIPv6) > 0 {
				err = client.UpdateAddressSetAdd(context.TODO(), ovn.OVNAddressSet(asInfo.Name), append(addIPv4, addIPv6...)...)
				if err != nil {
					return nil, fmt.Errorf("Failed adding addresses to address set %q in OVN: %w", asInfo.Name, err)
				}
			}

			if len(removeIPv4) > 0 || len(removeIPv6) > 0 {
				err = client.UpdateAddressSetRemove(context.TODO(), ovn.OVNAddressSet(asInfo.Name), append(removeIPv4, removeIPv6...)...)
				if err != nil {
					return nil, fmt.Errorf("Failed removing addresses from address set %q in OVN: %w", asInfo.Name, err)
				}
			}
		}
	}

	cleanup := revertion.Clone().Fail
	revertion.Success()
	return cleanup, nil
}

// OVNAddressSetDeleteIfUnused checks if the specified address set is unused and if so, removes it from OVN.
func OVNAddressSetDeleteIfUnused(s *state.State, l logger.Logger, client *ovn.NB, projectName string, setName string) error {
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

// GetAddressSetsForACLs return the set of address sets used by given ACLs.
func GetAddressSetsForACLs(s *state.State, projectName string, ACLNames []string) ([]string, error) {
	var projectSetsNames []string
	var setsNames []string
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		projectSetsNames, err = tx.GetNetworkAddressSets(ctx, projectName)

		return err
	})

	if err != nil {
		return nil, fmt.Errorf("Failed loading address set names for project %q: %w", projectName, err)
	}

	// For every address set in project check if used by acls given via ACLNames.
	// If so store it in setsNames slice.
	for _, setName := range projectSetsNames {
		err = AddressSetUsedBy(s, projectName, func(aclName string) error {
			if slices.Contains(ACLNames, aclName) {
				setsNames = append(setsNames, setName)
			}

			return nil
		}, setName)

		if err != nil {
			return nil, fmt.Errorf("Failed to fetch address set %s use", setName)
		}
	}

	return setsNames, nil
}
