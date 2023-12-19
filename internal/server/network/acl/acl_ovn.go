package acl

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/network/ovn"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

// OVN ACL rule priorities.
const ovnACLPriorityPortGroupDefaultAction = 0
const ovnACLPriorityNICDefaultActionIngress = 100

// ovnACLPriorityNICDefaultActionEgress needs to be >10 higher than ovnACLPriorityNICDefaultActionIngress so that
// ingress reject rules (that OVN adds 10 to their priorities) don't prevent egress rules being tested first.
const ovnACLPriorityNICDefaultActionEgress = 111
const ovnACLPrioritySwitchAllow = 200
const ovnACLPriorityPortGroupAllow = 300
const ovnACLPriorityPortGroupReject = 400
const ovnACLPriorityPortGroupDrop = 500

// ovnACLPortGroupPrefix prefix used when naming ACL related port groups in OVN.
const ovnACLPortGroupPrefix = "incus_acl"

// OVNACLPortGroupName returns the port group name for a Network ACL ID.
func OVNACLPortGroupName(networkACLID int64) ovn.OVNPortGroup {
	// OVN doesn't match port groups that have a "-" in them. So use an "_" for the separator.
	// This is because OVN port group names must match: [a-zA-Z_.][a-zA-Z_.0-9]*.
	return ovn.OVNPortGroup(fmt.Sprintf("%s%d", ovnACLPortGroupPrefix, networkACLID))
}

// OVNACLNetworkPortGroupName returns the port group name for a Network ACL ID and Network ID.
func OVNACLNetworkPortGroupName(networkACLID int64, networkID int64) ovn.OVNPortGroup {
	// OVN doesn't match port groups that have a "-" in them. So use an "_" for the separator.
	// This is because OVN port group names must match: [a-zA-Z_.][a-zA-Z_.0-9]*.
	return ovn.OVNPortGroup(fmt.Sprintf("%s%d_net%d", ovnACLPortGroupPrefix, networkACLID, networkID))
}

// OVNIntSwitchPortGroupName returns the port group name for a Network ID.
func OVNIntSwitchPortGroupName(networkID int64) ovn.OVNPortGroup {
	return ovn.OVNPortGroup(fmt.Sprintf("incus_net%d", networkID))
}

// OVNIntSwitchPortGroupAddressSetPrefix returns the internal switch routes address set prefix for a Network ID.
func OVNIntSwitchPortGroupAddressSetPrefix(networkID int64) ovn.OVNAddressSet {
	return ovn.OVNAddressSet(fmt.Sprintf("%s_routes", OVNIntSwitchPortGroupName(networkID)))
}

// OVNNetworkPrefix returns the prefix used for OVN entities related to a Network ID.
func OVNNetworkPrefix(networkID int64) string {
	return fmt.Sprintf("incus-net%d", networkID)
}

// OVNIntSwitchName returns the internal logical switch name for a Network ID.
func OVNIntSwitchName(networkID int64) ovn.OVNSwitch {
	return ovn.OVNSwitch(fmt.Sprintf("%s-ls-int", OVNNetworkPrefix(networkID)))
}

// OVNIntSwitchRouterPortName returns OVN logical internal switch router port name.
func OVNIntSwitchRouterPortName(networkID int64) ovn.OVNSwitchPort {
	return ovn.OVNSwitchPort(fmt.Sprintf("%s-lsp-router", OVNIntSwitchName(networkID)))
}

