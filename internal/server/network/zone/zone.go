package zone

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/lxc/incus/client"
	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/cluster"
	"github.com/lxc/incus/internal/server/cluster/request"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/network"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/state"
	localUtil "github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

// zone represents a Network zone.
type zone struct {
	logger      logger.Logger
	state       *state.State
	id          int64
	projectName string
	info        *api.NetworkZone
}

// init initialise internal variables.
func (d *zone) init(state *state.State, id int64, projectName string, info *api.NetworkZone) {
	if info == nil {
		d.info = &api.NetworkZone{}
	} else {
		d.info = info
	}

	d.logger = logger.AddContext(logger.Ctx{"project": projectName, "networkzone": d.info.Name})
	d.id = id
	d.projectName = projectName
	d.state = state

	if d.info.Config == nil {
		d.info.Config = make(map[string]string)
	}
}

// ID returns the Network zone ID.
func (d *zone) ID() int64 {
	return d.id
}

// Name returns the project.
func (d *zone) Project() string {
	return d.projectName
}

// Info returns copy of internal info for the Network zone.
func (d *zone) Info() *api.NetworkZone {
	// Copy internal info to prevent modification externally.
	info := api.NetworkZone{}
	info.Name = d.info.Name
	info.Description = d.info.Description
	info.Config = localUtil.CopyConfig(d.info.Config)
	info.UsedBy = nil // To indicate its not populated (use Usedby() function to populate).

	return &info
}

// networkUsesZone indicates if the network uses the zone based on its config.
func (d *zone) networkUsesZone(netConfig map[string]string) bool {
	for _, key := range []string{"dns.zone.forward", "dns.zone.reverse.ipv4", "dns.zone.reverse.ipv6"} {
		zoneNames := util.SplitNTrimSpace(netConfig[key], ",", -1, true)
		if util.ValueInSlice(d.info.Name, zoneNames) {
			return true
		}
	}

	return false
}

