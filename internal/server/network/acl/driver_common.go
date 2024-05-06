package acl

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/lxc/incus/v6/client"
	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/state"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

// Define type for rule directions.
type ruleDirection string

const ruleDirectionIngress ruleDirection = "ingress"
const ruleDirectionEgress ruleDirection = "egress"

// ReservedNetworkSubects contains a list of reserved network peer names (those starting with @ character) that
// cannot be used when to name peering connections. Otherwise peer connections wouldn't be able to be referenced
// in ACL rules using the "@<peer name>" format without the potential of conflicts.
var ReservedNetworkSubects = []string{"internal", "external"}

// Define reserved ACL subjects.
const ruleSubjectInternal = "@internal"
const ruleSubjectExternal = "@external"

// Define aliases for reserved ACL subjects. This is to allow earlier deprecated names that used the "#" prefix.
// They were deprecated to avoid confusion with YAML comments. So "#internal" and "#external" should not be used.
var ruleSubjectInternalAliases = []string{ruleSubjectInternal, "#internal"}
var ruleSubjectExternalAliases = []string{ruleSubjectExternal, "#external"}

// ValidActions defines valid actions for rules.
var ValidActions = []string{"allow", "allow-stateless", "drop", "reject"}

// common represents a Network ACL.
type common struct {
	logger      logger.Logger
	state       *state.State
	id          int64
	projectName string
	info        *api.NetworkACL
}

// init initialize internal variables.
func (d *common) init(state *state.State, id int64, projectName string, info *api.NetworkACL) {
	if info == nil {
		d.info = &api.NetworkACL{}
	} else {
		d.info = info
	}

	d.logger = logger.AddContext(logger.Ctx{"project": projectName, "networkACL": d.info.Name})
	d.id = id
	d.projectName = projectName
	d.state = state

	if d.info.Ingress == nil {
		d.info.Ingress = []api.NetworkACLRule{}
	}

	for i := range d.info.Ingress {
		d.info.Ingress[i].Normalise()
	}

	if d.info.Egress == nil {
		d.info.Egress = []api.NetworkACLRule{}
	}

	for i := range d.info.Egress {
		d.info.Egress[i].Normalise()
	}

	if d.info.Config == nil {
		d.info.Config = make(map[string]string)
	}
}

// ID returns the Network ACL ID.
func (d *common) ID() int64 {
	return d.id
}

// Project returns the project name.
func (d *common) Project() string {
	return d.projectName
}

// Info returns copy of internal info for the Network ACL.
func (d *common) Info() *api.NetworkACL {
	// Copy internal info to prevent modification externally.
	info := api.NetworkACL{}
	info.Name = d.info.Name
	info.Description = d.info.Description
	info.Ingress = append(make([]api.NetworkACLRule, 0, len(d.info.Ingress)), d.info.Ingress...)
	info.Egress = append(make([]api.NetworkACLRule, 0, len(d.info.Egress)), d.info.Egress...)
	info.Config = localUtil.CopyConfig(d.info.Config)
	info.UsedBy = nil // To indicate its not populated (use Usedby() function to populate).
	info.Project = d.projectName

	return &info
}