// OVNEnsureACLs ensures that the requested aclNames exist as OVN port groups (creates & applies ACL rules if not),
// If reapplyRules is true then the current ACL rules in the database are applied to the existing port groups
// rather than just new ones. Any ACLs referenced in the requested ACLs rules are also created as empty OVN port
// groups if needed. If a requested ACL exists, but has no ACL rules applied, then the current rules are loaded out
// of the database and applied. For each network provided in aclNets, the network specific port group for each ACL
// is checked for existence (it is created & applies network specific ACL rules if not).
// Returns a revert fail function that can be used to undo this function if a subsequent step fails.
func OVNEnsureACLs(s *state.State, l logger.Logger, client *ovn.NB, aclProjectName string, aclNameIDs map[string]int64, aclNets map[string]NetworkACLUsage, aclNames []string, reapplyRules bool) (revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	var err error
	var projectID int64
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectID, err = cluster.GetProjectID(ctx, tx.Tx(), aclProjectName)
		if err != nil {
			return fmt.Errorf("Failed getting project ID for project %q: %w", aclProjectName, err)
		}

		return err
	})
	if err != nil {
		return nil, err
	}

	peerTargetNetIDs, err := s.DB.Cluster.GetNetworkPeersTargetNetworkIDs(aclProjectName, db.NetworkTypeOVN)
	if err != nil {
		return nil, fmt.Errorf("Failed getting peer connection mappings: %w", err)
	}

	// First check all ACL Names map to IDs in supplied aclNameIDs.
	for _, aclName := range aclNames {
		_, found := aclNameIDs[aclName]
		if !found {
			return nil, fmt.Errorf("Cannot find security ACL ID for %q", aclName)
		}
	}

	// Next check which OVN port groups need creating and which exist already.
	type aclStatus struct {
		name       string
		uuid       ovn.OVNPortGroupUUID
		aclInfo    *api.NetworkACL
		addACLNets map[string]NetworkACLUsage
	}

	existingACLPortGroups := []aclStatus{}
	createACLPortGroups := []aclStatus{}

	for _, aclName := range aclNames {
		portGroupName := OVNACLPortGroupName(aclNameIDs[aclName])

		// Check if port group exists and has ACLs.
		portGroupUUID, portGroupHasACLs, err := client.PortGroupInfo(portGroupName)
		if err != nil {
			return nil, fmt.Errorf("Failed getting port group UUID for security ACL %q setup: %w", aclName, err)
		}

		if portGroupUUID == "" {
			// Load the config we'll need to create the port group with ACL rules.
			_, aclInfo, err := s.DB.Cluster.GetNetworkACL(aclProjectName, aclName)
			if err != nil {
				return nil, fmt.Errorf("Failed loading Network ACL %q: %w", aclName, err)
			}

			createACLPortGroups = append(createACLPortGroups, aclStatus{name: aclName, aclInfo: aclInfo})
		} else {
			var aclInfo *api.NetworkACL
			addACLNets := make(map[string]NetworkACLUsage)

			// Check each per-ACL-per-network port group exists.
			for _, aclNet := range aclNets {
				netPortGroupName := OVNACLNetworkPortGroupName(aclNameIDs[aclName], aclNet.ID)
				netPortGroupUUID, _, err := client.PortGroupInfo(netPortGroupName)
				if err != nil {
					return nil, fmt.Errorf("Failed getting port group UUID for security ACL %q setup: %w", aclName, err)
				}

				if netPortGroupUUID == "" {
					addACLNets[aclNet.Name] = aclNet
				}
			}

			// If we are being asked to forcefully reapply the rules, or if the port group exists but
			// doesn't have any rules, then we load the current rule set from the database to apply.
			// Note: An empty ACL list on a port group means it has only been partially setup, as
			// even Network ACLs with no rules should have at least 1 OVN ACL applied because of
			// the default rule we add. We also need to reapply the rules if we are adding any
			// new per-ACL-per-network port groups.
			if reapplyRules || !portGroupHasACLs || len(addACLNets) > 0 {
				_, aclInfo, err = s.DB.Cluster.GetNetworkACL(aclProjectName, aclName)
				if err != nil {
					return nil, fmt.Errorf("Failed loading Network ACL %q: %w", aclName, err)
				}
			}

			// Storing non-nil aclInfo in the aclStatus struct will trigger rule applying.
			existingACLPortGroups = append(existingACLPortGroups, aclStatus{name: aclName, uuid: portGroupUUID, aclInfo: aclInfo, addACLNets: addACLNets})
		}
	}

	// Build a list of referenced ACLs in the rules of ACLs we need to create.
	// We will create port groups (without ACL rules) for any missing referenced ACL OVN port groups so that
	// when we add the rules for the new ACL port groups this doesn't trigger an OVN log error about missing
	// port groups.
	referencedACLs := make(map[string]struct{}, 0)
	for _, aclStatus := range createACLPortGroups {
		ovnAddReferencedACLs(aclStatus.aclInfo, referencedACLs)
	}

	if reapplyRules {
		// Also add referenced ACLs in existing ACL rulesets if reapplying rules, as they may have changed.
		for _, aclStatus := range existingACLPortGroups {
			ovnAddReferencedACLs(aclStatus.aclInfo, referencedACLs)
		}
	}

	// Remove any references for our creation ACLs as we don't want to try and create them twice.
	for _, aclStatus := range createACLPortGroups {
		delete(referencedACLs, aclStatus.name)
	}

	// Create any missing port groups for the referenced ACLs before creating the requested ACL port groups.
	// This way the referenced port groups will exist for any rules that referenced them in the creation ACLs.
	// Note: We only create the empty port group, we do not add the ACL rules, so it is expected that any
	// future direct assignment of these referenced ACLs will trigger the ACL rules being added if needed.
	for aclName := range referencedACLs {
		portGroupName := OVNACLPortGroupName(aclNameIDs[aclName])

		// Check if port group exists.
		portGroupUUID, _, err := client.PortGroupInfo(portGroupName)
		if err != nil {
			return nil, fmt.Errorf("Failed getting port group UUID for security ACL %q setup: %w", aclName, err)
		}

		if portGroupUUID == "" {
			l.Debug("Creating empty referenced ACL OVN port group", logger.Ctx{"networkACL": aclName, "portGroup": portGroupName})

			err := client.PortGroupAdd(projectID, portGroupName, "", "")
			if err != nil {
				return nil, fmt.Errorf("Failed creating port group %q for referenced security ACL %q setup: %w", portGroupName, aclName, err)
			}

			revert.Add(func() { _ = client.PortGroupDelete(portGroupName) })
		}
	}

	// Create the needed port groups and then apply ACL rules to new port groups.
	for _, aclStatus := range createACLPortGroups {
		portGroupName := OVNACLPortGroupName(aclNameIDs[aclStatus.name])
		l.Debug("Creating ACL OVN port group", logger.Ctx{"networkACL": aclStatus.name, "portGroup": portGroupName})

		err := client.PortGroupAdd(projectID, portGroupName, "", "")
		if err != nil {
			return nil, fmt.Errorf("Failed creating port group %q for security ACL %q setup: %w", portGroupName, aclStatus.name, err)
		}

		revert.Add(func() { _ = client.PortGroupDelete(portGroupName) })

		// Create any per-ACL-per-network port groups needed.
		for _, aclNet := range aclNets {
			netPortGroupName := OVNACLNetworkPortGroupName(aclNameIDs[aclStatus.name], aclNet.ID)
			l.Debug("Creating ACL OVN network port group", logger.Ctx{"networkACL": aclStatus.name, "portGroup": netPortGroupName})

			// Create OVN network specific port group and link it to switch by adding the router port.
			err = client.PortGroupAdd(projectID, netPortGroupName, portGroupName, OVNIntSwitchName(aclNet.ID), OVNIntSwitchRouterPortName(aclNet.ID))
			if err != nil {
				return nil, fmt.Errorf("Failed creating port group %q for security ACL %q and network %q setup: %w", portGroupName, aclStatus.name, aclNet.Name, err)
			}

			revert.Add(func() { _ = client.PortGroupDelete(netPortGroupName) })
		}

		// Now apply our ACL rules to port group (and any per-ACL-per-network port groups needed).
		err = ovnApplyToPortGroup(l, client, aclStatus.aclInfo, portGroupName, aclNameIDs, aclNets, peerTargetNetIDs)
		if err != nil {
			return nil, fmt.Errorf("Failed applying ACL rules to port group %q for security ACL %q setup: %w", portGroupName, aclStatus.name, err)
		}
	}

	// Create any missing per-ACL-per-network port groups for existing ACL port groups, and apply the ACL rules
	// to them and the main ACL port group (if needed).
	for _, aclStatus := range existingACLPortGroups {
		portGroupName := OVNACLPortGroupName(aclNameIDs[aclStatus.name])

		// Create any missing per-ACL-per-network port groups.
		for _, aclNet := range aclStatus.addACLNets {
			netPortGroupName := OVNACLNetworkPortGroupName(aclNameIDs[aclStatus.name], aclNet.ID)
			l.Debug("Creating ACL OVN network port group", logger.Ctx{"networkACL": aclStatus.name, "portGroup": netPortGroupName})

			// Create OVN network specific port group and link it to switch by adding the router port.
			err := client.PortGroupAdd(projectID, netPortGroupName, portGroupName, OVNIntSwitchName(aclNet.ID), OVNIntSwitchRouterPortName(aclNet.ID))
			if err != nil {
				return nil, fmt.Errorf("Failed creating port group %q for security ACL %q and network %q setup: %w", portGroupName, aclStatus.name, aclNet.Name, err)
			}

			revert.Add(func() { _ = client.PortGroupDelete(netPortGroupName) })
		}

		// If aclInfo has been loaded, then we should use it to apply ACL rules to the existing port group
		// (and any per-ACL-per-network port groups needed).
		if aclStatus.aclInfo != nil {
			l.Debug("Applying ACL rules to OVN port group", logger.Ctx{"networkACL": aclStatus.name, "portGroup": portGroupName})

			err := ovnApplyToPortGroup(l, client, aclStatus.aclInfo, portGroupName, aclNameIDs, aclNets, peerTargetNetIDs)
			if err != nil {
				return nil, fmt.Errorf("Failed applying ACL rules to port group %q for security ACL %q setup: %w", portGroupName, aclStatus.name, err)
			}
		}
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, nil
}

