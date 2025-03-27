package addressset

import (
	"context"
	"fmt"
	"net"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/state"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
)

// common represents a network address set.
type common struct {
	logger      logger.Logger
	state       *state.State
	id          int
	projectName string
	info        *api.NetworkAddressSet
}

// init initialize internal variables.
func (d *common) init(s *state.State, id int, projectName string, info *api.NetworkAddressSet) {
	if info == nil {
		d.info = &api.NetworkAddressSet{}
	} else {
		d.info = info
	}

	d.logger = logger.AddContext(logger.Ctx{"project": projectName, "networkAddressSet": d.info.Name})
	d.id = id
	d.projectName = projectName
	d.state = s

	if d.info.Addresses == nil {
		d.info.Addresses = []string{}
	}

	if d.info.Config == nil {
		d.info.Config = make(map[string]string)
	}
}

// ID returns the network address set ID.
func (d *common) ID() int {
	return d.id
}

// Project returns the project name.
func (d *common) Project() string {
	return d.projectName
}

// Info returns copy of internal info for the Network AddressSet.
func (d *common) Info() *api.NetworkAddressSet {
	// Copy internal info to prevent modification externally.
	info := api.NetworkAddressSet{}
	info.Name = d.info.Name
	info.Description = d.info.Description
	info.Addresses = append([]string(nil), d.info.Addresses...)
	info.Config = localUtil.CopyConfig(d.info.Config)
	info.UsedBy = nil // To indicate its not populated (use Usedby() function to populate).
	info.Project = d.projectName

	return &info
}

// usedBy returns a list of ACLs API endpoints referencing this address set.
// If firstOnly is true then search stops at first result.
func (d *common) usedBy(firstOnly bool) ([]string, error) {
	usedBy := []string{}

	// Find all ACLs that reference this address set.
	err := AddressSetUsedBy(d.state, d.projectName, func(aclName string) error {
		uri := fmt.Sprintf("/%s/network-acls/%s", version.APIVersion, aclName)
		if d.projectName != api.ProjectDefaultName {
			uri += fmt.Sprintf("?project=%s", d.projectName)
		}

		usedBy = append(usedBy, uri)
		if firstOnly {
			return db.ErrInstanceListStop
		}

		return nil
	}, d.info.Name)
	if err != nil {
		if err == db.ErrInstanceListStop {
			return usedBy, nil
		}

		return nil, fmt.Errorf("Failed getting address set usage: %w", err)
	}

	return usedBy, nil
}

// UsedBy returns a list of ACL API endpoints referencing this address set.
func (d *common) UsedBy() ([]string, error) {
	return d.usedBy(false)
}

// Etag returns the values used for etag generation.
func (d *common) Etag() []any {
	return []any{d.info.Name, d.info.Description, d.info.Addresses, d.info.Config}
}

// validateName checks name is valid.
func (d *common) validateName(name string) error {
	return ValidName(name)
}

// validateAddresses ensure set is valid.
func (d *common) validateAddresses(addresses []string) error {
	seen := make(map[string]struct{})

	for i, addr := range addresses {
		_, exists := seen[addr]
		if exists {
			return fmt.Errorf("Duplicate address %q found at index %d", addr, i)
		}

		seen[addr] = struct{}{}
		// Check if it's a valid plain IP address.
		if net.ParseIP(addr) != nil {
			continue
		}

		// Check if it's a valid CIDR.
		_, _, err := net.ParseCIDR(addr)
		if err == nil {
			continue
		}

		// Check if it's a valid MAC address.
		_, err = net.ParseMAC(addr)
		if err == nil {
			return fmt.Errorf("Unsupported MAC address format %q at index %d", addr, i)
		}

		return fmt.Errorf("Unsupported address format %q at index %d", addr, i)
	}

	return nil
}

// validateConfig checks the entire config including name and addresses.
func (d *common) validateConfig(config *api.NetworkAddressSetPut) error {
	// Validate the address list.
	err := d.validateAddresses(config.Addresses)
	if err != nil {
		return fmt.Errorf("Invalid addresses: %w", err)
	}

	// Validate the configuration.
	configKeys := map[string]func(value string) error{}

	for k, v := range config.Config {
		// User keys are free for all.

		// gendoc:generate(entity=network_address_set, group=common, key=user.*)
		// User keys can be used in search.
		// ---
		//  type: string
		//  shortdesc: Free form user key/value storage
		if strings.HasPrefix(k, "user.") {
			continue
		}

		validator, ok := configKeys[k]
		if !ok {
			return fmt.Errorf("Invalid network integration configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid network integration configuration key %q value", k)
		}
	}

	return nil
}

