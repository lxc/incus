package acl

import (
	"context"
	"fmt"

	"github.com/lxc/incus/internal/server/db"
	firewallDrivers "github.com/lxc/incus/internal/server/firewall/drivers"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
)

// FirewallApplyACLRules applies ACL rules to network firewall.
func FirewallApplyACLRules(s *state.State, logger logger.Logger, aclProjectName string, aclNet NetworkACLUsage) error {
	var dropRules []firewallDrivers.ACLRule
	var rejectRules []firewallDrivers.ACLRule
	var allowRules []firewallDrivers.ACLRule

	// convertACLRules converts the ACL rules to Firewall ACL rules.
	convertACLRules := func(direction string, logPrefix string, rules ...api.NetworkACLRule) error {
		for ruleIndex, rule := range rules {
			if rule.State == "disabled" {
				continue
			}

			firewallACLRule := firewallDrivers.ACLRule{
				Direction:       direction,
				Action:          rule.Action,
				Source:          rule.Source,
				Destination:     rule.Destination,
				Protocol:        rule.Protocol,
				SourcePort:      rule.SourcePort,
				DestinationPort: rule.DestinationPort,
				ICMPType:        rule.ICMPType,
				ICMPCode:        rule.ICMPCode,
			}

			if rule.State == "logged" {
				firewallACLRule.Log = true
				// Max 29 chars.
				firewallACLRule.LogName = fmt.Sprintf("%s-%s-%d", logPrefix, direction, ruleIndex)
			}

			switch {
			case rule.Action == "drop":
				dropRules = append(dropRules, firewallACLRule)
			case rule.Action == "reject":
				rejectRules = append(rejectRules, firewallACLRule)
			case rule.Action == "allow":
				allowRules = append(allowRules, firewallACLRule)
			default:
				return fmt.Errorf("Unrecognised action %q", rule.Action)
			}
		}

		return nil
	}

	logPrefix := aclNet.Name

	// Load ACLs specified by network.
	for _, aclName := range util.SplitNTrimSpace(aclNet.Config["security.acls"], ",", -1, true) {
		var aclInfo *api.NetworkACL

		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error

			_, aclInfo, err = tx.GetNetworkACL(ctx, aclProjectName, aclName)

			return err
		})
		if err != nil {
			return fmt.Errorf("Failed loading ACL %q for network %q: %w", aclName, aclNet.Name, err)
		}

		err = convertACLRules("ingress", logPrefix, aclInfo.Ingress...)
		if err != nil {
			return fmt.Errorf("Failed converting ACL %q ingress rules for network %q: %w", aclInfo.Name, aclNet.Name, err)
		}

		err = convertACLRules("egress", logPrefix, aclInfo.Egress...)
		if err != nil {
			return fmt.Errorf("Failed converting ACL %q egress rules for network %q: %w", aclInfo.Name, aclNet.Name, err)
		}
	}

	var rules []firewallDrivers.ACLRule
	rules = append(rules, dropRules...)
	rules = append(rules, rejectRules...)
	rules = append(rules, allowRules...)

	// Add the automatic default ACL rule for the network.
	egressAction, egressLogged := firewallACLDefaults(aclNet.Config, "egress")
	ingressAction, ingressLogged := firewallACLDefaults(aclNet.Config, "ingress")

	rules = append(rules, firewallDrivers.ACLRule{
		Direction: "egress",
		Action:    egressAction,
		Log:       egressLogged,
		LogName:   fmt.Sprintf("%s-egress", logPrefix),
	})

	rules = append(rules, firewallDrivers.ACLRule{
		Direction: "ingress",
		Action:    ingressAction,
		Log:       ingressLogged,
		LogName:   fmt.Sprintf("%s-ingress", logPrefix),
	})

	return s.Firewall.NetworkApplyACLRules(aclNet.Name, rules)
}

// firewallACLDefaults returns the action and logging mode to use for the specified direction's default rule.
// If the security.acls.default.{in,e}gress.action or security.acls.default.{in,e}gress.logged settings are not
// specified in the network config, then it returns "reject" and false respectively.
func firewallACLDefaults(netConfig map[string]string, direction string) (string, bool) {
	defaults := map[string]string{
		fmt.Sprintf("security.acls.default.%s.action", direction): "reject",
		fmt.Sprintf("security.acls.default.%s.logged", direction): "false",
	}

	for k := range defaults {
		if netConfig[k] != "" {
			defaults[k] = netConfig[k]
		}
	}

	return defaults[fmt.Sprintf("security.acls.default.%s.action", direction)], util.IsTrue(defaults[fmt.Sprintf("security.acls.default.%s.logged", direction)])
}