// ovnAddReferencedACLs adds to the referencedACLNames any ACLs referenced by the rules in the supplied ACL.
func ovnAddReferencedACLs(info *api.NetworkACL, referencedACLNames map[string]struct{}) {
	addACLNamesFrom := func(ruleSubjects []string) {
		for _, subject := range ruleSubjects {
			_, found := referencedACLNames[subject]
			if found {
				continue // Skip subjects already seen.
			}

			if util.ValueInSlice(subject, append(ruleSubjectInternalAliases, ruleSubjectExternalAliases...)) {
				continue // Skip special reserved subjects that are not ACL names.
			}

			if validate.IsNetworkAddressCIDR(subject) == nil || validate.IsNetworkRange(subject) == nil {
				continue // Skip if the subject is an IP CIDR or IP range.
			}

			// Anything else must be a referenced ACL name.
			// Record newly seen referenced ACL into authoritative list.
			referencedACLNames[subject] = struct{}{}
		}
	}

	for _, rule := range info.Ingress {
		addACLNamesFrom(util.SplitNTrimSpace(rule.Source, ",", -1, true))
	}

	for _, rule := range info.Egress {
		addACLNamesFrom(util.SplitNTrimSpace(rule.Destination, ",", -1, true))
	}
}

// ovnApplyToPortGroup applies the rules in the specified ACL to the specified port group.
func ovnApplyToPortGroup(l logger.Logger, client *ovn.NB, aclInfo *api.NetworkACL, portGroupName ovn.OVNPortGroup, aclNameIDs map[string]int64, aclNets map[string]NetworkACLUsage, peerTargetNetIDs map[db.NetworkPeer]int64) error {
	// Create slice for port group rules that has the capacity for ingress and egress rules, plus default rule.
	portGroupRules := make([]ovn.OVNACLRule, 0, len(aclInfo.Ingress)+len(aclInfo.Egress)+1)
	networkRules := make([]ovn.OVNACLRule, 0)
	networkPeersNeeded := make([]db.NetworkPeer, 0)

	// convertACLRules converts the ACL rules to OVN ACL rules.
	convertACLRules := func(direction string, rules ...api.NetworkACLRule) error {
		for ruleIndex, rule := range rules {
			if rule.State == "disabled" {
				continue
			}

			ovnACLRule, networkSpecific, networkPeers, err := ovnRuleCriteriaToOVNACLRule(direction, &rule, portGroupName, aclNameIDs, peerTargetNetIDs)
			if err != nil {
				return err
			}

			if rule.State == "logged" {
				ovnACLRule.Log = true
				ovnACLRule.LogName = fmt.Sprintf("%s-%s-%d", portGroupName, direction, ruleIndex)
			}

			if networkSpecific {
				networkRules = append(networkRules, ovnACLRule)
			} else {
				portGroupRules = append(portGroupRules, ovnACLRule)
			}

			networkPeersNeeded = append(networkPeersNeeded, networkPeers...)
		}

		return nil
	}

	err := convertACLRules("ingress", aclInfo.Ingress...)
	if err != nil {
		return fmt.Errorf("Failed converting ACL %q ingress rules for port group %q: %w", aclInfo.Name, portGroupName, err)
	}

	err = convertACLRules("egress", aclInfo.Egress...)
	if err != nil {
		return fmt.Errorf("Failed converting ACL %q egress rules for port group %q: %w", aclInfo.Name, portGroupName, err)
	}

	// Add default rule to port group ACL.
	// This is a failsafe to drop unmatched traffic if the per-NIC default rule has unexpectedly not kicked in.
	defaultAction := "drop"
	defaultLogged := false

	portGroupRules = append(portGroupRules, ovn.OVNACLRule{
		Direction: "to-lport", // Always use this so that outport is available to Match.
		Action:    defaultAction,
		Priority:  ovnACLPriorityPortGroupDefaultAction, // Lowest priority to catch only unmatched traffic.
		Match:     fmt.Sprintf("(inport == @%s || outport == @%s)", portGroupName, portGroupName),
		Log:       defaultLogged,
		LogName:   string(portGroupName),
	})

	// Check ACL is only being applied to networks that have the required peers.
	for _, aclNet := range aclNets {
		for _, peer := range networkPeersNeeded {
			if peer.NetworkName != aclNet.Name {
				return fmt.Errorf(`ACL requiring peer "%s/%s" cannot be applied to network %q`, peer.NetworkName, peer.PeerName, aclNet.Name)
			}
		}
	}

	// Clear all existing ACL rules from port group then add the new rules to the port group.
	err = client.PortGroupSetACLRules(portGroupName, nil, portGroupRules...)
	if err != nil {
		return fmt.Errorf("Failed applying ACL %q rules to port group %q: %w", aclInfo.Name, portGroupName, err)
	}

	// Now apply the network specific rules to all networks requested (even if networkRules is empty).
	for _, aclNet := range aclNets {
		netPortGroupName := OVNACLNetworkPortGroupName(aclNameIDs[aclInfo.Name], aclNet.ID)
		l.Debug("Applying network specific ACL rules to network OVN port group", logger.Ctx{"networkACL": aclInfo.Name, "network": aclNet.Name, "portGroup": netPortGroupName})

		// Setup per-network dynamic replacements for @internal/@external subject port selectors.
		matchReplace := map[string]string{
			fmt.Sprintf("@%s", ruleSubjectInternal): fmt.Sprintf("@%s", OVNIntSwitchPortGroupName(aclNet.ID)),
			fmt.Sprintf("@%s", ruleSubjectExternal): fmt.Sprintf(`"%s"`, OVNIntSwitchRouterPortName(aclNet.ID)),
		}

		err = client.PortGroupSetACLRules(netPortGroupName, matchReplace, networkRules...)
		if err != nil {
			return fmt.Errorf("Failed applying ACL %q rules to port group %q for network %q: %w", aclInfo.Name, netPortGroupName, aclNet.Name, err)
		}
	}

	return nil
}