// Update method is used to update an address set and apply to concerned networks.
func (d *common) Update(config *api.NetworkAddressSetPut, clientType request.ClientType) error {
	reverter := revert.New()
	defer reverter.Fail()

	// Validate the new configuration.
	err := d.validateConfig(config)
	if err != nil {
		return err
	}

	if clientType == request.ClientTypeNormal {
		var dbRecord *dbCluster.NetworkAddressSet
		oldConfig := d.info.NetworkAddressSetPut

		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error

			// Get current record.
			dbRecord, err = dbCluster.GetNetworkAddressSet(ctx, tx.Tx(), d.projectName, d.info.Name)
			if err != nil {
				return err
			}

			// Update database. Its important this occurs before we attempt to apply to networks.
			dbRecord.Addresses = config.Addresses
			dbRecord.Description = config.Description

			err = dbCluster.UpdateNetworkAddressSet(ctx, tx.Tx(), d.projectName, d.info.Name, *dbRecord)
			if err != nil {
				return err
			}

			// Update the configuration.
			err = dbCluster.UpdateNetworkAddressSetConfig(ctx, tx.Tx(), int64(dbRecord.ID), config.Config)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Apply changes internally and reinitialize.
		d.info.NetworkAddressSetPut = *config
		d.init(d.state, d.id, d.projectName, d.info)

		reverter.Add(func() {
			_ = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				var err error

				// Update database. Its important this occurs before we attempt to apply to networks.
				dbRecord.Addresses = oldConfig.Addresses
				dbRecord.Description = oldConfig.Description

				err = dbCluster.UpdateNetworkAddressSet(ctx, tx.Tx(), d.projectName, d.info.Name, *dbRecord)
				if err != nil {
					return err
				}

				// Update the configuration.
				err = dbCluster.UpdateNetworkAddressSetConfig(ctx, tx.Tx(), int64(dbRecord.ID), oldConfig.Config)
				if err != nil {
					return err
				}

				return nil
			})

			d.info.NetworkAddressSetPut = oldConfig
			d.init(d.state, d.id, d.projectName, d.info)
		})
	}

	// Get a list of networks that indirectly reference this address set via ACLs.
	asNets := map[string]AddressSetUsage{}
	err = AddressSetNetworkUsage(d.state, d.projectName, d.info.Name, d.info.Addresses, asNets)
	if err != nil {
		return fmt.Errorf("Failed getting address set network usage: %w", err)
	}

	// Separate out OVN networks from non-OVN networks for different handling.
	asOVNNets := map[string]AddressSetUsage{}
	for k, v := range asNets {
		if v.Type == "ovn" {
			delete(asNets, k)
			asOVNNets[k] = v
		} else if v.Type != "bridge" {
			return fmt.Errorf("Unsupported network type %q using address set %q", v.Type, d.info.Name)
		}
	}

	// Apply address set changes to non-OVN networks on this member.
	if len(asNets) > 0 {
		for _, asNet := range asNets {
			if asNet.DeviceName != "" {
				err = FirewallApplyAddressSetsForACLRules(d.state, "bridge", d.projectName, asNet.ACLNames)
				if err != nil {
					return err
				}
			} else {
				err = FirewallApplyAddressSets(d.state, d.projectName, asNet)
				if err != nil {
					return err
				}
			}
		}
	}

	// If there are affected OVN networks, then apply changes if request type is normal.
	if len(asOVNNets) > 0 && clientType == request.ClientTypeNormal {
		// Check that OVN is available.
		ovnnb, _, err := d.state.OVN()
		if err != nil {
			return err
		}

		// Ensure address sets are created or updated in OVN.
		cleanup, err := OVNEnsureAddressSets(d.state, d.logger, ovnnb, d.projectName, []string{d.info.Name})
		if err != nil {
			return fmt.Errorf("Failed ensuring address set %q is configured in OVN: %w", d.info.Name, err)
		}

		reverter.Add(cleanup)
	}

	// If normal request and asNets is not empty, notify other cluster members.
	if clientType == request.ClientTypeNormal && len(asNets) > 0 {
		notifier, err := cluster.NewNotifier(d.state, d.state.Endpoints.NetworkCert(), d.state.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return err
		}

		err = notifier(func(client incus.InstanceServer) error {
			// Make sure we have a suitable endpoint for updating the address set on other members.
			return client.UseProject(d.projectName).UpdateNetworkAddressSet(d.info.Name, d.info.NetworkAddressSetPut, "")
		})
		if err != nil {
			return err
		}
	}

	reverter.Success()
	return nil
}

// Rename is used to rename an address set.
func (d *common) Rename(newName string) error {
	err := d.validateName(newName)
	if err != nil {
		return err
	}

	// Check if name already exists.
	_, err = LoadByName(d.state, d.projectName, newName)
	if err == nil {
		return fmt.Errorf("Address set by that name: %s exists already", newName)
	}

	usedBy, err := d.UsedBy()
	if err != nil {
		return err
	}

	if len(usedBy) > 0 {
		return fmt.Errorf("Cannot rename address set that is in use")
	}

	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.RenameNetworkAddressSet(ctx, tx.Tx(), d.projectName, d.info.Name, newName)
	})
	if err != nil {
		return err
	}

	d.info.Name = newName
	return nil
}

// Delete is used to delete an address set.
func (d *common) Delete() error {
	usedBy, err := d.UsedBy()
	if err != nil {
		return err
	}

	if len(usedBy) > 0 {
		return fmt.Errorf("Cannot delete address set that is in use")
	}

	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.DeleteNetworkAddressSet(ctx, tx.Tx(), d.projectName, d.info.Name)
	})
	if err != nil {
		return fmt.Errorf("Error while deleting address set from db")
	}

	return nil
}
