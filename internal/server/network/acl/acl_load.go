package acl

import (
	"context"
	"fmt"

	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/util"
)

// LoadByName loads and initialises a Network ACL from the database by project and name.
func LoadByName(s *state.State, projectName string, name string) (NetworkACL, error) {
	var id int64
	var aclInfo *api.NetworkACL

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		id, aclInfo, err = tx.GetNetworkACL(ctx, projectName, name)

		return err
	})
	if err != nil {
		return nil, err
	}

	var acl NetworkACL = &common{} // Only a single driver currently.
	acl.init(s, id, projectName, aclInfo)

	return acl, nil
}

// Create validates supplied record and creates new Network ACL record in the database.
func Create(s *state.State, projectName string, aclInfo *api.NetworkACLsPost) error {
	var acl NetworkACL = &common{} // Only a single driver currently.
	acl.init(s, -1, projectName, nil)

	err := acl.validateName(aclInfo.Name)
	if err != nil {
		return err
	}

	err = acl.validateConfig(&aclInfo.NetworkACLPut)
	if err != nil {
		return err
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Insert DB record.
		_, err := tx.CreateNetworkACL(ctx, projectName, aclInfo)

		return err
	})
	if err != nil {
		return err
	}

	return nil
}

// Exists checks the ACL name(s) provided exists in the project.
// If multiple names are provided, also checks that duplicate names aren't specified in the list.
func Exists(s *state.State, projectName string, name ...string) error {
	var existingACLNames []string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		existingACLNames, err = tx.GetNetworkACLs(ctx, projectName)

		return err
	})
	if err != nil {
		return err
	}

	checkedACLNames := make(map[string]struct{}, len(name))

	for _, aclName := range name {
		if !util.ValueInSlice(aclName, existingACLNames) {
			return fmt.Errorf("Network ACL %q does not exist", aclName)
		}

		_, found := checkedACLNames[aclName]
		if found {
			return fmt.Errorf("Network ACL %q specified multiple times", aclName)
		}

		checkedACLNames[aclName] = struct{}{}
	}

	return nil
}

// UsedBy finds all networks, profiles and instance NICs that use any of the specified ACLs and executes usageFunc
// once for each resource using one or more of the ACLs with info about the resource and matched ACLs being used.
func UsedBy(s *state.State, aclProjectName string, usageFunc func(ctx context.Context, tx *db.ClusterTx, matchedACLNames []string, usageType any, nicName string, nicConfig map[string]string) error, matchACLNames ...string) error {
	if len(matchACLNames) <= 0 {
		return nil
	}

	var profiles []cluster.Profile
	profileDevices := map[string]map[string]cluster.Device{}

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
				if util.ValueInSlice(netACLName, matchACLNames) {
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
		profiles, err = cluster.GetProfiles(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, profile := range profiles {
			profileDevices[profile.Name], err = cluster.GetProfileDevices(ctx, tx.Tx(), profile.ID)
			if err != nil {
				return err
			}
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
		for devName, devConfig := range deviceConfig.NewDevices(cluster.DevicesToAPI(profileDevices[profile.Name])) {
			matchedACLNames := isInUseByDevice(devConfig, matchACLNames...)
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
		// Find ACLs that have rules that reference the ACLs.
		aclNames, err = tx.GetNetworkACLs(ctx, aclProjectName)

		return err
	})
	if err != nil {
		return err
	}

	for _, aclName := range aclNames {
		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			_, aclInfo, err := tx.GetNetworkACL(ctx, aclProjectName, aclName)
			if err != nil {
				return err
			}

			matchedACLNames := []string{}

			// Ingress rules can specify ACL names in their Source subjects.
			for _, rule := range aclInfo.Ingress {
				for _, subject := range util.SplitNTrimSpace(rule.Source, ",", -1, true) {
					// Look for new matching ACLs, but ignore our own ACL reference in our own rules.
					if util.ValueInSlice(subject, matchACLNames) && !util.ValueInSlice(subject, matchedACLNames) && subject != aclInfo.Name {
						matchedACLNames = append(matchedACLNames, subject)
					}
				}
			}

			// Egress rules can specify ACL names in their Destination subjects.
			for _, rule := range aclInfo.Egress {
				for _, subject := range util.SplitNTrimSpace(rule.Destination, ",", -1, true) {
					// Look for new matching ACLs, but ignore our own ACL reference in our own rules.
					if util.ValueInSlice(subject, matchACLNames) && !util.ValueInSlice(subject, matchedACLNames) && subject != aclInfo.Name {
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
					matchedACLNames := isInUseByDevice(devConfig, matchACLNames...)
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
	}

	return nil
}

// isInUseByDevice returns any of the supplied matching ACL names found referenced by the NIC device.
func isInUseByDevice(d deviceConfig.Device, matchACLNames ...string) []string {
	matchedACLNames := []string{}

	// Only NICs linked to managed networks can use network ACLs.
	if d["type"] != "nic" || d["network"] == "" {
		return matchedACLNames
	}

	for _, nicACLName := range util.SplitNTrimSpace(d["security.acls"], ",", -1, true) {
		if util.ValueInSlice(nicACLName, matchACLNames) {
			matchedACLNames = append(matchedACLNames, nicACLName)
		}
	}

	return matchedACLNames
}

// NetworkACLUsage info about a network and what ACL it uses.
type NetworkACLUsage struct {
	ID     int64
	Name   string
	Type   string
	Config map[string]string
}

// NetworkUsage populates the provided aclNets map with networks that are using any of the specified ACLs.
func NetworkUsage(s *state.State, aclProjectName string, aclNames []string, aclNets map[string]NetworkACLUsage) error {
	supportedNetTypes := []string{"bridge", "ovn"}

	// Find all networks and instance/profile NICs that use any of the specified Network ACLs.
	err := UsedBy(s, aclProjectName, func(ctx context.Context, tx *db.ClusterTx, matchedACLNames []string, usageType any, _ string, nicConfig map[string]string) error {
		switch u := usageType.(type) {
		case db.InstanceArgs, cluster.Profile:
			networkID, network, _, err := tx.GetNetworkInAnyState(ctx, aclProjectName, nicConfig["network"])
			if err != nil {
				return fmt.Errorf("Failed to load network %q: %w", nicConfig["network"], err)
			}

			if util.ValueInSlice(network.Type, supportedNetTypes) {
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

		case *api.Network:
			if util.ValueInSlice(u.Type, supportedNetTypes) {
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