// ovnRuleCriteriaToOVNACLRule converts an ACL rule into an OVNACLRule for an OVN port group or network.
// Returns a bool indicating if any of the rule subjects are network specific.
func ovnRuleCriteriaToOVNACLRule(direction string, rule *api.NetworkACLRule, portGroupName ovn.OVNPortGroup, aclNameIDs map[string]int64, peerTargetNetIDs map[db.NetworkPeer]int64) (ovn.OVNACLRule, bool, []db.NetworkPeer, error) {
	networkSpecific := false
	networkPeersNeeded := make([]db.NetworkPeer, 0)
	portGroupRule := ovn.OVNACLRule{
		Direction: "to-lport", // Always use this so that outport is available to Match.
	}

	// Populate Action and Priority based on rule's Action.
	switch rule.Action {
	case "allow":
		portGroupRule.Action = "allow-related" // TODO add stateless support.
		portGroupRule.Priority = ovnACLPriorityPortGroupAllow
	case "reject":
		portGroupRule.Action = "reject"
		portGroupRule.Priority = ovnACLPriorityPortGroupReject
	case "drop":
		portGroupRule.Action = "drop"
		portGroupRule.Priority = ovnACLPriorityPortGroupDrop
	}

	var matchParts []string

	// Add directional port filter so we only apply this rule to the ports in the port group.
	switch direction {
	case "ingress":
		matchParts = []string{fmt.Sprintf("outport == @%s", portGroupName)} // Traffic going to Instance.
	case "egress":
		matchParts = []string{fmt.Sprintf("inport == @%s", portGroupName)} // Traffic leaving Instance.
	default:
		matchParts = []string{fmt.Sprintf("inport == @%s || outport == @%s", portGroupName, portGroupName)}
	}

	// Add subject filters.
	if rule.Source != "" {
		match, netSpecificMatch, networkPeers, err := ovnRuleSubjectToOVNACLMatch("src", aclNameIDs, peerTargetNetIDs, util.SplitNTrimSpace(rule.Source, ",", -1, false)...)
		if err != nil {
			return ovn.OVNACLRule{}, false, nil, err
		}

		if netSpecificMatch {
			networkSpecific = true
		}

		matchParts = append(matchParts, match)
		networkPeersNeeded = append(networkPeersNeeded, networkPeers...)
	}

	if rule.Destination != "" {
		match, netSpecificMatch, networkPeers, err := ovnRuleSubjectToOVNACLMatch("dst", aclNameIDs, peerTargetNetIDs, util.SplitNTrimSpace(rule.Destination, ",", -1, false)...)
		if err != nil {
			return ovn.OVNACLRule{}, false, nil, err
		}

		if netSpecificMatch {
			networkSpecific = true
		}

		matchParts = append(matchParts, match)
		networkPeersNeeded = append(networkPeersNeeded, networkPeers...)
	}

	// Add protocol filters.
	if util.ValueInSlice(rule.Protocol, []string{"tcp", "udp"}) {
		matchParts = append(matchParts, rule.Protocol)

		if rule.SourcePort != "" {
			matchParts = append(matchParts, ovnRulePortToOVNACLMatch(rule.Protocol, "src", util.SplitNTrimSpace(rule.SourcePort, ",", -1, false)...))
		}

		if rule.DestinationPort != "" {
			matchParts = append(matchParts, ovnRulePortToOVNACLMatch(rule.Protocol, "dst", util.SplitNTrimSpace(rule.DestinationPort, ",", -1, false)...))
		}
	} else if util.ValueInSlice(rule.Protocol, []string{"icmp4", "icmp6"}) {
		matchParts = append(matchParts, rule.Protocol)

		if rule.ICMPType != "" {
			matchParts = append(matchParts, fmt.Sprintf("%s.type == %s", rule.Protocol, rule.ICMPType))
		}

		if rule.ICMPCode != "" {
			matchParts = append(matchParts, fmt.Sprintf("%s.code == %s", rule.Protocol, rule.ICMPCode))
		}
	}

	// Populate the Match field with the generated match parts.
	portGroupRule.Match = fmt.Sprintf("(%s)", strings.Join(matchParts, ") && ("))

	return portGroupRule, networkSpecific, networkPeersNeeded, nil
}