// usedBy returns a list of API endpoints referencing this ACL.
// If firstOnly is true then search stops at first result.
func (d *common) usedBy(firstOnly bool) ([]string, error) {
	usedBy := []string{}

	// Find all networks, profiles and instance NICs that use this Network ACL.
	err := UsedBy(d.state, d.projectName, func(ctx context.Context, tx *db.ClusterTx, _ []string, usageType any, _ string, _ map[string]string) error {
		switch u := usageType.(type) {
		case db.InstanceArgs:
			uri := fmt.Sprintf("/%s/instances/%s", version.APIVersion, u.Name)
			if u.Project != api.ProjectDefaultName {
				uri += fmt.Sprintf("?project=%s", u.Project)
			}

			usedBy = append(usedBy, uri)
		case *api.Network:
			uri := fmt.Sprintf("/%s/networks/%s", version.APIVersion, u.Name)
			if d.projectName != api.ProjectDefaultName {
				uri += fmt.Sprintf("?project=%s", d.projectName)
			}

			usedBy = append(usedBy, uri)
		case dbCluster.Profile:
			uri := fmt.Sprintf("/%s/profiles/%s", version.APIVersion, u.Name)
			if u.Project != api.ProjectDefaultName {
				uri += fmt.Sprintf("?project=%s", u.Project)
			}

			usedBy = append(usedBy, uri)
		case *api.NetworkACL:
			uri := fmt.Sprintf("/%s/network-acls/%s", version.APIVersion, u.Name)
			if d.projectName != api.ProjectDefaultName {
				uri += fmt.Sprintf("?project=%s", d.projectName)
			}

			usedBy = append(usedBy, uri)
		default:
			return fmt.Errorf("Unrecognised usage type %T", u)
		}

		if firstOnly {
			return db.ErrInstanceListStop
		}

		return nil
	}, d.Info().Name)
	if err != nil {
		if err == db.ErrInstanceListStop {
			return usedBy, nil
		}

		return nil, fmt.Errorf("Failed getting ACL usage: %w", err)
	}

	return usedBy, nil
}

// UsedBy returns a list of API endpoints referencing this ACL.
func (d *common) UsedBy() ([]string, error) {
	return d.usedBy(false)
}

// isUsed returns whether or not the ACL is in use.
func (d *common) isUsed() (bool, error) {
	usedBy, err := d.usedBy(true)
	if err != nil {
		return false, err
	}

	return len(usedBy) > 0, nil
}

// Etag returns the values used for etag generation.
func (d *common) Etag() []any {
	return []any{d.info.Name, d.info.Description, d.info.Ingress, d.info.Egress, d.info.Config}
}

// validateName checks name is valid.
func (d *common) validateName(name string) error {
	return ValidName(name)
}

// validateConfig checks the config and rules are valid.
func (d *common) validateConfig(info *api.NetworkACLPut) error {
	err := d.validateConfigMap(info.Config, nil)
	if err != nil {
		return err
	}

	// Normalise rules before validation for duplicate detection.
	for i := range info.Ingress {
		info.Ingress[i].Normalise()
	}

	for i := range info.Egress {
		info.Egress[i].Normalise()
	}

	// Validate each ingress rule.
	for i, ingressRule := range info.Ingress {
		err := d.validateRule(ruleDirectionIngress, ingressRule)
		if err != nil {
			return fmt.Errorf("Invalid ingress rule %d: %w", i, err)
		}

		// Check for duplicates.
		for ri, r := range info.Ingress {
			if ri == i {
				continue // Skip ourselves.
			}

			if r == ingressRule {
				return fmt.Errorf("Duplicate of ingress rule %d", i)
			}
		}
	}

	// Validate each egress rule.
	for i, egressRule := range info.Egress {
		err := d.validateRule(ruleDirectionEgress, egressRule)
		if err != nil {
			return fmt.Errorf("Invalid egress rule %d: %w", i, err)
		}

		// Check for duplicates.
		for ri, r := range info.Egress {
			if ri == i {
				continue // Skip ourselves.
			}

			if r == egressRule {
				return fmt.Errorf("Duplicate of egress rule %d", i)
			}
		}
	}

	return nil
}

