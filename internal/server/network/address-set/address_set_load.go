package addressset

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/response"
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
		acls, err := dbCluster.GetNetworkACLs(ctx, tx.Tx(), dbCluster.NetworkACLFilter{Project: &projectName})
		if err != nil {
			return err
		}

		aclNames = make([]string, len(acls))
		for i, acl := range acls {
			aclNames[i] = acl.Name
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Load each ACL and check rules.
	for _, aclName := range aclNames {
		var aclInfo *api.NetworkACL
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			_, aclInfo, err = dbCluster.GetNetworkACLAPI(ctx, tx.Tx(), projectName, aclName)
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

		if slices.ContainsFunc(aclInfo.Ingress, checkRule) {
			refFound = true
		}

		if !refFound {
			if slices.ContainsFunc(aclInfo.Egress, checkRule) {
				refFound = true
			}
		}

		if refFound {
			err = usageFunc(aclName)
			if err != nil {
				if errors.Is(err, db.ErrInstanceListStop) {
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
	return slices.Contains(parts, needle)
}

// NetworkACLUsage info about a network and what ACL it uses.
type NetworkACLUsage struct {
	ID           int64
	Name         string
	Type         string
	Config       map[string]string
	InstanceName string
	DeviceName   string
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
	aclNets := map[string]NetworkACLUsage{}
	err = ACLNetworkUsage(s, projectName, aclNames, aclNets)
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

// Redefine ACL usage funcs because we run into circular import otherwise

// ACLisInUseByDevice returns any of the supplied matching ACL names found referenced by the NIC device.
func ACLisInUseByDevice(d deviceConfig.Device, matchACLNames ...string) []string {
	matchedACLNames := []string{}

	// Only NICs linked to managed networks can use network ACLs.
	if d["type"] != "nic" || d["network"] == "" {
		return matchedACLNames
	}

	for _, nicACLName := range util.SplitNTrimSpace(d["security.acls"], ",", -1, true) {
		if slices.Contains(matchACLNames, nicACLName) {
			matchedACLNames = append(matchedACLNames, nicACLName)
		}
	}

	return matchedACLNames
}

// ACLUsedBy finds all networks, profiles and instance NICs that use any of the specified ACLs and executes usageFunc
// once for each resource using one or more of the ACLs with info about the resource and matched ACLs being used.
func ACLUsedBy(s *state.State, aclProjectName string, usageFunc func(ctx context.Context, tx *db.ClusterTx, matchedACLNames []string, usageType any, nicName string, nicConfig map[string]string) error, matchACLNames ...string) error {
	if len(matchACLNames) <= 0 {
		return nil
	}

	var profiles []dbCluster.Profile
	profileDevices := map[string]map[string]dbCluster.Device{}

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Find networks using the ACLs. Cheapest to do.
		networkNames, err := tx.GetCreatedNetworkNamesByProject(ctx, aclProjectName)
		if err != nil && !response.IsNotFoundError(err) {
			return fmt.Errorf("Failed loading networks for project %q: %w", aclProjectName, err)
		}

		for _, networkName := range networkNames {
			_, network, _, err := tx.GetNetworkInAnyState(ctx, aclProjectName, networkName)
			if err != nil {
				return fmt.Errorf("Failed to get network config for %q: %w", networkName, err)
			}

			netACLNames := util.SplitNTrimSpace(network.Config["security.acls"], ",", -1, true)
			matchedACLNames := []string{}
			for _, netACLName := range netACLNames {
				if slices.Contains(matchACLNames, netACLName) {
					matchedACLNames = append(matchedACLNames, netACLName)
				}
			}

			if len(matchedACLNames) > 0 {
				// Call usageFunc with a list of matched ACLs and info about the network.
				err := usageFunc(ctx, tx, matchedACLNames, network, "", nil)
				if err != nil {
					return err
				}
			}
		}

		// Look for profiles. Next cheapest to do.
		profiles, err = dbCluster.GetProfiles(ctx, tx.Tx())
		if err != nil {
			return err
		}

		// Get all the profile devices.
		profileDevicesByID, err := dbCluster.GetAllProfileDevices(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, profile := range profiles {
			devices := map[string]dbCluster.Device{}
			for _, dev := range profileDevicesByID[profile.ID] {
				devices[dev.Name] = dev
			}

			profileDevices[profile.Name] = devices
		}

		return nil
	})
	if err != nil {
		return err
	}

	for _, profile := range profiles {
		// Get the profiles's effective network project name.
		profileNetworkProjectName, _, err := project.NetworkProject(s.DB.Cluster, profile.Project)
		if err != nil {
			return err
		}

		// Skip profiles who's effective network project doesn't match this Network ACL's project.
		if profileNetworkProjectName != aclProjectName {
			continue
		}

		// Iterate through each of the instance's devices, looking for NICs that are using any of the ACLs.
		for devName, devConfig := range deviceConfig.NewDevices(dbCluster.DevicesToAPI(profileDevices[profile.Name])) {
			matchedACLNames := ACLisInUseByDevice(devConfig, matchACLNames...)
			if len(matchedACLNames) > 0 {
				// Call usageFunc with a list of matched ACLs and info about the instance NIC.
				err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
					return usageFunc(ctx, tx, matchedACLNames, profile, devName, devConfig)
				})
				if err != nil {
					return err
				}
			}
		}
	}

	var aclNames []string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		acls, err := dbCluster.GetNetworkACLs(ctx, tx.Tx(), dbCluster.NetworkACLFilter{Project: &aclProjectName})
		if err != nil {
			return err
		}

		aclNames = make([]string, len(acls))
		for i, acl := range acls {
			aclNames[i] = acl.Name
		}

		return nil
	})
	if err != nil {
		return err
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		for _, aclName := range aclNames {
			_, aclInfo, err := dbCluster.GetNetworkACLAPI(ctx, tx.Tx(), aclProjectName, aclName)
			if err != nil {
				return err
			}

			matchedACLNames := []string{}

			// Ingress rules can specify ACL names in their Source subjects.
			for _, rule := range aclInfo.Ingress {
				for _, subject := range util.SplitNTrimSpace(rule.Source, ",", -1, true) {
					// Look for new matching ACLs, but ignore our own ACL reference in our own rules.
					if slices.Contains(matchACLNames, subject) && !slices.Contains(matchedACLNames, subject) && subject != aclInfo.Name {
						matchedACLNames = append(matchedACLNames, subject)
					}
				}
			}

			// Egress rules can specify ACL names in their Destination subjects.
			for _, rule := range aclInfo.Egress {
				for _, subject := range util.SplitNTrimSpace(rule.Destination, ",", -1, true) {
					// Look for new matching ACLs, but ignore our own ACL reference in our own rules.
					if slices.Contains(matchACLNames, subject) && !slices.Contains(matchedACLNames, subject) && subject != aclInfo.Name {
						matchedACLNames = append(matchedACLNames, subject)
					}
				}
			}

			if len(matchedACLNames) > 0 {
				// Call usageFunc with a list of matched ACLs and info about the ACL.
				err = usageFunc(ctx, tx, matchedACLNames, aclInfo, "", nil)
				if err != nil {
					return err
				}
			}
		}

		// Find instances using the ACLs. Most expensive to do.
		err = tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
			// Get the instance's effective network project name.
			instNetworkProject := project.NetworkProjectFromRecord(&p)

			// Skip instances who's effective network project doesn't match this Network ACL's project.
			if instNetworkProject != aclProjectName {
				return nil
			}

			devices := db.ExpandInstanceDevices(inst.Devices.Clone(), inst.Profiles)

			// Iterate through each of the instance's devices, looking for NICs that are using any of the ACLs.
			for devName, devConfig := range devices {
				matchedACLNames := ACLisInUseByDevice(devConfig, matchACLNames...)
				if len(matchedACLNames) > 0 {
					// Call usageFunc with a list of matched ACLs and info about the instance NIC.
					err := usageFunc(ctx, tx, matchedACLNames, inst, devName, devConfig)
					if err != nil {
						return err
					}
				}
			}

			return nil
		})
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