// ovnRulePortToOVNACLMatch converts protocol (tcp/udp), direction (src/dst) and port criteria list into an OVN
// match statement.
func ovnRulePortToOVNACLMatch(protocol string, direction string, portCriteria ...string) string {
	fieldParts := make([]string, 0, len(portCriteria))

	for _, portCriterion := range portCriteria {
		criterionParts := strings.SplitN(portCriterion, "-", 2)
		if len(criterionParts) > 1 {
			fieldParts = append(fieldParts, fmt.Sprintf("(%s.%s >= %s && %s.%s <= %s)", protocol, direction, criterionParts[0], protocol, direction, criterionParts[1]))
		} else {
			fieldParts = append(fieldParts, fmt.Sprintf("%s.%s == %s", protocol, direction, criterionParts[0]))
		}
	}

	return strings.Join(fieldParts, " || ")
}

// ovnRuleSubjectToOVNACLMatch converts direction (src/dst) and subject criteria list into an OVN match statement.
// Returns a bool indicating if any of the subjects are network specific.
func ovnRuleSubjectToOVNACLMatch(direction string, aclNameIDs map[string]int64, peerTargetNetIDs map[db.NetworkPeer]int64, subjectCriteria ...string) (string, bool, []db.NetworkPeer, error) {
	fieldParts := make([]string, 0, len(subjectCriteria))
	networkSpecific := false
	networkPeersNeeded := make([]db.NetworkPeer, 0)

	// For each criterion check if value looks like an IP range or IP CIDR, and if not use it as an ACL name.
	for _, subjectCriterion := range subjectCriteria {
		if validate.IsNetworkRange(subjectCriterion) == nil {
			criterionParts := strings.SplitN(subjectCriterion, "-", 2)
			if len(criterionParts) > 1 {
				ip := net.ParseIP(criterionParts[0])
				if ip != nil {
					protocol := "ip4"
					if ip.To4() == nil {
						protocol = "ip6"
					}

					fieldParts = append(fieldParts, fmt.Sprintf("(%s.%s >= %s && %s.%s <= %s)", protocol, direction, criterionParts[0], protocol, direction, criterionParts[1]))
				}
			} else {
				return "", false, nil, fmt.Errorf("Invalid IP range %q", subjectCriterion)
			}
		} else {
			// Try parsing subject as single IP or CIDR.
			ip := net.ParseIP(subjectCriterion)
			if ip == nil {
				ip, _, _ = net.ParseCIDR(subjectCriterion)
			}

			if ip != nil {
				protocol := "ip4"
				if ip.To4() == nil {
					protocol = "ip6"
				}

				fieldParts = append(fieldParts, fmt.Sprintf("%s.%s == %s", protocol, direction, subjectCriterion))
			} else {
				// If not valid IP subnet, check if subject is ACL name or network peer name.
				var subjectPortSelector ovn.OVNPortGroup
				if util.ValueInSlice(subjectCriterion, ruleSubjectInternalAliases) {
					// Use pseudo port group name for special reserved port selector types.
					// These will be expanded later for each network specific rule.
					// Convert deprecated #internal to non-deprecated @internal if needed.
					subjectPortSelector = ovn.OVNPortGroup(ruleSubjectInternal)
					networkSpecific = true
				} else if util.ValueInSlice(subjectCriterion, ruleSubjectExternalAliases) {
					// Use pseudo port group name for special reserved port selector types.
					// These will be expanded later for each network specific rule.
					// Convert deprecated #external to non-deprecated @external if needed.
					subjectPortSelector = ovn.OVNPortGroup(ruleSubjectExternal)
					networkSpecific = true
				} else if strings.HasPrefix(subjectCriterion, "@") {
					// Subject is a network peer name. Convert to address set criteria.
					peerParts := strings.SplitN(strings.TrimPrefix(subjectCriterion, "@"), "/", 2)
					if len(peerParts) != 2 {
						return "", false, nil, fmt.Errorf("Cannot parse subject as peer %q", subjectCriterion)
					}

					peer := db.NetworkPeer{
						NetworkName: peerParts[0],
						PeerName:    peerParts[1],
					}

					networkID, found := peerTargetNetIDs[peer]
					if !found {
						return "", false, nil, fmt.Errorf("Cannot find network ID for peer %q", subjectCriterion)
					}

					addrSetPrefix := OVNIntSwitchPortGroupAddressSetPrefix(networkID)

					fieldParts = append(fieldParts, fmt.Sprintf("ip6.%s == $%s_ip6 || ip4.%s == $%s_ip4", direction, addrSetPrefix, direction, addrSetPrefix))
					networkPeersNeeded = append(networkPeersNeeded, peer)

					continue // Not a port based selector.
				} else {
					// Assume the bare name is an ACL name and convert to port group.
					aclID, found := aclNameIDs[subjectCriterion]
					if !found {
						return "", false, nil, fmt.Errorf("Cannot find security ACL ID for %q", subjectCriterion)
					}

					subjectPortSelector = OVNACLPortGroupName(aclID)
				}

				portType := "inport"
				if direction == "dst" {
					portType = "outport"
				}

				fieldParts = append(fieldParts, fmt.Sprintf("%s == @%s", portType, subjectPortSelector))
			}
		}
	}

	return strings.Join(fieldParts, " || "), networkSpecific, networkPeersNeeded, nil
}

