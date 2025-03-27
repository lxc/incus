package addressset

import (
	"context"
	"fmt"
	"slices"

	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/network/acl"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

// LoadByName loads and initializes a network address set from the database by project and name.
func LoadByName(s *state.State, projectName string, name string) (NetworkAddressSet, error) {
	var id int
	var asInfo *api.NetworkAddressSet

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbSet, err := dbCluster.GetNetworkAddressSet(ctx, tx.Tx(), projectName, name)
		if err != nil {
			return err
		}

		asInfo, err = dbSet.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		id = dbSet.ID

		return nil
	})
	if err != nil {
		return nil, err
	}

	var as NetworkAddressSet = &common{}
	as.init(s, id, projectName, asInfo)

	return as, nil
}

// Create validates supplied record and creates a new network address set record in the database.
func Create(s *state.State, projectName string, asInfo *api.NetworkAddressSetsPost) error {
	var addrSet NetworkAddressSet = &common{}
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
		id, err := dbCluster.CreateNetworkAddressSet(ctx, tx.Tx(), dbCluster.NetworkAddressSet{
			Name:        asInfo.Name,
			Description: asInfo.Description,
			Addresses:   asInfo.Addresses,
			Project:     projectName,
		})
		if err != nil {
			return err
		}

		err = dbCluster.CreateNetworkAddressSetConfig(ctx, tx.Tx(), id, asInfo.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// Exists checks the address set name(s) provided exist in the project.
// If multiple names are provided, also checks that duplicate names aren't specified in the list.
func Exists(s *state.State, projectName string, name ...string) error {
	var existingSetNames []string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		sets, err := dbCluster.GetNetworkAddressSets(ctx, tx.Tx(), dbCluster.NetworkAddressSetFilter{Project: &projectName})
		if err != nil {
			return err
		}

		for _, set := range sets {
			existingSetNames = append(existingSetNames, set.Name)
		}

		return nil
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

		// Check all rules for reference to $addressSetName.
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

// subjectListReferences checks if the subject list (comma separated) references $addressSetName.
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
	ID         int
	Name       string
	Type       string
	DeviceName string
	Addresses  []string
	Config     map[string]string
	ACLNames   []string
}

// AddressSetNetworkUsage retrieve the networks that use an address set by checking ACLs.
func AddressSetNetworkUsage(s *state.State, projectName string, addressSetName string, addresses []string, asNets map[string]AddressSetUsage) error {
	// Get ACLs referencing this address set.
	aclNames := []string{}
	err := AddressSetUsedBy(s, projectName, func(aclName string) error {
		aclNames = append(aclNames, aclName)
		return nil
	}, addressSetName)
	if err != nil {
		return err
	}

	// Now get network usage from those ACLs. Reuse ACL's NetworkUsage function.
	aclNets := map[string]acl.NetworkACLUsage{}
	err = acl.NetworkUsage(s, projectName, aclNames, aclNets)
	if err != nil {
		return err
	}

	// Convert ACLNetworkUsage entries into AddressSetUsage entries.
	for netName, netUsage := range aclNets {
		asNets[netName] = AddressSetUsage{
			ID:         int(netUsage.ID),
			Name:       netUsage.Name,
			Type:       netUsage.Type,
			DeviceName: netUsage.DeviceName,
			Addresses:  addresses,
			Config:     netUsage.Config,
			ACLNames:   aclNames,
		}
	}

	return nil
}

// GetAddressSetsForACLs return the set of address sets used by given ACLs.
func GetAddressSetsForACLs(s *state.State, projectName string, ACLNames []string) ([]string, error) {
	var projectSetsNames []string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		sets, err := dbCluster.GetNetworkAddressSets(ctx, tx.Tx(), dbCluster.NetworkAddressSetFilter{Project: &projectName})
		if err != nil {
			return err
		}

		for _, set := range sets {
			projectSetsNames = append(projectSetsNames, set.Name)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Failed loading address set names for project %q: %w", projectName, err)
	}

	// For every address set in project check if used by acls given via ACLNames.
	// If so store it in setsNames slice.
	var setsNames []string
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
