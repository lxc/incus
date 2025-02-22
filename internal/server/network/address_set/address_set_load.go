package address_set

import (
	"context"
	"fmt"
	"slices"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/network/acl"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

// LoadByName loads and initializes a Network Address Set from the database by project and name.
func LoadByName(s *state.State, projectName string, name string) (NetworkAddressSet, error) {
	var id int64
	var asInfo *api.NetworkAddressSet

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		id, asInfo, err = tx.GetNetworkAddressSet(ctx, projectName, name)
		return err
	})
	if err != nil {
		return nil, err
	}

	var as NetworkAddressSet = &common{} // Only a single driver currently.
	as.init(s, id, projectName, asInfo)

	return as, nil
}

// Create validates supplied record and creates a new Network Address Set record in the database.
func Create(s *state.State, projectName string, asInfo *api.NetworkAddressSetsPost) error {
	var addrSet NetworkAddressSet = &common{} // Only a single driver currently.
	addrSet.init(s, -1, projectName, nil)

	err := addrSet.validateName(asInfo.Name)
	if err != nil {
		return err
	}

	err = addrSet.validateConfig(&asInfo.NetworkAddressSetPut)
	if err != nil {
		return err
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Insert DB record.
		_, err := tx.CreateNetworkAddressSet(ctx, projectName, asInfo)
		return err
	})
	if err != nil {
		return err
	}

	return nil
}

// Exists checks the Address Set name(s) provided exist in the project.
// If multiple names are provided, also checks that duplicate names aren't specified in the list.
func Exists(s *state.State, projectName string, name ...string) error {
	var existingSetNames []string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		existingSetNames, err = tx.GetNetworkAddressSets(ctx, projectName)
		return err
	})
	if err != nil {
		return err
	}

	checkedSetNames := make(map[string]struct{}, len(name))

	for _, setName := range name {
		if !slices.Contains(existingSetNames, setName) {
			return fmt.Errorf("Network address set %q does not exist", setName)
		}

		_, found := checkedSetNames[setName]
		if found {
			return fmt.Errorf("Network address set %q specified multiple times", setName)
		}

		checkedSetNames[setName] = struct{}{}
	}

	return nil
}

// AddressSetUsedBy calls usageFunc for each ACL that references the specified address set name.
func AddressSetUsedBy(s *state.State, projectName string, usageFunc func(aclName string) error, addressSetName string) error {
	var aclNames []string
	var err error

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		aclNames, err = tx.GetNetworkACLs(ctx, projectName)
		return err
	})
	if err != nil {
		return err
	}

	// Load each ACL and check rules.
	for _, aclName := range aclNames {
		var aclInfo *api.NetworkACL
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			_, aclInfo, err = tx.GetNetworkACL(ctx, projectName, aclName)
			return err
		})
		if err != nil {
			return fmt.Errorf("Failed loading ACL %q: %w", aclName, err)
		}

		// Check all rules for reference to @addressSetName.
		refFound := false

		checkRule := func(rule api.NetworkACLRule) bool {
			// We assume address sets are referenced as "$addressSetName".

			if rule.Source != "" && subjectListReferences(rule.Source, addressSetName) {
				return true
			}

			if rule.Destination != "" && subjectListReferences(rule.Destination, addressSetName) {
				return true
			}

			return false
		}

		for _, ingress := range aclInfo.Ingress {
			if checkRule(ingress) {
				refFound = true
				break
			}
		}

		if !refFound {
			for _, egress := range aclInfo.Egress {
				if checkRule(egress) {
					refFound = true
					break
				}
			}
		}

		if refFound {
			err = usageFunc(aclName)
			if err != nil {
				if err == db.ErrInstanceListStop {
					return err
				}
				return fmt.Errorf("Usage callback failed: %w", err)
			}
		}
	}

	return nil
}

// subjectListReferences checks if the subject list (comma separated) references @addressSetName
// We split by comma and trim space, check if any match "@addressSetName".
func subjectListReferences(subjects string, addressSetName string) bool {
	parts := util.SplitNTrimSpace(subjects, ",", -1, false)
	needle := "$" + addressSetName
	for _, p := range parts {
		if p == needle {
			return true
		}
	}
	return false
}

// AddressSetUsage holds info about a network using the address set.
type AddressSetUsage struct {
	ID        int64
	Name      string
	Type      string
	Addresses []string
	Config    map[string]string
}

func AddressSetNetworkUsage(s *state.State, projectName string, addressSetName string, addresses []string, asNets map[string]AddressSetUsage) error {
	// 1. Get ACLs referencing this address set.
	aclNames := []string{}
	err := AddressSetUsedBy(s, projectName, func(aclName string) error {
		aclNames = append(aclNames, aclName)
		return nil
	}, addressSetName)
	if err != nil {
		return err
	}

	// Now get network usage from those ACLs. Reuse ACL's NetworkUsage function if it can handle partial usage.
	aclNets := map[string]acl.NetworkACLUsage{}
	err = acl.NetworkUsage(s, projectName, aclNames, aclNets)
	if err != nil {
		return err
	}

	// Convert ACLNetworkUsage entries into AddressSetUsage entries.
	for netName, netUsage := range aclNets {
		asNets[netName] = AddressSetUsage{
			ID:        netUsage.ID,
			Name:      netUsage.Name,
			Type:      netUsage.Type,
			Addresses: addresses,
			Config:    netUsage.Config,
		}
	}

	return nil
}