// OVNApplyNetworkBaselineRules applies preset baseline logical switch rules to a allow access to network services.
func OVNApplyNetworkBaselineRules(client *ovn.NB, switchName ovn.OVNSwitch, routerPortName ovn.OVNSwitchPort, intRouterIPs []*net.IPNet, dnsIPs []net.IP) error {
	rules := []ovn.OVNACLRule{
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     "(arp || nd)", // Neighbour discovery.
		},
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     fmt.Sprintf(`inport == "%s" && nd_ra`, routerPortName), // IPv6 router adverts from router.
		},
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     fmt.Sprintf(`outport == "%s" && nd_rs`, routerPortName), // IPv6 router solicitation to router.
		},
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     "icmp6 && icmp6.type == 143 && ip.ttl == 1 && ip6.dst == ff02::16", // IPv6 ICMP Multicast Listener Discovery reports.
		},
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     "igmp && ip.ttl == 1 && ip4.mcast", // IPv4 IGMP.
		},
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     fmt.Sprintf(`outport == "%s" && ((ip4 && udp.dst == 67) || (ip6 && udp.dst == 547))`, routerPortName), // DHCP to router.
		},
		// These 3 rules allow packets sent by the ACL when matching a reject rule. It is very important
		// that they are allowed when no stateful rules are in use, otherwise a bug in OVN causes it to
		// enter an infinite loop rejecting its own generated reject packets, causing more to be generated,
		// and OVN will use 100% CPU.
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     "icmp6 && icmp6.type == {1,2,3,4} && ip.ttl == 255", // IPv6 ICMP error messages for ACL reject.
		},
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     "icmp4 && icmp4.type == {3,11,12} && ip.ttl == 255", // IPv4 ICMP error messages for ACL reject.
		},
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     fmt.Sprintf("tcp && tcp.flags == %#.03x", ovn.TCPRST|ovn.TCPACK), // TCP RST|ACK messages for ACL reject.
		},
	}

	// Add rules to allow ping to/from internal router IPs.
	for _, intRouterIP := range intRouterIPs {
		ipVersion := 4
		icmpPingType := 8
		icmpPingReplyType := 0
		if intRouterIP.IP.To4() == nil {
			ipVersion = 6
			icmpPingType = 128
			icmpPingReplyType = 129
		}

		rules = append(rules,
			ovn.OVNACLRule{
				Direction: "to-lport",
				Action:    "allow",
				Priority:  ovnACLPrioritySwitchAllow,
				Match:     fmt.Sprintf(`outport == "%s" && icmp%d.type == %d && ip%d.dst == %s`, routerPortName, ipVersion, icmpPingType, ipVersion, intRouterIP.IP),
			},
			ovn.OVNACLRule{
				Direction: "to-lport",
				Action:    "allow",
				Priority:  ovnACLPrioritySwitchAllow,
				Match:     fmt.Sprintf(`inport == "%s" && icmp%d.type == %d && ip%d.src == %s`, routerPortName, ipVersion, icmpPingReplyType, ipVersion, intRouterIP.IP),
			},
		)
	}

	// Add rules to allow DNS to DNS IPs.
	for _, dnsIP := range dnsIPs {
		ipVersion := 4
		if dnsIP.To4() == nil {
			ipVersion = 6
		}

		rules = append(rules,
			ovn.OVNACLRule{
				Direction: "to-lport",
				Action:    "allow",
				Priority:  ovnACLPrioritySwitchAllow,
				Match:     fmt.Sprintf(`outport == "%s" && ip%d.dst == %s && (udp.dst == 53 || tcp.dst == 53)`, routerPortName, ipVersion, dnsIP),
			},
		)
	}

	err := client.LogicalSwitchSetACLRules(switchName, rules...)
	if err != nil {
		return fmt.Errorf("Failed applying baseline ACL rules to logical switch %q: %w", switchName, err)
	}

	return nil
}