// validateConfigMap checks ACL config map against rules.
func (d *common) validateConfigMap(config map[string]string, rules map[string]func(value string) error) error {
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

// validateRule validates the rule supplied.
func (d *common) validateRule(direction ruleDirection, rule api.NetworkACLRule) error {
	// Validate Action field (required).
	if !slices.Contains(ValidActions, rule.Action) {
		return fmt.Errorf("Action must be one of: %s", strings.Join(ValidActions, ", "))
	}

	// Validate State field (required).
	validStates := []string{"enabled", "disabled", "logged"}
	if !slices.Contains(validStates, rule.State) {
		return fmt.Errorf("State must be one of: %s", strings.Join(validStates, ", "))
	}

	var acls map[string]int64

	err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get map of ACL names to DB IDs (used for generating OVN port group names).
		acls, err = tx.GetNetworkACLIDsByNames(ctx, d.Project())

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed getting network ACLs for security ACL subject validation: %w", err)
	}

	validSubjectNames := make([]string, 0, len(acls)+len(ruleSubjectInternalAliases)+len(ruleSubjectExternalAliases))
	validSubjectNames = append(validSubjectNames, ruleSubjectInternalAliases...)
	validSubjectNames = append(validSubjectNames, ruleSubjectExternalAliases...)

	for aclName := range acls {
		validSubjectNames = append(validSubjectNames, aclName)
	}

	var srcHasName, srcHasIPv4, srcHasIPv6 bool
	var dstHasName, dstHasIPv4, dstHasIPv6 bool

	// Validate Source field.
	if rule.Source != "" {
		srcHasName, srcHasIPv4, srcHasIPv6, err = d.validateRuleSubjects("Source", direction, util.SplitNTrimSpace(rule.Source, ",", -1, false), validSubjectNames)
		if err != nil {
			return fmt.Errorf("Invalid Source: %w", err)
		}
	}

	// Validate Destination field.
	if rule.Destination != "" {
		dstHasName, dstHasIPv4, dstHasIPv6, err = d.validateRuleSubjects("Destination", direction, util.SplitNTrimSpace(rule.Destination, ",", -1, false), validSubjectNames)
		if err != nil {
			return fmt.Errorf("Invalid Destination: %w", err)
		}
	}

	// Check combination of subject types is valid for source/destination.
	if rule.Source != "" && rule.Destination != "" {
		if (srcHasIPv4 && !dstHasIPv4 && !dstHasName) ||
			(dstHasIPv4 && !srcHasIPv4 && !srcHasName) ||
			(srcHasIPv6 && !dstHasIPv6 && !dstHasName) ||
			(dstHasIPv6 && !srcHasIPv6 && !srcHasName) {
			return fmt.Errorf("Conflicting IP family types used for Source and Destination")
		}
	}

	// Validate Protocol field.
	if rule.Protocol != "" {
		validProtocols := []string{"icmp4", "icmp6", "tcp", "udp"}
		if !slices.Contains(validProtocols, rule.Protocol) {
			return fmt.Errorf("Protocol must be one of: %s", strings.Join(validProtocols, ", "))
		}
	}

	// Validate protocol dependent fields.
	if slices.Contains([]string{"tcp", "udp"}, rule.Protocol) {
		if rule.ICMPType != "" {
			return fmt.Errorf("ICMP type cannot be used with non-ICMP protocol")
		}

		if rule.ICMPCode != "" {
			return fmt.Errorf("ICMP code cannot be used with non-ICMP protocol")
		}

		// Validate SourcePort field.
		if rule.SourcePort != "" {
			err := d.validatePorts(util.SplitNTrimSpace(rule.SourcePort, ",", -1, false))
			if err != nil {
				return fmt.Errorf("Invalid Source port: %w", err)
			}
		}

		// Validate DestinationPort field.
		if rule.DestinationPort != "" {
			err := d.validatePorts(util.SplitNTrimSpace(rule.DestinationPort, ",", -1, false))
			if err != nil {
				return fmt.Errorf("Invalid Destination port: %w", err)
			}
		}
	} else if slices.Contains([]string{"icmp4", "icmp6"}, rule.Protocol) {
		if rule.SourcePort != "" {
			return fmt.Errorf("Source port cannot be used with %q protocol", rule.Protocol)
		}

		if rule.DestinationPort != "" {
			return fmt.Errorf("Destination port cannot be used with %q protocol", rule.Protocol)
		}

		if rule.Protocol == "icmp4" {
			if srcHasIPv6 {
				return fmt.Errorf("Cannot use IPv6 source addresses with %q protocol", rule.Protocol)
			}

			if dstHasIPv6 {
				return fmt.Errorf("Cannot use IPv6 destination addresses with %q protocol", rule.Protocol)
			}
		} else if rule.Protocol == "icmp6" {
			if srcHasIPv4 {
				return fmt.Errorf("Cannot use IPv4 source addresses with %q protocol", rule.Protocol)
			}

			if dstHasIPv4 {
				return fmt.Errorf("Cannot use IPv4 destination addresses with %q protocol", rule.Protocol)
			}
		}

		// Validate ICMPType field.
		if rule.ICMPType != "" {
			err := validate.IsUint8(rule.ICMPType)
			if err != nil {
				return fmt.Errorf("Invalid ICMP type: %w", err)
			}
		}

		// Validate ICMPCode field.
		if rule.ICMPCode != "" {
			err := validate.IsUint8(rule.ICMPCode)
			if err != nil {
				return fmt.Errorf("Invalid ICMP code: %w", err)
			}
		}
	} else {
		if rule.ICMPType != "" {
			return fmt.Errorf("ICMP type cannot be used without specifying protocol")
		}

		if rule.ICMPCode != "" {
			return fmt.Errorf("ICMP code cannot be used without specifying protocol")
		}

		if rule.SourcePort != "" {
			return fmt.Errorf("Source port cannot be used without specifying protocol")
		}

		if rule.DestinationPort != "" {
			return fmt.Errorf("Destination port cannot be used without specifying protocol")
		}
	}

	return nil
}