// ACLNetworkUsage populates the provided aclNets map with networks that are using any of the specified ACLs.
func ACLNetworkUsage(s *state.State, aclProjectName string, aclNames []string, aclNets map[string]NetworkACLUsage) error {
	supportedNetTypes := []string{"bridge", "ovn"}

	// Find all networks and instance/profile NICs that use any of the specified Network ACLs.
	err := ACLUsedBy(s, aclProjectName, func(ctx context.Context, tx *db.ClusterTx, matchedACLNames []string, usageType any, devName string, nicConfig map[string]string) error {
		switch u := usageType.(type) {
		case dbCluster.Profile:
			networkID, network, _, err := tx.GetNetworkInAnyState(ctx, aclProjectName, nicConfig["network"])
			if err != nil {
				return fmt.Errorf("Failed to load network %q: %w", nicConfig["network"], err)
			}

			if slices.Contains(supportedNetTypes, network.Type) {
				_, found := aclNets[network.Name]
				if !found {
					aclNets[network.Name] = NetworkACLUsage{
						ID:     networkID,
						Name:   network.Name,
						Type:   network.Type,
						Config: network.Config,
					}
				}
			}

		case db.InstanceArgs:
			networkID, network, _, err := tx.GetNetworkInAnyState(ctx, aclProjectName, nicConfig["network"])
			if err != nil {
				return fmt.Errorf("Failed to load network %q: %w", nicConfig["network"], err)
			}

			if slices.Contains(supportedNetTypes, network.Type) {
				if network.Type == "bridge" && devName != "" {
					// Use different key for the usage by bridge NICs to avoid overwriting the usage by the bridge network itself.
					key := fmt.Sprintf("%s/%s/%s", network.Name, u.Name, devName)

					_, found := aclNets[key]

					if !found {
						aclNets[key] = NetworkACLUsage{
							ID:           networkID,
							Name:         network.Name,
							Type:         network.Type,
							Config:       network.Config,
							InstanceName: u.Name,
							DeviceName:   devName,
						}
					}
				} else {
					_, found := aclNets[network.Name]

					if !found {
						aclNets[network.Name] = NetworkACLUsage{
							ID:     networkID,
							Name:   network.Name,
							Type:   network.Type,
							Config: network.Config,
						}
					}
				}
			}

		case *api.Network:
			if slices.Contains(supportedNetTypes, u.Type) {
				_, found := aclNets[u.Name]
				if !found {
					networkID, network, _, err := tx.GetNetworkInAnyState(ctx, aclProjectName, u.Name)
					if err != nil {
						return fmt.Errorf("Failed to load network %q: %w", u.Name, err)
					}

					aclNets[u.Name] = NetworkACLUsage{
						ID:     networkID,
						Name:   network.Name,
						Type:   network.Type,
						Config: network.Config,
					}
				}
			}

		case *api.NetworkACL:
			return nil // Nothing to do for ACL rules referencing us.
		default:
			return fmt.Errorf("Unrecognised usage type %T", u)
		}

		return nil
	}, aclNames...)
	if err != nil {
		return err
	}

	return nil
}