// OVNPortGroupDeleteIfUnused deletes unused port groups. Accepts optional ignoreUsageType and ignoreUsageNicName
// arguments, allowing the used by logic to ignore an instance/profile NIC or network (useful if config not
// applied to database yet). Also accepts optional list of ACLs to explicitly consider in use by OVN.
// The combination of ignoring the specifified usage type and explicit keep ACLs allows the caller to ensure that
// the desired ACLs are considered unused by the usage type even if the referring config has not yet been removed
// from the database.
func OVNPortGroupDeleteIfUnused(s *state.State, l logger.Logger, client *ovn.NB, aclProjectName string, ignoreUsageType any, ignoreUsageNicName string, keepACLs ...string) error {
	// Get map of ACL names to DB IDs (used for generating OVN port group names).
	aclNameIDs, err := s.DB.Cluster.GetNetworkACLIDsByNames(aclProjectName)
	if err != nil {
		return fmt.Errorf("Failed getting network ACL IDs for security ACL port group removal: %w", err)
	}

	// Convert aclNameIDs to aclNames slice for use with UsedBy.
	aclNames := make([]string, 0, len(aclNameIDs))
	for aclName := range aclNameIDs {
		aclNames = append(aclNames, aclName)
	}

	// Get project ID.
	var projectID int64
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectID, err = cluster.GetProjectID(ctx, tx.Tx(), aclProjectName)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed getting project ID for project %q: %w", aclProjectName, err)
	}

	// Get list of OVN port groups associated to this project.
	portGroups, err := client.PortGroupListByProject(projectID)
	if err != nil {
		return fmt.Errorf("Failed getting port groups for project %q: %w", aclProjectName, err)
	}

	// hasKeeperPrefix indicates if the port group provided matches the prefix of one of the keepACLs.
	// This will include ACL network port groups too.
	hasKeeperPrefix := func(portGroup ovn.OVNPortGroup) bool {
		for _, keepACLName := range keepACLs {
			keepACLPortGroup := OVNACLPortGroupName(aclNameIDs[keepACLName])
			if strings.HasPrefix(string(portGroup), string(keepACLPortGroup)) {
				return true
			}
		}

		return false
	}

	// Filter project port group list by ACL related ones, and store them in a map keyed by port group name.
	// This contains the initial candidates for removal. But any found to be in use will be removed from list.
	removeACLPortGroups := make(map[ovn.OVNPortGroup]struct{}, 0)
	for _, portGroup := range portGroups {
		// If port group is related to an ACL and is not related to one of keepACLs, then add it as a
		// candidate for removal.
		if strings.HasPrefix(string(portGroup), ovnACLPortGroupPrefix) && !hasKeeperPrefix(portGroup) {
			removeACLPortGroups[portGroup] = struct{}{}
		}
	}

	// Add keepACLs to ovnUsedACLs to indicate they are explicitly in use by OVN. This is important because it
	// also ensures that indirectly referred ACLs in the rulesets of these ACLs will also be kept even if not
	// found to be in use in the database yet.
	ovnUsedACLs := make(map[string]struct{}, len(keepACLs))
	for _, keepACLName := range keepACLs {
		ovnUsedACLs[keepACLName] = struct{}{}
	}

	// Map to record ACLs being referenced by other ACLs. Need to check later if they are in use with OVN ACLs.
	aclUsedACLS := make(map[string][]string, 0)

	// Find alls ACLs that are either directly referred to by OVN entities (networks, instance/profile NICs)
	// or indirectly by being referred to by a ruleset of another ACL that is itself in use by OVN entities.
	// For the indirectly referred to ACLs, store a list of the ACLs that are referring to it.
	err = UsedBy(s, aclProjectName, func(matchedACLNames []string, usageType any, nicName string, nicConfig map[string]string) error {
		switch u := usageType.(type) {
		case db.InstanceArgs:
			ignoreInst, isIgnoreInst := ignoreUsageType.(instance.Instance)

			if isIgnoreInst && ignoreUsageNicName == "" {
				return fmt.Errorf("ignoreUsageNicName should be specified when providing an instance in ignoreUsageType")
			}

			// If an ignore instance was provided, then skip the device that the ACLs were just removed
			// from. In case DB record is not updated until the update process has completed otherwise
			// we would still consider it using the ACL.
			if isIgnoreInst && ignoreInst.Name() == u.Name && ignoreInst.Project().Name == u.Project && ignoreUsageNicName == nicName {
				return nil
			}

			netID, network, _, err := s.DB.Cluster.GetNetworkInAnyState(aclProjectName, nicConfig["network"])
			if err != nil {
				return fmt.Errorf("Failed to load network %q: %w", nicConfig["network"], err)
			}

			if network.Type == "ovn" {
				for _, matchedACLName := range matchedACLNames {
					ovnUsedACLs[matchedACLName] = struct{}{} // Record as in use by OVN entity.

					// Delete entries (if exist) for ACL and per-ACL-per-network port groups.
					delete(removeACLPortGroups, OVNACLPortGroupName(aclNameIDs[matchedACLName]))
					delete(removeACLPortGroups, OVNACLNetworkPortGroupName(aclNameIDs[matchedACLName], netID))
				}
			}
		case *api.Network:
			ignoreNet, isIgnoreNet := ignoreUsageType.(*api.Network)

			if isIgnoreNet && ignoreUsageNicName != "" {
				return fmt.Errorf("ignoreUsageNicName should be empty when providing a network in ignoreUsageType")
			}

			// If an ignore network was provided, then skip the network that the ACLs were just removed
			// from. In case DB record is not updated until the update process has completed otherwise
			// we would still consider it using the ACL.
			if isIgnoreNet && ignoreNet.Name == u.Name {
				return nil
			}

			if u.Type == "ovn" {
				netID, _, _, err := s.DB.Cluster.GetNetworkInAnyState(aclProjectName, u.Name)
				if err != nil {
					return fmt.Errorf("Failed to load network %q: %w", nicConfig["network"], err)
				}

				for _, matchedACLName := range matchedACLNames {
					ovnUsedACLs[matchedACLName] = struct{}{} // Record as in use by OVN entity.

					// Delete entries (if exist) for ACL and per-ACL-per-network port groups.
					delete(removeACLPortGroups, OVNACLPortGroupName(aclNameIDs[matchedACLName]))
					delete(removeACLPortGroups, OVNACLNetworkPortGroupName(aclNameIDs[matchedACLName], netID))
				}
			}
		case cluster.Profile:
			ignoreProfile, isIgnoreProfile := ignoreUsageType.(cluster.Profile)

			if isIgnoreProfile && ignoreUsageNicName == "" {
				return fmt.Errorf("ignoreUsageNicName should be specified when providing a profile in ignoreUsageType")
			}

			// If an ignore profile was provided, then skip the device that the ACLs were just removed
			// from. In case DB record is not updated until the update process has completed otherwise
			// we would still consider it using the ACL.
			if isIgnoreProfile && ignoreProfile.Name == u.Name && ignoreProfile.Project == u.Project && ignoreUsageNicName == nicName {
				return nil
			}

			netID, network, _, err := s.DB.Cluster.GetNetworkInAnyState(aclProjectName, nicConfig["network"])
			if err != nil {
				return fmt.Errorf("Failed to load network %q: %w", nicConfig["network"], err)
			}

			if network.Type == "ovn" {
				for _, matchedACLName := range matchedACLNames {
					ovnUsedACLs[matchedACLName] = struct{}{} // Record as in use by OVN entity.

					// Delete entries (if exist) for ACL and per-ACL-per-network port groups.
					delete(removeACLPortGroups, OVNACLPortGroupName(aclNameIDs[matchedACLName]))
					delete(removeACLPortGroups, OVNACLNetworkPortGroupName(aclNameIDs[matchedACLName], netID))
				}
			}
		case *api.NetworkACL:
			// Record which ACLs this ACL's ruleset refers to.
			for _, matchedACLName := range matchedACLNames {
				if aclUsedACLS[matchedACLName] == nil {
					aclUsedACLS[matchedACLName] = make([]string, 0, 1)
				}

				if !util.ValueInSlice(u.Name, aclUsedACLS[matchedACLName]) {
					// Record as in use by another ACL entity.
					aclUsedACLS[matchedACLName] = append(aclUsedACLS[matchedACLName], u.Name)
				}
			}
		default:
			return fmt.Errorf("Unrecognised usage type %T", u)
		}

		return nil
	}, aclNames...)
	if err != nil && err != db.ErrInstanceListStop {
		return fmt.Errorf("Failed getting ACL usage: %w", err)
	}

	// usedByOvn checks if any of the aclNames are in use by an OVN entity (network or instance/profile NIC).
	usedByOvn := func(aclNames ...string) bool {
		for _, aclName := range aclNames {
			_, found := ovnUsedACLs[aclName]
			if found {
				return true
			}
		}

		return false
	}

	// Check each ACL referenced in the rulesets of other ACLs whether any of the ACLs they were referenced
	// from are in use by ACLs that are also being used by OVN. If not then we don't need to keep the
	// referenced port group in OVN.
	for aclName, refACLs := range aclUsedACLS {
		if usedByOvn(refACLs...) {
			// Delete entry (if exists) for ACL port group.
			delete(removeACLPortGroups, OVNACLPortGroupName(aclNameIDs[aclName]))
		}
	}

	// Now remove any remaining port groups left in removeACLPortGroups.
	removePortGroups := make([]ovn.OVNPortGroup, 0, len(removeACLPortGroups))
	for removeACLPortGroup := range removeACLPortGroups {
		removePortGroups = append(removePortGroups, removeACLPortGroup)
		l.Debug("Scheduled deletion of unused ACL OVN port group", logger.Ctx{"portGroup": removeACLPortGroup})
	}

	if len(removePortGroups) > 0 {
		err = client.PortGroupDelete(removePortGroups...)
		if err != nil {
			return fmt.Errorf("Failed to delete unused OVN port groups: %w", err)
		}
	}

	return nil
}