// validateRuleSubjects checks that the source or destination subjects for a rule are valid.
// Accepts a validSubjectNames list of valid ACL or special classifier names.
// Returns whether the subjects include names, IPv4 and IPv6 addresses respectively.
func (d *common) validateRuleSubjects(fieldName string, direction ruleDirection, subjects []string, validSubjectNames []string) (bool, bool, bool, error) {
	// Check if named subjects are allowed in field/direction combination.
	allowSubjectNames := false
	if (fieldName == "Source" && direction == ruleDirectionIngress) || (fieldName == "Destination" && direction == ruleDirectionEgress) {
		allowSubjectNames = true
	}

	isNetworkAddress := func(value string) (uint, error) {
		ip := net.ParseIP(value)
		if ip == nil {
			return 0, fmt.Errorf("Not an IP address %q", value)
		}

		var ipVersion uint = 4
		if ip.To4() == nil {
			ipVersion = 6
		}

		return ipVersion, nil
	}

	isNetworkAddressCIDR := func(value string) (uint, error) {
		ip, _, err := net.ParseCIDR(value)
		if err != nil {
			return 0, err
		}

		var ipVersion uint = 4
		if ip.To4() == nil {
			ipVersion = 6
		}

		return ipVersion, nil
	}

	isNetworkRange := func(value string) (uint, error) {
		err := validate.IsNetworkRange(value)
		if err != nil {
			return 0, err
		}

		ips := strings.SplitN(value, "-", 2)
		if len(ips) != 2 {
			return 0, fmt.Errorf("IP range must contain start and end IP addresses")
		}

		ip := net.ParseIP(ips[0])

		var ipVersion uint = 4
		if ip.To4() == nil {
			ipVersion = 6
		}

		return ipVersion, nil
	}

	checks := []func(s string) (uint, error){
		isNetworkAddress,
		isNetworkAddressCIDR,
		isNetworkRange,
	}

	validSubject := func(subject string) (uint, error) {
		// Check if it is one of the network IP types.
		for _, c := range checks {
			ipVersion, err := c(subject)
			if err == nil {
				return ipVersion, nil // Found valid subject.
			}
		}

		// Check if it is one of the valid subject names.
		for _, n := range validSubjectNames {
			if subject == n {
				if allowSubjectNames {
					return 0, nil // Found valid subject.
				}

				return 0, fmt.Errorf("Named subjects not allowed in %q for %q rules", fieldName, direction)
			}
		}

		// Check if it looks like a network peer connection name.
		if strings.HasPrefix(subject, "@") {
			if allowSubjectNames {
				return 0, nil // Found valid subject.
			}

			return 0, fmt.Errorf("Named subjects not allowed in %q for %q rules", fieldName, direction)
		}

		return 0, fmt.Errorf("Invalid subject %q", subject)
	}

	hasIPv4 := false
	hasIPv6 := false
	hasName := false

	for _, s := range subjects {
		ipVersion, err := validSubject(s)
		if err != nil {
			return false, false, false, err
		}

		switch ipVersion {
		case 0:
			hasName = true
		case 4:
			hasIPv4 = true
		case 6:
			hasIPv6 = true
		}
	}

	return hasName, hasIPv4, hasIPv6, nil
}

