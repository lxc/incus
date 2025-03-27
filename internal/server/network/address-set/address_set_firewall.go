package addressset

import (
	"context"
	"fmt"

	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	firewallDrivers "github.com/lxc/incus/v6/internal/server/firewall/drivers"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
)

// FirewallApplyAddressSets applies address set rules to the network firewall.
func FirewallApplyAddressSets(s *state.State, projectName string, addressSet AddressSetUsage) error {
	sets, err := FirewallAddressSets(s, projectName)
	if err != nil {
		return err
	}

	// Here assume nftTable is inet because we are applying directly to firewall
	err = s.Firewall.NetworkApplyAddressSets(sets, "inet")
	if err != nil {
		return err
	}

	return nil
}

// FirewallApplyAddressSetsForACLRules apply address-sets from ACLNames to the correct nft Table.
func FirewallApplyAddressSetsForACLRules(s *state.State, nftTable string, projectName string, ACLNames []string) error {
	// Build address set usage from network ACLs.
	var apiSets []*api.NetworkAddressSet
	var fwSets []firewallDrivers.AddressSet
	setsNames, err := GetAddressSetsForACLs(s, projectName, ACLNames)
	if err != nil {
		return err
	}
	// convertAddressSets convert the address set to a Firewall named set.
	convertAddressSets := func(apiSets []*api.NetworkAddressSet) error {
		for _, set := range apiSets {
			firewallAddressSet := firewallDrivers.AddressSet{
				Name:      set.Name,
				Addresses: set.Addresses,
			}

			fwSets = append(fwSets, firewallAddressSet)
		}

		return nil
	}

	for _, setName := range setsNames {
		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			dbSet, err := dbCluster.GetNetworkAddressSet(ctx, tx.Tx(), projectName, setName)
			if err != nil {
				return err
			}

			set, err := dbSet.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			apiSets = append(apiSets, set)
			return nil
		})
		if err != nil {
			return fmt.Errorf("Failed loading address set %q: %w", setName, err)
		}
	}

	err = convertAddressSets(apiSets)
	if err != nil {
		return err
	}

	return s.Firewall.NetworkApplyAddressSets(fwSets, nftTable)
}

// FirewallAddressSets returns address sets for a network firewall.
func FirewallAddressSets(s *state.State, addrSetProjectName string) ([]firewallDrivers.AddressSet, error) {
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

	// Here we want to load every address set for a given project.
	var sets []*api.NetworkAddressSet

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		dbSets, err := dbCluster.GetNetworkAddressSets(ctx, tx.Tx(), dbCluster.NetworkAddressSetFilter{Project: &addrSetProjectName})
		if err != nil {
			return err
		}

		for _, dbSet := range dbSets {
			set, err := dbSet.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			sets = append(sets, set)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Failed loading address set names for network firewall: %w", err)
	}

	err = convertAddressSets(sets)
	if err != nil {
		return nil, fmt.Errorf("Failed converting address sets for network firewall: %w", err)
	}

	return addressSets, nil
}