// OVNPortGroupInstanceNICSchedule adds the specified NIC port to the specified port groups in the changeSet.
func OVNPortGroupInstanceNICSchedule(portUUID ovn.OVNSwitchPortUUID, changeSet map[ovn.OVNPortGroup][]ovn.OVNSwitchPortUUID, portGroups ...ovn.OVNPortGroup) {
	for _, portGroupName := range portGroups {
		_, found := changeSet[portGroupName]
		if !found {
			changeSet[portGroupName] = []ovn.OVNSwitchPortUUID{}
		}

		changeSet[portGroupName] = append(changeSet[portGroupName], portUUID)
	}
}

// OVNApplyInstanceNICDefaultRules applies instance NIC default rules to per-network port group.
func OVNApplyInstanceNICDefaultRules(client *ovn.NB, switchPortGroup ovn.OVNPortGroup, logPrefix string, nicPortName ovn.OVNSwitchPort, ingressAction string, ingressLogged bool, egressAction string, egressLogged bool) error {
	if !util.ValueInSlice(ingressAction, ValidActions) {
		return fmt.Errorf("Invalid ingress action %q", ingressAction)
	}

	if !util.ValueInSlice(egressAction, ValidActions) {
		return fmt.Errorf("Invalid egress action %q", egressAction)
	}

	rules := []ovn.OVNACLRule{
		{
			Direction: "to-lport",
			Action:    egressAction,
			Log:       egressLogged,
			LogName:   fmt.Sprintf("%s-egress", logPrefix), // Max 63 chars.
			Priority:  ovnACLPriorityNICDefaultActionEgress,
			Match:     fmt.Sprintf(`inport == "%s"`, nicPortName), // From NIC.
		},
		{
			Direction: "to-lport",
			Action:    ingressAction,
			Log:       ingressLogged,
			LogName:   fmt.Sprintf("%s-ingress", logPrefix), // Max 63 chars.
			Priority:  ovnACLPriorityNICDefaultActionIngress,
			Match:     fmt.Sprintf(`outport == "%s"`, nicPortName), // To NIC.
		},
	}

	err := client.PortGroupPortSetACLRules(switchPortGroup, nicPortName, rules...)
	if err != nil {
		return fmt.Errorf("Failed applying instance NIC default ACL rules for port %q: %w", nicPortName, err)
	}

	return nil
}

// ovnLogEntry is the type used for the JSON encoded entries on the log endpoint (when coming from OVN).
type ovnLogEntry struct {
	Time     string `json:"time"`
	Proto    string `json:"proto"`
	Src      string `json:"src"`
	Dst      string `json:"dst"`
	SrcPort  string `json:"src_port,omitempty"`
	DstPort  string `json:"dst_port,omitempty"`
	ICMPType string `json:"icmp_type,omitempty"`
	ICMPCode string `json:"icmp_code,omitempty"`
	Action   string `json:"action"`
}

// ovnParseLogEntry takes a log line and expected ACL prefix and returns a re-formated log entry if matching.
func ovnParseLogEntry(input string, prefix string) string {
	fields := strings.Split(input, "|")

	// Skip unknown formatting.
	if len(fields) != 5 {
		return ""
	}

	// We only care about ACLs.
	if !strings.HasPrefix(fields[2], "acl_log") {
		return ""
	}

	// Parse the ACL log entry.
	aclEntry := map[string]string{}
	for _, entry := range util.SplitNTrimSpace(fields[4], ",", -1, true) {
		pair := strings.Split(entry, "=")
		if len(pair) != 2 {
			continue
		}

		aclEntry[strings.Trim(pair[0], "\"")] = strings.Trim(pair[1], "\"")
	}

	// Filter for our ACL.
	if !strings.HasPrefix(aclEntry["name"], prefix) {
		return ""
	}

	// Parse the timestamp.
	logTime, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		return ""
	}

	// Get the protocol.
	severityFields := strings.Split(aclEntry["severity"], " ")
	if len(severityFields) != 2 {
		return ""
	}

	protocol := severityFields[1]

	// Get the source and destination addresses.
	srcAddr, ok := aclEntry["nw_src"]
	if !ok {
		srcAddr, ok = aclEntry["ipv6_src"]
		if !ok {
			return ""
		}
	}

	dstAddr, ok := aclEntry["nw_dst"]
	if !ok {
		dstAddr, ok = aclEntry["ipv6_dst"]
		if !ok {
			return ""
		}
	}

	// Prepare the core log entry.
	newEntry := ovnLogEntry{
		Time:     logTime.UTC().Format(time.RFC3339),
		Proto:    protocol,
		Src:      srcAddr,
		Dst:      dstAddr,
		ICMPType: aclEntry["icmp_type"],
		ICMPCode: aclEntry["icmp_code"],
		Action:   aclEntry["verdict"],
	}

	// Add the source and destination ports.
	srcPort, ok := aclEntry["tp_src"]
	if ok {
		newEntry.SrcPort = srcPort
	}

	dstPort, ok := aclEntry["tp_dst"]
	if ok {
		newEntry.DstPort = dstPort
	}

	out, err := json.Marshal(&newEntry)
	if err != nil {
		return ""
	}

	return string(out)
}