// validatePorts checks that the source or destination ports for a rule are valid.
func (d *common) validatePorts(ports []string) error {
	for _, port := range ports {
		err := validate.IsNetworkPortRange(port)
		if err != nil {
			return err
		}
	}

	return nil
}

// Update applies the supplied config to the ACL.
func (d *common) Update(config *api.NetworkACLPut, clientType request.ClientType) error {
	// Validate the configuration.
	err := d.validateConfig(config)
	if err != nil {
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	if clientType == request.ClientTypeNormal {
		oldConfig := d.info.NetworkACLPut

		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Update database. Its important this occurs before we attempt to apply to networks using the ACL
			// as usage functions will inspect the database.
			return tx.UpdateNetworkACL(ctx, d.id, config)
		})
		if err != nil {
			return err
		}

		// Apply changes internally and reinitialize.
		d.info.NetworkACLPut = *config
		d.init(d.state, d.id, d.projectName, d.info)

		revert.Add(func() {
			_ = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.UpdateNetworkACL(ctx, d.id, &oldConfig)
			})

			d.info.NetworkACLPut = oldConfig
			d.init(d.state, d.id, d.projectName, d.info)
		})
	}

	// Get a list of networks that are using this ACL (either directly or indirectly via a NIC).
	aclNets := map[string]NetworkACLUsage{}
	err = NetworkUsage(d.state, d.projectName, []string{d.info.Name}, aclNets)
	if err != nil {
		return fmt.Errorf("Failed getting ACL network usage: %w", err)
	}

	// Separate out OVN networks from non-OVN networks. This is because OVN networks share ACL config, and
	// so changes are not applied entirely on a per-network basis and need to be treated differently.
	aclOVNNets := map[string]NetworkACLUsage{}
	for k, v := range aclNets {
		if v.Type == "ovn" {
			delete(aclNets, k)
			aclOVNNets[k] = v
		} else if v.Type != "bridge" {
			return fmt.Errorf("Unsupported network ACL type %q", v.Type)
		}
	}

	// Apply ACL changes to non-OVN networks on this member.
	for _, aclNet := range aclNets {
		err = FirewallApplyACLRules(d.state, d.logger, d.projectName, aclNet)
		if err != nil {
			return err
		}
	}

	// If there are affected OVN networks, then apply the changes, but only if the request type is normal.
	// This way we won't apply the same changes multiple times for each cluster member.
	if len(aclOVNNets) > 0 && clientType == request.ClientTypeNormal {
		// Check that OVN is available.
		if d.state.OVNNB == nil {
			return fmt.Errorf("OVN isn't currently available")
		}

		var aclNameIDs map[string]int64

		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Get map of ACL names to DB IDs (used for generating OVN port group names).
			aclNameIDs, err = tx.GetNetworkACLIDsByNames(ctx, d.Project())

			return err
		})
		if err != nil {
			return fmt.Errorf("Failed getting network ACL IDs for security ACL update: %w", err)
		}

		// Request that the ACL and any referenced ACLs in the ruleset are created in OVN.
		// Pass aclOVNNets info, because although OVN networks share ACL port group definitions, when the
		// ACL rules themselves use network specific selectors such as @internal/@external, we then need to
		// apply those rules to each network affected by the ACL, so pass the full list of OVN networks
		// affected by this ACL (either because the ACL is assigned directly or because it is assigned to
		// an OVN NIC in an instance or profile).
		cleanup, err := OVNEnsureACLs(d.state, d.logger, d.state.OVNNB, d.projectName, aclNameIDs, aclOVNNets, []string{d.info.Name}, true)
		if err != nil {
			return fmt.Errorf("Failed ensuring ACL is configured in OVN: %w", err)
		}

		revert.Add(cleanup)

		// Run unused port group cleanup in case any formerly referenced ACL in this ACL's rules means that
		// an ACL port group is now considered unused.
		err = OVNPortGroupDeleteIfUnused(d.state, d.logger, d.state.OVNNB, d.projectName, nil, "", d.info.Name)
		if err != nil {
			return fmt.Errorf("Failed removing unused OVN port groups: %w", err)
		}
	}

	// Apply ACL changes to non-OVN networks on cluster members.
	if clientType == request.ClientTypeNormal && len(aclNets) > 0 {
		// Notify all other nodes to update the network if no target specified.
		notifier, err := cluster.NewNotifier(d.state, d.state.Endpoints.NetworkCert(), d.state.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return err
		}

		err = notifier(func(client incus.InstanceServer) error {
			return client.UseProject(d.projectName).UpdateNetworkACL(d.info.Name, d.info.NetworkACLPut, "")
		})
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// Rename renames the ACL if not in use.
func (d *common) Rename(newName string) error {
	_, err := LoadByName(d.state, d.projectName, newName)
	if err == nil {
		return fmt.Errorf("An ACL by that name exists already")
	}

	isUsed, err := d.isUsed()
	if err != nil {
		return err
	}

	if isUsed {
		return fmt.Errorf("Cannot rename an ACL that is in use")
	}

	err = d.validateName(newName)
	if err != nil {
		return err
	}

	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RenameNetworkACL(ctx, d.id, newName)
	})
	if err != nil {
		return err
	}

	// Apply changes internally.
	d.info.Name = newName

	return nil
}