// usedBy returns a list of API endpoints referencing this zone.
// If firstOnly is true then search stops at first result.
func (d *zone) usedBy(firstOnly bool) ([]string, error) {
	usedBy := []string{}

	var networkNames []string

	err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Find networks using the zone.
		networkNames, err = tx.GetCreatedNetworkNamesByProject(ctx, d.projectName)
		if err != nil && !response.IsNotFoundError(err) {
			return fmt.Errorf("Failed loading networks for project %q: %w", d.projectName, err)
		}

		for _, networkName := range networkNames {
			_, network, _, err := tx.GetNetworkInAnyState(ctx, d.projectName, networkName)
			if err != nil {
				return fmt.Errorf("Failed to get network config for %q: %w", networkName, err)
			}

			// Check if the network is using this zone.
			if d.networkUsesZone(network.Config) {
				u := api.NewURL().Path(version.APIVersion, "networks", networkName)
				usedBy = append(usedBy, u.String())
				if firstOnly {
					return nil
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return usedBy, nil
}

// UsedBy returns a list of API endpoints referencing this zone.
func (d *zone) UsedBy() ([]string, error) {
	return d.usedBy(false)
}

// isUsed returns whether or not the zone is in use.
func (d *zone) isUsed() (bool, error) {
	usedBy, err := d.usedBy(true)
	if err != nil {
		return false, err
	}

	return len(usedBy) > 0, nil
}

// Etag returns the values used for etag generation.
func (d *zone) Etag() []any {
	return []any{d.info.Name, d.info.Description, d.info.Config}
}

// validateName checks name is valid.
func (d *zone) validateName(name string) error {
	if name == "" {
		return fmt.Errorf("Name is required")
	}

	if strings.HasPrefix(name, "/") {
		return fmt.Errorf(`Name cannot start with "/"`)
	}

	return nil
}

// validateConfig checks the config and rules are valid.
func (d *zone) validateConfig(info *api.NetworkZonePut) error {
	rules := map[string]func(value string) error{}

	// Regular config keys.
	rules["dns.nameservers"] = validate.IsListOf(validate.IsAny)
	rules["network.nat"] = validate.Optional(validate.IsBool)

	// Validate peer config.
	for k := range info.Config {
		if !strings.HasPrefix(k, "peers.") {
			continue
		}

		// Validate remote name in key.
		fields := strings.Split(k, ".")
		if len(fields) != 3 {
			return fmt.Errorf("Invalid network zone configuration key %q", k)
		}

		peerKey := fields[2]

		// Add the correct validation rule for the dynamic field based on last part of key.
		switch peerKey {
		case "address":
			rules[k] = validate.Optional(validate.IsNetworkAddress)
		case "key":
			rules[k] = validate.Optional(validate.IsAny)
		}
	}

	err := d.validateConfigMap(info.Config, rules)
	if err != nil {
		return err
	}

	return nil
}

// validateConfigMap checks zone config map against rules.
func (d *zone) validateConfigMap(config map[string]string, rules map[string]func(value string) error) error {
	checkedFields := map[string]struct{}{}

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(config[k])
		if err != nil {
			return fmt.Errorf("Invalid value for config option %q: %w", k, err)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range config {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// User keys are not validated.
		if internalInstance.IsUserConfig(k) {
			continue
		}

		return fmt.Errorf("Invalid config option %q", k)
	}

	return nil
}

// Update applies the supplied config to the zone.
func (d *zone) Update(config *api.NetworkZonePut, clientType request.ClientType) error {
	err := d.validateConfig(config)
	if err != nil {
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	// Update the database and notify the rest of the cluster.
	if clientType == request.ClientTypeNormal {
		oldConfig := d.info.NetworkZonePut

		// Update database.
		err = d.state.DB.Cluster.UpdateNetworkZone(d.id, config)
		if err != nil {
			return err
		}

		// Apply changes internally and reinitialise.
		d.info.NetworkZonePut = *config
		d.init(d.state, d.id, d.projectName, d.info)

		revert.Add(func() {
			_ = d.state.DB.Cluster.UpdateNetworkZone(d.id, &oldConfig)
			d.info.NetworkZonePut = oldConfig
			d.init(d.state, d.id, d.projectName, d.info)
		})

		// Notify all other nodes to update the network zone if no target specified.
		notifier, err := cluster.NewNotifier(d.state, d.state.Endpoints.NetworkCert(), d.state.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return err
		}

		err = notifier(func(client incus.InstanceServer) error {
			return client.UseProject(d.projectName).UpdateNetworkZone(d.info.Name, d.info.NetworkZonePut, "")
		})
		if err != nil {
			return err
		}
	}

	// Trigger a refresh of the TSIG entries.
	err = d.state.DNS.UpdateTSIG()
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// Delete deletes the zone.
func (d *zone) Delete() error {
	isUsed, err := d.isUsed()
	if err != nil {
		return err
	}

	if isUsed {
		return fmt.Errorf("Cannot delete a zone that is in use")
	}

	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Delete the database record.
		err = tx.DeleteNetworkZone(ctx, d.id)

		return err
	})
	if err != nil {
		return err
	}

	// Trigger a refresh of the TSIG entries.
	err = d.state.DNS.UpdateTSIG()
	if err != nil {
		return err
	}

	return nil
}

// Content returns the DNS zone content.
func (d *zone) Content() (*strings.Builder, error) {
	var err error
	records := []map[string]string{}

	// Check if we should include NAT records.
	includeNAT := util.IsTrueOrEmpty(d.info.Config["network.nat"])

	// Get all managed networks across all projects.
	var projectNetworks map[string]map[int64]api.Network
	var zoneProjects map[string]string
	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNetworks, err = tx.GetCreatedNetworks(ctx)
		if err != nil {
			return fmt.Errorf("Failed to load all networks: %w", err)
		}

		zoneProjects, err = tx.GetNetworkZones(ctx)
		if err != nil {
			return fmt.Errorf("Failed to load all network zones: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	for netProjectName, networks := range projectNetworks {
		for _, netInfo := range networks {
			if !d.networkUsesZone(netInfo.Config) {
				continue
			}

			// Load the network.
			n, err := network.LoadByName(d.state, netProjectName, netInfo.Name)
			if err != nil {
				return nil, err
			}

			// Check whether what records to include.
			netConfig := n.Config()
			includeV4 := includeNAT || util.IsFalseOrEmpty(netConfig["ipv4.nat"])
			includeV6 := includeNAT || util.IsFalseOrEmpty(netConfig["ipv6.nat"])

			// Check if dealing with a reverse zone.
			isReverse4 := strings.HasSuffix(d.info.Name, ip4Arpa)
			isReverse6 := strings.HasSuffix(d.info.Name, ip6Arpa)
			isReverse := isReverse4 || isReverse6

			genRecord := func(name string, ip net.IP) map[string]string {
				isV4 := ip.To4() != nil

				// Skip disabled families.
				if isV4 && !includeV4 {
					return nil
				}

				if !isV4 && !includeV6 {
					return nil
				}

				record := map[string]string{}
				record["ttl"] = "300"
				if !isReverse {
					if isV4 {
						record["type"] = "A"
					} else {
						record["type"] = "AAAA"
					}

					record["name"] = name
					record["value"] = ip.String()
				} else {
					// Skip PTR records for wrong family.
					if isV4 && !isReverse4 {
						return nil
					}

					if !isV4 && !isReverse6 {
						return nil
					}

					// Get the ARPA record.
					reverseAddr := reverse(ip)
					if reverseAddr == "" {
						return nil
					}

					record["type"] = "PTR"
					record["name"] = strings.TrimSuffix(reverseAddr, "."+d.info.Name+".")
					record["value"] = name + "."
				}

				return record
			}

			if isReverse {
				// Load network leases in correct project context for each forward zone referenced.
				for _, forwardZoneName := range util.SplitNTrimSpace(n.Config()["dns.zone.forward"], ",", -1, true) {
					// Get forward zone's project.
					forwardZoneProjectName := zoneProjects[forwardZoneName]
					if forwardZoneProjectName == "" {
						return nil, fmt.Errorf("Associated project not found for zone %q", forwardZoneName)
					}

					// Load the leases for the forward zone project.
					leases, err := n.Leases(forwardZoneProjectName, request.ClientTypeNormal)
					if err != nil {
						return nil, err
					}

					// Convert leases to usable PTR records.
					for _, lease := range leases {
						ip := net.ParseIP(lease.Address)

						// Get the record.
						record := genRecord(fmt.Sprintf("%s.%s", lease.Hostname, forwardZoneName), ip)
						if record == nil {
							continue
						}

						records = append(records, record)
					}
				}
			} else {
				// Load the leases in the forward zone's project.
				leases, err := n.Leases(d.projectName, request.ClientTypeNormal)
				if err != nil {
					return nil, err
				}

				// Convert leases to usable records.
				for _, lease := range leases {
					ip := net.ParseIP(lease.Address)

					// Get the record.
					record := genRecord(lease.Hostname, ip)
					if record == nil {
						continue
					}

					records = append(records, record)
				}
			}
		}
	}

	// Add the extra records.
	extraRecords, err := d.GetRecords()
	if err != nil {
		return nil, err
	}

	for _, extraRecord := range extraRecords {
		for _, entry := range extraRecord.Entries {
			record := map[string]string{}
			if entry.TTL > 0 {
				record["ttl"] = fmt.Sprintf("%d", entry.TTL)
			} else {
				record["ttl"] = "300"
			}

			record["type"] = entry.Type
			record["name"] = extraRecord.Name
			record["value"] = entry.Value

			records = append(records, record)
		}
	}

	// Get the nameservers.
	nameservers := []string{}
	for _, entry := range strings.Split(d.info.Config["dns.nameservers"], ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		nameservers = append(nameservers, entry)
	}

	primary := "hostmaster." + d.info.Name
	if len(nameservers) > 0 {
		primary = nameservers[0]
	}

	// Template the zone file.
	sb := &strings.Builder{}
	err = zoneTemplate.Execute(sb, map[string]any{
		"primary":     primary,
		"nameservers": nameservers,
		"zone":        d.info.Name,
		"serial":      time.Now().Unix(),
		"records":     records,
	})
	if err != nil {
		return nil, err
	}

	return sb, nil
}

// SOA returns just the DNS zone SOA record.
func (d *zone) SOA() (*strings.Builder, error) {
	// Get the nameservers.
	nameservers := []string{}
	for _, entry := range strings.Split(d.info.Config["dns.nameservers"], ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		nameservers = append(nameservers, entry)
	}

	primary := "hostmaster." + d.info.Name
	if len(nameservers) > 0 {
		primary = nameservers[0]
	}

	// Template the zone file.
	sb := &strings.Builder{}
	err := zoneTemplate.Execute(sb, map[string]any{
		"primary":     primary,
		"nameservers": nameservers,
		"zone":        d.info.Name,
		"serial":      time.Now().Unix(),
		"records":     map[string]string{},
	})
	if err != nil {
		return nil, err
	}

	return sb, nil
}
