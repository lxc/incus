package addressset

import (
	"context"
	"fmt"

	"github.com/lxc/incus/v6/internal/server/db"
	firewallDrivers "github.com/lxc/incus/v6/internal/server/firewall/drivers"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
)

// FirewallApplyAddressSetRules applies Address Set rules to the network firewall.
func FirewallApplyAddressSetRules(s *state.State, projectName string, addressSet AddressSetUsage) error {
	sets, err := FirewallAddressSets(s, addressSet.Name, projectName)
	if err != nil {
		return err
	}

	return s.Firewall.NetworkApplyAddressSets(addressSet.Name, sets)
}

// FirewallAddressSets returns address sets for a network firewall.
func FirewallAddressSets(s *state.State, addrSetDeviceName string, addrSetProjectName string) ([]firewallDrivers.AddressSet, error) {
	var addressSets []firewallDrivers.AddressSet
	// convertAddressSets convert the address set to a Firewall named set.
	convertAddressSets := func(sets []*api.NetworkAddressSet) error {
		for _, set := range sets {
			firewallAddressSet := firewallDrivers.AddressSet{
				Name:      set.Name,
				Addresses: set.Addresses,
			}

			addressSets = append(addressSets, firewallAddressSet)
		}

		return nil
	}

	// Here we want to load every address set for a given project
	var setsNames []string
	var sets []*api.NetworkAddressSet

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		setsNames, err = tx.GetNetworkAddressSets(ctx, addrSetProjectName)

		return err
	})

	if err != nil {
		return nil, fmt.Errorf("Failed loading address set names for network %q: %w", addrSetDeviceName, err)
	}

	for _, setName := range setsNames {
		var set *api.NetworkAddressSet

		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			_, set, err = tx.GetNetworkAddressSet(ctx, addrSetProjectName, setName)

			return err
		})

		if err != nil {
			return nil, fmt.Errorf("Failed loading address set %q for network %q: %w", setName, addrSetDeviceName, err)
		}

		sets = append(sets, set)
	}

	err = convertAddressSets(sets)

	if err != nil {
		return nil, fmt.Errorf("Failed converting address sets for network %q: %w", addrSetDeviceName, err)
	}

	return addressSets, nil
}