// Delete deletes the ACL.
func (d *common) Delete() error {
	isUsed, err := d.isUsed()
	if err != nil {
		return err
	}

	if isUsed {
		return fmt.Errorf("Cannot delete an ACL that is in use")
	}

	return d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.DeleteNetworkACL(ctx, d.id)
	})
}

// GetLog gets the ACL log.
func (d *common) GetLog(clientType request.ClientType) (string, error) {
	// ACLs aren't specific to a particular network type but the log only works with OVN.
	logPath := "/var/log/ovn/ovn-controller.log"
	if !util.PathExists(logPath) {
		return "", fmt.Errorf("Only OVN log entries may be retrieved at this time")
	}

	// Open the log file.
	logFile, err := os.Open(logPath)
	if err != nil {
		return "", fmt.Errorf("Couldn't open OVN log file: %w", err)
	}

	defer func() { _ = logFile.Close() }()

	logEntries := []string{}
	scanner := bufio.NewScanner(logFile)
	for scanner.Scan() {
		logEntry := ovnParseLogEntry(scanner.Text(), fmt.Sprintf("incus_acl%d-", d.id))
		if logEntry == "" {
			continue
		}

		logEntries = append(logEntries, logEntry)
	}

	err = scanner.Err()
	if err != nil {
		return "", fmt.Errorf("Failed to read OVN log file: %w", err)
	}

	// Aggregates the entries from the rest of the cluster.
	if clientType == request.ClientTypeNormal {
		// Setup notifier to reach the rest of the cluster.
		notifier, err := cluster.NewNotifier(d.state, d.state.Endpoints.NetworkCert(), d.state.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return "", err
		}

		mu := sync.Mutex{}
		err = notifier(func(client incus.InstanceServer) error {
			// Get the entries.
			entries, err := client.UseProject(d.projectName).GetNetworkACLLogfile(d.info.Name)
			if err != nil {
				return err
			}

			defer func() { _ = entries.Close() }()

			// Prevent concurrent writes to the log entries slice.
			mu.Lock()
			defer mu.Unlock()

			// Parse the response and add to the slice.
			scanner := bufio.NewScanner(entries)
			for scanner.Scan() {
				entry := scanner.Text()
				if entry == "" {
					continue
				}

				logEntries = append(logEntries, entry)
			}

			err = scanner.Err()
			if err != nil {
				return fmt.Errorf("Failed to read OVN log file: %w", err)
			}

			return nil
		})
		if err != nil {
			return "", err
		}
	}

	// Just return empty if no log entries (no need for trailing line break).
	if len(logEntries) == 0 {
		return "", nil
	}

	// Sort the entries (by timestamp).
	sort.Strings(logEntries)

	return strings.Join(logEntries, "\n") + "\n", nil
}
