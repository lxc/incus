package drivers

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

// iptablesChainNICFilterPrefix chain prefix used for NIC specific filtering rules.
const iptablesChainNICFilterPrefix = "incus_nic"

// iptablesChainACLFilterPrefix chain used for ACL specific filtering rules.
const iptablesChainACLFilterPrefix = "incus_acl"

// iptablesCommentPrefix is used to prefix the rule comment.
const iptablesCommentPrefix = "generated for"

// ebtablesMu used for locking concurrent operations against ebtables.
// As its own locking mechanism isn't always available.
var ebtablesMu sync.Mutex

// Xtables is an implmentation of Incus firewall using {ip, ip6, eb}tables.
type Xtables struct{}

// String returns the driver name.
func (d Xtables) String() string {
	return "xtables"
}

// Compat returns whether the driver backend is in use, and any host compatibility errors.
func (d Xtables) Compat() (bool, error) {
	// xtables commands can be powered by nftables, so check we are using non-nft version first, otherwise
	// we should be using the nftables driver instead.
	cmds := []string{"iptables", "ip6tables", "ebtables"}
	for _, cmd := range cmds {
		// Check command exists.
		_, err := exec.LookPath(cmd)
		if err != nil {
			return false, fmt.Errorf("Backend command %q missing", cmd)
		}

		// Check whether it is an nftables shim.
		if d.xtablesIsNftables(cmd) {
			return false, fmt.Errorf("Backend command %q is an nftables shim", cmd)
		}
	}

	// Check whether any of the backends are in use already.
	if d.iptablesInUse("iptables") {
		logger.Debug("Firewall xtables detected iptables is in use")
		return true, nil
	}

	if d.iptablesInUse("ip6tables") {
		logger.Debug("Firewall xtables detected ip6tables is in use")
		return true, nil
	}

	if d.ebtablesInUse() {
		logger.Debug("Firewall xtables detected ebtables is in use")
		return true, nil
	}

	return false, nil
}

// xtablesIsNftables checks whether the specified xtables backend command is actually an nftables shim.
func (d Xtables) xtablesIsNftables(cmd string) bool {
	output, err := subprocess.RunCommandCLocale(cmd, "--version")
	if err != nil {
		return false
	}

	if strings.Contains(output, "nf_tables") {
		return true
	}

	return false
}

// iptablesInUse returns whether the specified iptables backend command has any rules defined.
func (d Xtables) iptablesInUse(iptablesCmd string) bool {
	// tableIsUse checks an individual iptables table for active rules. We do this rather than using the
	// iptables-save command because we cannot guarantee that this command is available and don't want mixed
	// behaviour when iptables command is an nft shim and the iptables-save command is legacy.
	tableIsUse := func(table string) bool {
		cmd := exec.Command(iptablesCmd, "-S", "-t", table)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return false
		}

		err = cmd.Start()
		if err != nil {
			return false
		}

		defer func() { _ = cmd.Wait() }()

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()

			// Check for lines that indicate a rule being used.
			if strings.HasPrefix(line, "-A") || strings.HasPrefix(line, "-R") || strings.HasPrefix(line, "-I") {
				_ = cmd.Process.Kill()
				return true
			}
		}

		return false
	}

	for _, table := range []string{"filter", "nat", "mangle", "raw"} {
		if tableIsUse(table) {
			return true
		}
	}

	return false
}

// ebtablesInUse returns whether the ebtables backend command has any rules defined.
func (d Xtables) ebtablesInUse() bool {
	ebtablesMu.Lock()
	defer ebtablesMu.Unlock()

	cmd := exec.Command("ebtables", "-L", "--Lmac2", "--Lx")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false
	}

	err = cmd.Start()
	if err != nil {
		return false
	}

	defer func() { _ = cmd.Wait() }()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		return true
	}

	return false
}

// networkIPTablesComment returns the iptables comment that is added to each network related rule.
func (d Xtables) networkIPTablesComment(networkName string) string {
	return fmt.Sprintf("Incus network %s", networkName)
}

// networkForwardIPTablesComment returns the iptables comment that is added to each network forward related rule.
func (d Xtables) networkForwardIPTablesComment(networkName string) string {
	return fmt.Sprintf("Incus network-forward %s", networkName)
}

// networkSetupNICFilteringChain creates the NIC filtering chain if it doesn't exist, and adds the jump rules to
// the INPUT and FORWARD filter chains. Must be called after networkSetupForwardingPolicy so that the rules are
// prepended before the default fowarding policy rules.
func (d Xtables) networkSetupNICFilteringChain(networkName string, ipVersion uint) error {
	chain := fmt.Sprintf("%s_%s", iptablesChainNICFilterPrefix, networkName)

	// Create the NIC filter chain if it doesn't exist.
	exists, _, err := d.iptablesChainExists(ipVersion, "filter", chain)
	if err != nil {
		return err
	}

	if !exists {
		err = d.iptablesChainCreate(ipVersion, "filter", chain)
		if err != nil {
			return err
		}
	}

	comment := d.networkIPTablesComment(networkName)
	err = d.iptablesPrepend(ipVersion, comment, "filter", "INPUT", "-i", networkName, "-j", chain)
	if err != nil {
		return err
	}

	err = d.iptablesPrepend(ipVersion, comment, "filter", "FORWARD", "-i", networkName, "-j", chain)
	if err != nil {
		return err
	}

	return nil
}

// networkSetupACLFilteringChains creates any missing ACL chains and adds jump rules.
func (d Xtables) networkSetupACLFilteringChains(networkName string) error {
	chain := fmt.Sprintf("%s_%s", iptablesChainACLFilterPrefix, networkName)

	for _, ipVersion := range []uint{4, 6} {
		// Create the ACL filter chain if it doesn't exist.
		exists, _, err := d.iptablesChainExists(ipVersion, "filter", chain)
		if err != nil {
			return err
		}

		if !exists {
			err = d.iptablesChainCreate(ipVersion, "filter", chain)
			if err != nil {
				return err
			}
		}

		// Prepend jump rules for ACL candidate traffic.
		comment := d.networkIPTablesComment(networkName)
		err = d.iptablesPrepend(ipVersion, comment, "filter", "INPUT", "-i", networkName, "-j", chain)
		if err != nil {
			return err
		}

		err = d.iptablesPrepend(ipVersion, comment, "filter", "OUTPUT", "-o", networkName, "-j", chain)
		if err != nil {
			return err
		}

		// Prepend baseline services rules for network.
		// Unlike OVN networks, we add the rules first before the ACL candidate rules, aa we can't
		// indentify "INPUT" and "OUTPUT" chain traffic once we have jumped into the ACL chain. At this
		// point it becomes indistinguishable from FORWARD traffic. So unlike OVN an ACL rule cannot be
		// used to block baseline service traffic.

		// Allow DNS to Incus host.
		err = d.iptablesPrepend(ipVersion, comment, "filter", "INPUT", "-i", networkName, "-p", "tcp", "--dport", "53", "-j", "ACCEPT")
		if err != nil {
			return err
		}

		err = d.iptablesPrepend(ipVersion, comment, "filter", "INPUT", "-i", networkName, "-p", "udp", "--dport", "53", "-j", "ACCEPT")
		if err != nil {
			return err
		}

		if ipVersion == 4 {
			// Allow DHCPv4 to/from Incus host.
			err = d.iptablesPrepend(ipVersion, comment, "filter", "INPUT", "-i", networkName, "-p", "udp", "--sport", "68", "--dport", "67", "-j", "ACCEPT")
			if err != nil {
				return err
			}

			err = d.iptablesPrepend(ipVersion, comment, "filter", "OUTPUT", "-o", networkName, "-p", "udp", "--sport", "67", "--dport", "68", "-j", "ACCEPT")
			if err != nil {
				return err
			}

			// Allow core ICMPv4 to/from Incus host.
			for _, icmpType := range []int{3, 11, 12} {
				err = d.iptablesPrepend(ipVersion, comment, "filter", "INPUT", "-i", networkName, "-p", "icmp", "-m", "icmp", "--icmp-type", fmt.Sprintf("%d", icmpType), "-j", "ACCEPT")
				if err != nil {
					return err
				}

				err = d.iptablesPrepend(ipVersion, comment, "filter", "OUTPUT", "-o", networkName, "-p", "icmp", "-m", "icmp", "--icmp-type", fmt.Sprintf("%d", icmpType), "-j", "ACCEPT")
				if err != nil {
					return err
				}
			}
		}

		if ipVersion == 6 {
			// Allow DHCPv6 to/from Incus host.
			err = d.iptablesPrepend(ipVersion, comment, "filter", "INPUT", "-i", networkName, "-p", "udp", "--sport", "546", "--dport", "547", "-j", "ACCEPT")
			if err != nil {
				return err
			}

			err = d.iptablesPrepend(ipVersion, comment, "filter", "OUTPUT", "-o", networkName, "-p", "udp", "--sport", "547", "--dport", "546", "-j", "ACCEPT")
			if err != nil {
				return err
			}

			// Allow core ICMPv6 to/from Incus host.
			for _, icmpType := range []int{1, 2, 3, 4, 133, 135, 136, 143} {
				err = d.iptablesPrepend(ipVersion, comment, "filter", "INPUT", "-i", networkName, "-p", "icmpv6", "-m", "icmp6", "--icmpv6-type", fmt.Sprintf("%d", icmpType), "-j", "ACCEPT")
				if err != nil {
					return err
				}
			}

			// Allow ICMPv6 ping from host into network as dnsmasq uses this to probe IP allocations.
			for _, icmpType := range []int{1, 2, 3, 4, 128, 134, 135, 136, 143} {
				err = d.iptablesPrepend(ipVersion, comment, "filter", "OUTPUT", "-o", networkName, "-p", "icmpv6", "-m", "icmp6", "--icmpv6-type", fmt.Sprintf("%d", icmpType), "-j", "ACCEPT")
				if err != nil {
					return err
				}
			}
		}

		// Only consider traffic forwarding through the host, as opposed to traffic forwarding through the
		// bridge when br_netfilter is enabled. In this case the input/output interface is the same.
		err = d.iptablesPrepend(ipVersion, comment, "filter", "FORWARD", "-i", networkName, "!", "-o", networkName, "-j", chain)
		if err != nil {
			return err
		}

		err = d.iptablesPrepend(ipVersion, comment, "filter", "FORWARD", "-o", networkName, "!", "-i", networkName, "-j", chain)
		if err != nil {
			return err
		}
	}

	return nil
}

// networkSetupForwardingPolicy allows forwarding dependent on boolean argument. Must be called before
// networkSetupNICFilteringChains so the default forwarding policy rules are processed after NIC filtering rules.
func (d Xtables) networkSetupForwardingPolicy(networkName string, ipVersion uint, allow bool) error {
	forwardType := "REJECT"
	if allow {
		forwardType = "ACCEPT"
	}

	comment := d.networkIPTablesComment(networkName)
	err := d.iptablesPrepend(ipVersion, comment, "filter", "FORWARD", "-i", networkName, "-j", forwardType)
	if err != nil {
		return err
	}

	err = d.iptablesPrepend(ipVersion, comment, "filter", "FORWARD", "-o", networkName, "-j", forwardType)

	if err != nil {
		return err
	}

	return nil
}

// networkSetupOutboundNAT configures outbound NAT.
// If srcIP is non-nil then SNAT is used with the specified address, otherwise MASQUERADE mode is used.
func (d Xtables) networkSetupOutboundNAT(networkName string, subnet *net.IPNet, srcIP net.IP, appendRule bool) error {
	family := uint(4)
	if subnet.IP.To4() == nil {
		family = 6
	}

	args := []string{
		"-s", subnet.String(),
		"!", "-d", subnet.String(),
	}

	// If SNAT IP not supplied then use the IP of the outbound interface (MASQUERADE).
	if srcIP == nil {
		args = append(args, "-j", "MASQUERADE")
	} else {
		args = append(args, "-j", "SNAT", "--to", srcIP.String())
	}

	comment := d.networkIPTablesComment(networkName)

	if appendRule {
		err := d.iptablesAppend(family, comment, "nat", "POSTROUTING", args...)
		if err != nil {
			return err
		}
	} else {
		err := d.iptablesPrepend(family, comment, "nat", "POSTROUTING", args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// networkSetupICMPDHCPDNSAccess sets up basic iptables overrides for ICMP, DHCP and DNS.
func (d Xtables) networkSetupICMPDHCPDNSAccess(networkName string, ipVersion uint) error {
	var rules [][]string
	if ipVersion == 4 {
		rules = [][]string{
			{"4", networkName, "filter", "INPUT", "-i", networkName, "-p", "udp", "--dport", "67", "-j", "ACCEPT"},
			{"4", networkName, "filter", "INPUT", "-i", networkName, "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
			{"4", networkName, "filter", "INPUT", "-i", networkName, "-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
			{"4", networkName, "filter", "OUTPUT", "-o", networkName, "-p", "udp", "--sport", "67", "-j", "ACCEPT"},
			{"4", networkName, "filter", "OUTPUT", "-o", networkName, "-p", "udp", "--sport", "53", "-j", "ACCEPT"},
			{"4", networkName, "filter", "OUTPUT", "-o", networkName, "-p", "tcp", "--sport", "53", "-j", "ACCEPT"}}

		// Allow core ICMPv4 to/from Incus host.
		for _, icmpType := range []int{3, 11, 12} {
			rules = append(rules, []string{"4", networkName, "filter", "INPUT", "-i", networkName, "-p", "icmp", "-m", "icmp", "--icmp-type", fmt.Sprintf("%d", icmpType), "-j", "ACCEPT"})
			rules = append(rules, []string{"4", networkName, "filter", "OUTPUT", "-o", networkName, "-p", "icmp", "-m", "icmp", "--icmp-type", fmt.Sprintf("%d", icmpType), "-j", "ACCEPT"})
		}
	} else if ipVersion == 6 {
		rules = [][]string{
			{"6", networkName, "filter", "INPUT", "-i", networkName, "-p", "udp", "--dport", "547", "-j", "ACCEPT"},
			{"6", networkName, "filter", "INPUT", "-i", networkName, "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
			{"6", networkName, "filter", "INPUT", "-i", networkName, "-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
			{"6", networkName, "filter", "OUTPUT", "-o", networkName, "-p", "udp", "--sport", "547", "-j", "ACCEPT"},
			{"6", networkName, "filter", "OUTPUT", "-o", networkName, "-p", "udp", "--sport", "53", "-j", "ACCEPT"},
			{"6", networkName, "filter", "OUTPUT", "-o", networkName, "-p", "tcp", "--sport", "53", "-j", "ACCEPT"}}

		// Allow core ICMPv6 to/from Incus host.
		for _, icmpType := range []int{1, 2, 3, 4, 133, 135, 136, 143} {
			rules = append(rules, []string{"6", networkName, "filter", "INPUT", "-i", networkName, "-p", "icmpv6", "-m", "icmp6", "--icmpv6-type", fmt.Sprintf("%d", icmpType), "-j", "ACCEPT"})
		}

		// Allow ICMPv6 ping from host into network as dnsmasq uses this to probe IP allocations.
		for _, icmpType := range []int{1, 2, 3, 4, 128, 134, 135, 136, 143} {
			rules = append(rules, []string{"6", networkName, "filter", "OUTPUT", "-o", networkName, "-p", "icmpv6", "-m", "icmp6", "--icmpv6-type", fmt.Sprintf("%d", icmpType), "-j", "ACCEPT"})
		}
	} else {
		return fmt.Errorf("Invalid IP version")
	}

	comment := d.networkIPTablesComment(networkName)

	for _, rule := range rules {
		ipVersion, err := strconv.ParseUint(rule[0], 10, 0)
		if err != nil {
			return err
		}

		err = d.iptablesPrepend(uint(ipVersion), comment, rule[2], rule[3], rule[4:]...)
		if err != nil {
			return err
		}
	}

	return nil
}

// networkSetupDHCPv4Checksum attempts a workaround for broken DHCP clients.
func (d Xtables) networkSetupDHCPv4Checksum(networkName string) error {
	comment := d.networkIPTablesComment(networkName)
	return d.iptablesPrepend(4, comment, "mangle", "POSTROUTING", "-o", networkName, "-p", "udp", "--dport", "68", "-j", "CHECKSUM", "--checksum-fill")
}

// NetworkSetup configure network firewall.
func (d Xtables) NetworkSetup(networkName string, opts Opts) error {
	if opts.SNATV4 != nil {
		err := d.networkSetupOutboundNAT(networkName, opts.SNATV4.Subnet, opts.SNATV4.SNATAddress, opts.SNATV4.Append)
		if err != nil {
			return err
		}
	}

	if opts.SNATV6 != nil {
		err := d.networkSetupOutboundNAT(networkName, opts.SNATV6.Subnet, opts.SNATV6.SNATAddress, opts.SNATV6.Append)
		if err != nil {
			return err
		}
	}

	if opts.FeaturesV4 != nil {
		if opts.FeaturesV4.ICMPDHCPDNSAccess {
			err := d.networkSetupICMPDHCPDNSAccess(networkName, 4)
			if err != nil {
				return err
			}

			err = d.networkSetupDHCPv4Checksum(networkName)
			if err != nil {
				return err
			}
		}

		err := d.networkSetupForwardingPolicy(networkName, 4, opts.FeaturesV4.ForwardingAllow)
		if err != nil {
			return err
		}
	}

	if opts.FeaturesV6 != nil {
		if opts.FeaturesV6.ICMPDHCPDNSAccess {
			err := d.networkSetupICMPDHCPDNSAccess(networkName, 6)
			if err != nil {
				return err
			}
		}

		err := d.networkSetupForwardingPolicy(networkName, 6, opts.FeaturesV6.ForwardingAllow)
		if err != nil {
			return err
		}
	}

	if opts.ACL {
		// Needs to be after networkSetupForwardingPolicy but before networkSetupNICFilteringChain.
		err := d.networkSetupACLFilteringChains(networkName)
		if err != nil {
			return err
		}
	}

	if opts.FeaturesV6 != nil {
		// Setup NIC filtering chain. This must come after networkSetupForwardingPolicy so that the jump
		// rules prepended to the INPUT and FORWARD chains are processed before the default forwarding
		// policy rules.
		err := d.networkSetupNICFilteringChain(networkName, 6)
		if err != nil {
			return err
		}
	}

	return nil
}

// NetworkApplyACLRules applies ACL rules to the existing firewall chains.
func (d Xtables) NetworkApplyACLRules(networkName string, rules []ACLRule) error {
	chain := fmt.Sprintf("%s_%s", iptablesChainACLFilterPrefix, networkName)

	// Parse rules for both IP families before applying either family of rules.
	iptCmdRules := make(map[string][][]string)
	for _, ipVersion := range []uint{4, 6} {
		cmd := "iptables"
		if ipVersion == 6 {
			cmd = "ip6tables"
		}

		iptRules := make([][]string, 0)
		for _, rule := range rules {
			actionArgs, logArgs, err := d.aclRuleCriteriaToArgs(networkName, ipVersion, &rule)
			if err != nil {
				return err
			}

			if actionArgs == nil {
				continue // Rule is not appropriate for ipVersion.
			}

			if logArgs != nil {
				iptRules = append(iptRules, logArgs)
			}

			iptRules = append(iptRules, actionArgs)
		}

		iptCmdRules[cmd] = iptRules
	}

	applyACLRules := func(cmd string, iptRules [][]string) error {
		// Attempt to flush chain in table.
		_, err := subprocess.RunCommand(cmd, "-w", "-t", "filter", "-F", chain)
		if err != nil {
			return fmt.Errorf("Failed flushing %q chain %q in table %q: %w", cmd, chain, "filter", err)
		}

		// Allow connection tracking.
		_, err = subprocess.RunCommand(cmd, "-w", "-t", "filter", "-A", chain, "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT")
		if err != nil {
			return fmt.Errorf("Failed adding connection tracking rules to %q chain %q in table %q: %w", cmd, chain, "filter", err)
		}

		// Add rules to chain in table.
		for _, iptRule := range iptRules {
			_, err := subprocess.RunCommand(cmd, append([]string{"-w", "-t", "filter", "-A", chain}, iptRule...)...)
			if err != nil {
				return fmt.Errorf("Failed adding rule to %q chain %q in table %q: %w", cmd, chain, "filter", err)
			}
		}

		return nil
	}

	// Apply each family of rules.
	for cmd, rules := range iptCmdRules {
		err := applyACLRules(cmd, rules)
		if err != nil {
			return err
		}
	}

	return nil
}

// aclRuleCriteriaToArgs converts an ACL rule into an set of arguments for an xtables rule.
// Returns the arguments to use for the action command and separately the arguments for logging if enabled.
// Returns nil arguments if the rule is not appropriate for the ipVersion.
func (d Xtables) aclRuleCriteriaToArgs(networkName string, ipVersion uint, rule *ACLRule) ([]string, []string, error) {
	var args []string

	if rule.Direction == "ingress" {
		args = append(args, "-o", networkName) // Coming from host into network's interface.
	} else {
		args = append(args, "-i", networkName) // Coming from network's interface into host.
	}

	// Add subject filters.
	if rule.Source != "" {
		matchArgs, err := d.aclRuleSubjectToACLMatch("source", ipVersion, util.SplitNTrimSpace(rule.Source, ",", -1, false)...)
		if err != nil {
			return nil, nil, err
		}

		if matchArgs == nil {
			return nil, nil, nil // Rule is not appropriate for ipVersion.
		}

		args = append(args, matchArgs...)
	}

	if rule.Destination != "" {
		matchArgs, err := d.aclRuleSubjectToACLMatch("destination", ipVersion, util.SplitNTrimSpace(rule.Destination, ",", -1, false)...)
		if err != nil {
			return nil, nil, err
		}

		if matchArgs == nil {
			return nil, nil, nil // Rule is not appropriate for ipVersion.
		}

		args = append(args, matchArgs...)
	}

	// Add protocol filters.
	if util.ValueInSlice(rule.Protocol, []string{"tcp", "udp"}) {
		args = append(args, "-p", rule.Protocol)

		if rule.SourcePort != "" {
			args = append(args, d.aclRulePortToACLMatch("sports", util.SplitNTrimSpace(rule.SourcePort, ",", -1, false)...)...)
		}

		if rule.DestinationPort != "" {
			args = append(args, d.aclRulePortToACLMatch("dports", util.SplitNTrimSpace(rule.DestinationPort, ",", -1, false)...)...)
		}
	} else if util.ValueInSlice(rule.Protocol, []string{"icmp4", "icmp6"}) {
		var icmpIPVersion uint
		var protoName string
		var extName string

		switch rule.Protocol {
		case "icmp4":
			protoName = "icmp"
			extName = "icmp"
			icmpIPVersion = 4
		case "icmp6":
			protoName = "icmpv6"
			extName = "icmp6"
			icmpIPVersion = 6
		}

		if ipVersion != icmpIPVersion {
			// If we got this far it means that source/destination are either empty or are filled
			// with at least some subjects in the same family as ipVersion. So if the icmpIPVersion
			// doesn't match the ipVersion then it means the rule contains mixed-version subjects
			// which is invalid when using an IP version specific ICMP protocol.
			if rule.Source != "" || rule.Destination != "" {
				return nil, nil, fmt.Errorf("Invalid use of %q protocol with non-IPv%d source/destination criteria", rule.Protocol, ipVersion)
			}

			// Otherwise it means this is just a blanket ICMP rule and is only appropriate for use
			// with the corresponding ipVersion xtables command.
			return nil, nil, nil // Rule is not appropriate for ipVersion.
		}

		if rule.ICMPCode != "" && rule.ICMPType == "" {
			return nil, nil, fmt.Errorf("Invalid use of ICMP code without ICMP type")
		}

		args = append(args, "-p", protoName)

		if rule.ICMPType != "" {
			args = append(args, "-m", extName)

			if rule.ICMPCode == "" {
				args = append(args, fmt.Sprintf("--%s-type", protoName), rule.ICMPType)
			} else {
				args = append(args, fmt.Sprintf("--%s-type", protoName), fmt.Sprintf("%s/%s", rule.ICMPType, rule.ICMPCode))
			}
		}
	}

	// Handle action.
	action := rule.Action
	if action == "allow" {
		action = "accept"
	}

	actionArgs := append(args, "-j", strings.ToUpper(action))

	// Handle logging.
	var logArgs []string
	if rule.Log {
		logArgs = append(args, "-j", "LOG")

		if rule.LogName != "" {
			// Add a trailing space to prefix for readability in logs.
			logArgs = append(logArgs, "--log-prefix", fmt.Sprintf("%s ", rule.LogName))
		}
	}

	return actionArgs, logArgs, nil
}

// aclRuleSubjectToACLMatch converts direction (source/destination) and subject criteria list into xtables args.
// Returns nil if none of the subjects are appropriate for the ipVersion.
func (d Xtables) aclRuleSubjectToACLMatch(direction string, ipVersion uint, subjectCriteria ...string) ([]string, error) {
	fieldParts := make([]string, 0, len(subjectCriteria))

	// For each criterion check if value looks like IP CIDR.
	for _, subjectCriterion := range subjectCriteria {
		ip := net.ParseIP(subjectCriterion)
		if ip == nil {
			ip, _, _ = net.ParseCIDR(subjectCriterion)
		}

		if ip != nil {
			var subjectIPVersion uint = 4
			if ip.To4() == nil {
				subjectIPVersion = 6
			}

			if ipVersion != subjectIPVersion {
				continue // Skip subjects that not for the xtables tool we are using.
			}

			fieldParts = append(fieldParts, subjectCriterion)
		} else {
			return nil, fmt.Errorf("Unsupported xtables subject %q", subjectCriterion)
		}
	}

	if len(fieldParts) > 0 {
		return []string{fmt.Sprintf("--%s", direction), strings.Join(fieldParts, ",")}, nil
	}

	return nil, nil // No subjects suitable for ipVersion.
}

// aclRulePortToACLMatch converts protocol (tcp/udp), direction (sports/dports) and port criteria list into
// xtables args.
func (d Xtables) aclRulePortToACLMatch(direction string, portCriteria ...string) []string {
	fieldParts := make([]string, 0, len(portCriteria))

	for _, portCriterion := range portCriteria {
		criterionParts := strings.SplitN(portCriterion, "-", 2)
		if len(criterionParts) > 1 {
			fieldParts = append(fieldParts, fmt.Sprintf("%s:%s", criterionParts[0], criterionParts[1]))
		} else {
			fieldParts = append(fieldParts, criterionParts[0])
		}
	}

	return []string{"-m", "multiport", fmt.Sprintf("--%s", direction), strings.Join(fieldParts, ",")}
}

// NetworkClear removes network rules from filter, mangle and nat tables.
// If delete is true then network-specific chains are also removed.
func (d Xtables) NetworkClear(networkName string, delete bool, ipVersions []uint) error {
	comments := []string{
		d.networkIPTablesComment(networkName),
		d.networkForwardIPTablesComment(networkName),
	}

	for _, ipVersion := range ipVersions {
		// Clear any rules associated to the network and network address forwards.
		err := d.iptablesClear(ipVersion, comments, "filter", "mangle", "nat")
		if err != nil {
			return err
		}

		// Remove ACL chain and rules.
		aclFilterChain := fmt.Sprintf("%s_%s", iptablesChainACLFilterPrefix, networkName)
		exists, hasRules, err := d.iptablesChainExists(ipVersion, "filter", aclFilterChain)
		if err != nil {
			return err
		}

		if exists {
			err = d.iptablesChainDelete(ipVersion, "filter", aclFilterChain, hasRules)
			if err != nil {
				return err
			}
		}

		// Remove network specific chains (and any rules in them) if deleting.
		if delete {
			// Remove the NIC filter chain if it exists.
			nicFilterChain := fmt.Sprintf("%s_%s", iptablesChainNICFilterPrefix, networkName)
			exists, hasRules, err := d.iptablesChainExists(ipVersion, "filter", nicFilterChain)
			if err != nil {
				return err
			}

			if exists {
				err = d.iptablesChainDelete(ipVersion, "filter", nicFilterChain, hasRules)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// instanceDeviceIPTablesComment returns the iptables comment that is added to each instance device related rule.
func (d Xtables) instanceDeviceIPTablesComment(projectName string, instanceName string, deviceName string) string {
	return fmt.Sprintf("Incus container %s (%s)", project.Instance(projectName, instanceName), deviceName)
}

// InstanceSetupBridgeFilter sets up the filter rules to apply bridged device IP filtering.
// If the parent bridge is managed by Incus then parentManaged argument should be true so that the rules added can
// use the iptablesChainACLFilterPrefix chain. If not they are added to the main filter chains directly (which only
// works for unmanaged bridges because those don't support ACLs).
func (d Xtables) InstanceSetupBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4Nets []*net.IPNet, IPv6Nets []*net.IPNet, parentManaged bool) error {
	comment := d.instanceDeviceIPTablesComment(projectName, instanceName, deviceName)

	rules := d.generateFilterEbtablesRules(hostName, hwAddr, IPv4Nets, IPv6Nets)

	ebtablesMu.Lock()
	for _, rule := range rules {
		_, err := subprocess.RunCommand(rule[0], rule[1:]...)
		if err != nil {
			ebtablesMu.Unlock()
			return err
		}
	}
	ebtablesMu.Unlock()

	rules, err := d.generateFilterIptablesRules(parentName, hostName, hwAddr, IPv6Nets, parentManaged)
	if err != nil {
		return err
	}

	for _, rule := range rules {
		ipVersion, err := strconv.ParseUint(rule[0], 10, 0)
		if err != nil {
			return err
		}

		err = d.iptablesPrepend(uint(ipVersion), comment, "filter", rule[1], rule[2:]...)
		if err != nil {
			return err
		}
	}

	return nil
}

// InstanceClearBridgeFilter removes any filter rules that were added to apply bridged device IP filtering.
func (d Xtables) InstanceClearBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4Nets []*net.IPNet, IPv6Nets []*net.IPNet) error {
	comment := d.instanceDeviceIPTablesComment(projectName, instanceName, deviceName)

	// Get a list of rules that we would have applied on instance start.
	rules := d.generateFilterEbtablesRules(hostName, hwAddr, IPv4Nets, IPv6Nets)

	ebtablesMu.Lock()

	// Get a current list of rules active on the host.
	out, err := subprocess.RunCommand("ebtables", "-L", "--Lmac2", "--Lx")
	if err != nil {
		ebtablesMu.Unlock()
		return fmt.Errorf("Failed to get a list of network filters to for %q: %w", deviceName, err)
	}

	errs := []error{}
	// Iterate through each active rule on the host and try and match it to one the Incus rules.
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		fieldsLen := len(fields)

		for _, rule := range rules {
			// Rule doesn't match if the field lengths aren't the same, move on.
			if len(rule) != fieldsLen {
				continue
			}

			// Check whether active rule matches one of our rules to delete.
			if !d.matchEbtablesRule(fields, rule, true) {
				continue
			}

			// If we get this far, then the current host rule matches one of our Incus
			// rules, so we should run the modified command to delete it.
			_, err = subprocess.RunCommand(fields[0], fields[1:]...)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	ebtablesMu.Unlock()

	// Remove any ip6tables rules added as part of bridge filtering.
	err = d.iptablesClear(6, []string{comment}, "filter")
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("Failed to remove network filters rule for %q: %v", deviceName, errs)
	}

	return nil
}

// InstanceSetupProxyNAT creates DNAT rules for proxy devices.
func (d Xtables) InstanceSetupProxyNAT(projectName string, instanceName string, deviceName string, forward *AddressForward) error {
	if forward.ListenAddress == nil {
		return fmt.Errorf("Listen address is required")
	}

	if forward.TargetAddress == nil {
		return fmt.Errorf("Target address is required")
	}

	listenPortsLen := len(forward.ListenPorts)
	if listenPortsLen <= 0 {
		return fmt.Errorf("At least 1 listen port must be supplied")
	}

	// If multiple target ports supplied, check they match the listen port(s) count.
	targetPortsLen := len(forward.TargetPorts)
	if targetPortsLen != 1 && targetPortsLen != listenPortsLen {
		return fmt.Errorf("Mismatch between listen port(s) and target port(s) count")
	}

	ipVersion := uint(4)
	if forward.ListenAddress.To4() == nil {
		ipVersion = 6
	}

	listenAddressStr := forward.ListenAddress.String()
	targetAddressStr := forward.TargetAddress.String()

	revert := revert.New()
	defer revert.Fail()
	revert.Add(func() { _ = d.InstanceClearProxyNAT(projectName, instanceName, deviceName) })

	comment := d.instanceDeviceIPTablesComment(projectName, instanceName, deviceName)

	targetPortRanges := portRangesFromSlice(forward.TargetPorts)
	for _, targetPortRange := range targetPortRanges {
		targetPortRangeStr := portRangeStr(targetPortRange, ":")

		// Apply MASQUERADE rule for each target range.
		// instance <-> instance.
		// Requires instance's bridge port has hairpin mode enabled when br_netfilter is loaded.
		err := d.iptablesPrepend(ipVersion, comment, "nat", "POSTROUTING", "-p", forward.Protocol, "--source", targetAddressStr, "--destination", targetAddressStr, "--dport", targetPortRangeStr, "-j", "MASQUERADE")
		if err != nil {
			return err
		}
	}

	dnatRanges := getOptimisedDNATRanges(forward)
	for listenPortRange, targetPortRange := range dnatRanges {
		listenPortRangeStr := portRangeStr(listenPortRange, ":")
		targetDest := targetAddressStr

		if targetPortRange[1] == 1 {
			targetPortStr := portRangeStr(targetPortRange, ":")
			targetDest = fmt.Sprintf("%s:%s", targetAddressStr, targetPortStr)
			if ipVersion == 6 {
				targetDest = fmt.Sprintf("[%s]:%s", targetAddressStr, targetPortStr)
			}
		}

		// outbound <-> instance.
		err := d.iptablesPrepend(ipVersion, comment, "nat", "PREROUTING", "-p", forward.Protocol, "--destination", listenAddressStr, "--dport", listenPortRangeStr, "-j", "DNAT", "--to-destination", targetDest)
		if err != nil {
			return err
		}

		// host <-> instance.
		err = d.iptablesPrepend(ipVersion, comment, "nat", "OUTPUT", "-p", forward.Protocol, "--destination", listenAddressStr, "--dport", listenPortRangeStr, "-j", "DNAT", "--to-destination", targetDest)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// InstanceClearProxyNAT remove DNAT rules for proxy devices.
func (d Xtables) InstanceClearProxyNAT(projectName string, instanceName string, deviceName string) error {
	comment := d.instanceDeviceIPTablesComment(projectName, instanceName, deviceName)
	errs := []error{}

	for _, ipVersion := range []uint{4, 6} {
		err := d.iptablesClear(ipVersion, []string{comment}, "nat")
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("Failed to remove proxy NAT rules for %q: %v", deviceName, errs)
	}

	return nil
}

// generateFilterEbtablesRules returns a customised set of ebtables filter rules based on the device.
func (d Xtables) generateFilterEbtablesRules(hostName string, hwAddr string, IPv4Nets []*net.IPNet, IPv6Nets []*net.IPNet) [][]string {
	// MAC source filtering rules. Block any packet coming from instance with an incorrect Ethernet source MAC.
	// This is required for IP filtering too.
	rules := [][]string{
		{"ebtables", "-t", "filter", "-A", "INPUT", "-s", "!", hwAddr, "-i", hostName, "-j", "DROP"},
		{"ebtables", "-t", "filter", "-A", "FORWARD", "-s", "!", hwAddr, "-i", hostName, "-j", "DROP"},
	}

	// Don't write any firewall rules when IPv4Nets == nil (i.e. allow all traffic)
	if IPv4Nets != nil {
		if len(IPv4Nets) > 0 {
			// Only apply these rules if there are allowed subnets, since all traffic would otherwise be blocked anyway.
			rules = append(rules,
				// Prevent ARP MAC spoofing (prevents the instance poisoning the ARP cache of its neighbours with a MAC address that isn't its own).
				[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "ARP", "-i", hostName, "--arp-mac-src", "!", hwAddr, "-j", "DROP"},
				[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "ARP", "-i", hostName, "--arp-mac-src", "!", hwAddr, "-j", "DROP"},
				// Allow DHCPv4 to the host only. This must come before the IP source filtering rules below.
				[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv4", "-s", hwAddr, "-i", hostName, "--ip-src", "0.0.0.0", "--ip-dst", "255.255.255.255", "--ip-proto", "udp", "--ip-dport", "67", "-j", "ACCEPT"},
			)

			// Apply exceptions to these networks. These exceptions must be applied before all IPv4 and ARP traffic is blocked below.
			for _, IPv4Net := range IPv4Nets {
				rules = append(rules,
					// Allow ARP IP redirection (allows the instance to redirect traffic for IPs in the range).
					[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "ARP", "-i", hostName, "--arp-ip-src", fmt.Sprintf("%s/%s", IPv4Net.IP.String(), subnetMask(IPv4Net)), "-j", "ACCEPT"},
					[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "ARP", "-i", hostName, "--arp-ip-src", fmt.Sprintf("%s/%s", IPv4Net.IP.String(), subnetMask(IPv4Net)), "-j", "ACCEPT"},
					// IP source filtering rules. Allows any packet coming from instance with a correct IP source address.
					[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv4", "-i", hostName, "--ip-src", fmt.Sprintf("%s/%s", IPv4Net.IP.String(), subnetMask(IPv4Net)), "-j", "ACCEPT"},
					[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "IPv4", "-i", hostName, "--ip-src", fmt.Sprintf("%s/%s", IPv4Net.IP.String(), subnetMask(IPv4Net)), "-j", "ACCEPT"},
				)
			}
		}

		// Block any remaining traffic.
		rules = append(rules,
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "ARP", "-i", hostName, "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "ARP", "-i", hostName, "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv4", "-i", hostName, "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "IPv4", "-i", hostName, "-j", "DROP"},
		)
	} else {
		rules = append(rules,
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv4", "-i", hostName, "-j", "ACCEPT"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "IPv4", "-i", hostName, "-j", "ACCEPT"},
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "ARP", "-i", hostName, "-j", "ACCEPT"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "ARP", "-i", hostName, "-j", "ACCEPT"},
		)
	}

	if IPv6Nets != nil {
		if len(IPv6Nets) > 0 {
			rules = append(rules,
				// Allow DHCPv6 and Router Solicitation to the host only. This must come before the IP source filtering rules below.
				[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-s", hwAddr, "-i", hostName, "--ip6-src", "fe80::/ffc0::", "--ip6-dst", "ff02::1:2/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "--ip6-proto", "udp", "--ip6-dport", "547", "-j", "ACCEPT"},
				[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-s", hwAddr, "-i", hostName, "--ip6-src", "fe80::/ffc0::", "--ip6-dst", "ff02::2/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "--ip6-proto", "ipv6-icmp", "--ip6-icmp-type", "router-solicitation", "-j", "ACCEPT"},
				// Block any IPv6 router advertisement packets from instance.
				[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-i", hostName, "--ip6-proto", "ipv6-icmp", "--ip6-icmp-type", "router-advertisement", "-j", "DROP"},
				[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "IPv6", "-i", hostName, "--ip6-proto", "ipv6-icmp", "--ip6-icmp-type", "router-advertisement", "-j", "DROP"},
			)
			for _, IPv6Net := range IPv6Nets {
				rules = append(rules,
					// IP source filtering rules. Allows any packet coming from instance with a correct IP source address.
					[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-i", hostName, "--ip6-src", fmt.Sprintf("%s/%s", IPv6Net.IP.String(), subnetMask(IPv6Net)), "-j", "ACCEPT"},
					[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "IPv6", "-i", hostName, "--ip6-src", fmt.Sprintf("%s/%s", IPv6Net.IP.String(), subnetMask(IPv6Net)), "-j", "ACCEPT"},
				)
			}
		}

		// Block any remaining traffic
		rules = append(rules,
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-i", hostName, "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "IPv6", "-i", hostName, "-j", "DROP"},
		)
	} else {
		rules = append(rules,
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-i", hostName, "-j", "ACCEPT"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "IPv6", "-i", hostName, "-j", "ACCEPT"},
		)
	}

	if len(IPv4Nets)+len(IPv6Nets) > 0 {
		// Filter unwanted ethernet frames when using IP filtering.
		rules = append(rules, []string{"ebtables", "-t", "filter", "-A", "INPUT", "-i", hostName, "-j", "DROP"})
		rules = append(rules, []string{"ebtables", "-t", "filter", "-A", "FORWARD", "-i", hostName, "-j", "DROP"})
	}

	return rules
}

// generateFilterIptablesRules returns a customised set of iptables filter rules based on the device.
// If parentManaged is true then the rules are added to the iptablesChainACLFilterPrefix chain whereas if its false
// then the rules are added to both the INPUT and FORWARD chains (so that no additional NIC chain is required, as
// there's no managed network setup step available to create it and add jump rules).
//
// IMPORTANT NOTE: These rules are generated in reverse order and should only be used in combination with iptablesPrepend.
func (d Xtables) generateFilterIptablesRules(parentName string, hostName string, hwAddr string, IPv6Nets []*net.IPNet, parentManaged bool) (rules [][]string, err error) {
	mac, err := net.ParseMAC(hwAddr)
	if err != nil {
		return
	}

	macHex := hex.EncodeToString(mac)

	// These rules below are implemented using ip6tables because the functionality to inspect
	// the contents of an ICMPv6 packet does not exist in ebtables (unlike for IPv4 ARP).
	// Additionally, ip6tables doesn't really provide a nice way to do what we need here, so we
	// have resorted to doing a raw hex comparison of the packet contents at fixed positions.
	// If these rules are not added then it is possible to hijack traffic for another IP that is
	// not assigned to the instance by sending a specially crafted gratuitous NDP packet with
	// correct source address and MAC at the IP & ethernet layers, but a fraudulent IP or MAC
	// inside the ICMPv6 NDP packet.
	if IPv6Nets != nil {
		var chains []string

		if parentManaged {
			// Managed networks should have setup the iptablesChainNICFilterPrefix chain and added the
			// jump rules to INPUT and FORWARD already, so reduce the overhead of adding the rules to
			// both chains and just add it to the iptablesChainNICFilterPrefix chain instead.
			chains = append(chains, fmt.Sprintf("%s_%s", iptablesChainNICFilterPrefix, parentName))
		} else {
			// We add the NIC rules to both the INPUT and FORWARD chain as there is no managed network
			// setup step that could have created the iptablesChainNICFilterPrefix chain and added the
			// necessary jump rules to INPUT and FORWARD chains to make them work.
			chains = append(chains, "INPUT", "FORWARD")
		}

		for _, chain := range chains {
			// Prevent Neighbor Advertisement IP spoofing (prevents the instance redirecting traffic for IPs that are not its own).
			rules = append(rules,
				[]string{"6", chain, "-i", parentName, "-p", "ipv6-icmp", "-m", "physdev", "--physdev-in", hostName, "-m", "icmp6", "--icmpv6-type", "136", "-j", "DROP"},
			)

			for _, IPv6Net := range IPv6Nets {
				hexPrefix, err := subnetPrefixHex(IPv6Net)
				if err != nil {
					return nil, err
				}

				rules = append(rules,
					// Prevent Neighbor Advertisement IP spoofing (prevents the instance redirecting traffic for IPs that are not its own).
					[]string{"6", chain, "-i", parentName, "-p", "ipv6-icmp", "-m", "physdev", "--physdev-in", hostName, "-m", "icmp6", "--icmpv6-type", "136", "-m", "string", "--hex-string", fmt.Sprintf("|%s|", hexPrefix), "--algo", "bm", "--from", "48", "--to", strconv.Itoa(48 + len(hexPrefix)/2), "-j", "ACCEPT"},
				)
			}

			if len(IPv6Nets) > 0 {
				rules = append(rules,
					// Prevent Neighbor Advertisement MAC spoofing (prevents the instance poisoning the NDP cache of its neighbours with a MAC address that isn't its own).
					[]string{"6", chain, "-i", parentName, "-p", "ipv6-icmp", "-m", "physdev", "--physdev-in", hostName, "-m", "icmp6", "--icmpv6-type", "136", "-m", "string", "!", "--hex-string", fmt.Sprintf("|%s|", macHex), "--algo", "bm", "--from", "66", "--to", "72", "-j", "DROP"},
				)
			}
		}
	}

	return
}

// matchEbtablesRule compares an active rule to a supplied match rule to see if they match.
// If deleteMode is true then the "-A" flag in the active rule will be modified to "-D" and will
// not be part of the equality match. This allows delete commands to be generated from dumped add commands.
func (d Xtables) matchEbtablesRule(activeRule []string, matchRule []string, deleteMode bool) bool {
	for i := range matchRule {
		// Active rules will be dumped in "add" format, we need to detect
		// this and switch it to "delete" mode if requested. If this has already been
		// done then move on, as we don't want to break the comparison below.
		if deleteMode && (activeRule[i] == "-A" || activeRule[i] == "-D") {
			activeRule[i] = "-D"
			continue
		}

		active := activeRule[i]
		match := matchRule[i]

		// When adding a rule ebtables expects subnets to be of the form "<IP address>/<subnet mask>".
		// However when active rules are printed, subnets are expressed in CIDR format.
		// Additionally, if the subnet has a full mask (i.e. it is IP address, e.g. 10.0.0.0/32) then
		// ebtables will omit the range and just print an IP. For example, a generated subnet "198.0.2.0/255.255.255.0"
		// should match the active subnet "198.0.2.0/24", and a generated subnet "198.0.2.1/255.255.255.255" should match
		// the active IP address "198.0.2.1".
		//
		// First, check that the match is a subnet and that the IPs are the same.
		matchIPMaskStr := strings.SplitN(match, "/", 2)
		if len(matchIPMaskStr) == 2 && matchIPMaskStr[0] == strings.Split(active, "/")[0] {
			// If the active subnet is a CIDR string we have a match if the masks are identical.
			_, activeIPNet, err := net.ParseCIDR(active)
			if err == nil {
				return subnetMask(activeIPNet) == matchIPMaskStr[1]
			}

			// If the active subnet is a single IP then we have a match if the generated mask is a full mask.
			activeIP := net.ParseIP(active)
			if activeIP != nil {
				if activeIP.To4() != nil {
					return matchIPMaskStr[1] == "255.255.255.255"
				}

				return matchIPMaskStr[1] == "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"
			}
		}

		// Check the match rule field matches the active rule field.
		// If they don't match, then this isn't one of our rules.
		if active != match {
			return false
		}
	}

	return true
}

// iptablesAdd adds an iptables rule.
func (d Xtables) iptablesAdd(ipVersion uint, comment string, table string, method string, chain string, rule ...string) error {
	var cmd string
	if ipVersion == 4 {
		cmd = "iptables"
	} else if ipVersion == 6 {
		cmd = "ip6tables"
	} else {
		return fmt.Errorf("Invalid IP version")
	}

	_, err := exec.LookPath(cmd)
	if err != nil {
		return fmt.Errorf("Asked to setup IPv%d firewalling but %s can't be found", ipVersion, cmd)
	}

	args := []string{"-w", "-t", table, method, chain}
	args = append(args, rule...)
	args = append(args, "-m", "comment", "--comment", fmt.Sprintf("%s %s", iptablesCommentPrefix, comment))

	_, err = subprocess.TryRunCommand(cmd, args...)
	if err != nil {
		return err
	}

	return nil
}

// iptablesAppend appends an iptables rule.
func (d Xtables) iptablesAppend(ipVersion uint, comment string, table string, chain string, rule ...string) error {
	return d.iptablesAdd(ipVersion, comment, table, "-A", chain, rule...)
}

// iptablesPrepend prepends an iptables rule.
func (d Xtables) iptablesPrepend(ipVersion uint, comment string, table string, chain string, rule ...string) error {
	return d.iptablesAdd(ipVersion, comment, table, "-I", chain, rule...)
}

// iptablesClear clears iptables rules matching the supplied comment in the specified tables.
func (d Xtables) iptablesClear(ipVersion uint, comments []string, fromTables ...string) error {
	var cmd string
	var tablesFile string
	if ipVersion == 4 {
		cmd = "iptables"
		tablesFile = "/proc/self/net/ip_tables_names"
	} else if ipVersion == 6 {
		cmd = "ip6tables"
		tablesFile = "/proc/self/net/ip6_tables_names"
	} else {
		return fmt.Errorf("Invalid IP version")
	}

	// Detect kernels that lack IPv6 support.
	if !util.PathExists("/proc/sys/net/ipv6") && ipVersion == 6 {
		return nil
	}

	// Check command exists.
	_, err := exec.LookPath(cmd)
	if err != nil {
		return nil
	}

	// Check which tables exist.
	var tables []string // Uninitialised slice indicates we haven't opened the table file yet.
	file, err := os.Open(tablesFile)
	if err != nil {
		logger.Warnf("Failed getting list of tables from %q, assuming all requested tables exist", tablesFile)
	} else {
		tables = []string{} // Initialise the tables slice indcating we were able to open the tables file.
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			tables = append(tables, scanner.Text())
		}

		_ = file.Close()
	}

	for _, fromTable := range fromTables {
		if tables != nil && !util.ValueInSlice(fromTable, tables) {
			// If we successfully opened the tables file, and the requested table is not present,
			// then skip trying to get a list of rules from that table.
			continue
		}

		baseArgs := []string{"-w", "-t", fromTable}

		// List the rules.
		args := append(baseArgs, "--list-rules")
		output, err := subprocess.TryRunCommand(cmd, args...)
		if err != nil {
			return fmt.Errorf("Failed to list IPv%d rules (table %s)", ipVersion, fromTable)
		}

		for _, line := range strings.Split(output, "\n") {
			for _, comment := range comments {
				if !strings.Contains(line, fmt.Sprintf("%s %s", iptablesCommentPrefix, comment)) {
					continue
				}

				// Remove the entry.
				fields := strings.Fields(line)
				fields[0] = "-D"

				args = append(baseArgs, fields...)
				_, err = subprocess.TryRunCommand("sh", "-c", fmt.Sprintf("%s %s", cmd, strings.Join(args, " ")))
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// InstanceSetupRPFilter activates reverse path filtering for the specified instance device on the host interface.
func (d Xtables) InstanceSetupRPFilter(projectName string, instanceName string, deviceName string, hostName string) error {
	comment := fmt.Sprintf("%s rpfilter", d.instanceDeviceIPTablesComment(projectName, instanceName, deviceName))
	args := []string{
		"-m", "rpfilter",
		"--invert",
		"-i", hostName,
		"-j", "DROP",
	}

	// IPv4 filter.
	err := d.iptablesPrepend(4, comment, "raw", "PREROUTING", args...)
	if err != nil {
		return err
	}

	// IPv6 filter if IPv6 is enabled.
	if util.PathExists("/proc/sys/net/ipv6") {
		err = d.iptablesPrepend(6, comment, "raw", "PREROUTING", args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// InstanceClearRPFilter removes reverse path filtering for the specified instance device on the host interface.
func (d Xtables) InstanceClearRPFilter(projectName string, instanceName string, deviceName string) error {
	comment := fmt.Sprintf("%s rpfilter", d.instanceDeviceIPTablesComment(projectName, instanceName, deviceName))
	errs := []error{}

	for _, ipVersion := range []uint{4, 6} {
		err := d.iptablesClear(ipVersion, []string{comment}, "raw")
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("Failed to remove reverse path filter rules for %q: %v", deviceName, errs)
	}

	return nil
}

// InstanceSetupNetPrio activates setting of skb->priority for the specified instance device on the host interface.
func (d Xtables) InstanceSetupNetPrio(projectName string, instanceName string, deviceName string, netPrio uint32) error {
	comment := fmt.Sprintf("%s netprio", d.instanceDeviceIPTablesComment(projectName, instanceName, deviceName))
	class := fmt.Sprintf("%x:%x", uint16(uint32(netPrio)>>16), uint16(uint32(netPrio)&0xFFFF))
	args := []string{
		"-i", deviceName,
		"-j", "CLASSIFY",
		"--set-class", class,
	}

	// IPv4 filter.
	err := d.iptablesPrepend(4, comment, "mangle", "FORWARD", args...)
	if err != nil {
		return err
	}

	// IPv6 filter if IPv6 is enabled.
	if util.PathExists("/proc/sys/net/ipv6") {
		err = d.iptablesPrepend(6, comment, "mangle", "FORWARD", args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// InstanceClearNetPrio removes setting of skb->priority for the specified instance device on the host interface.
func (d Xtables) InstanceClearNetPrio(projectName string, instanceName string, deviceName string) error {
	if deviceName == "" {
		return fmt.Errorf("Failed clearing netprio rules for instance %q in project %q: device name is empty", projectName, instanceName)
	}

	comment := fmt.Sprintf("%s netprio", d.instanceDeviceIPTablesComment(projectName, instanceName, deviceName))
	errs := []error{}

	for _, ipVersion := range []uint{4, 6} {
		err := d.iptablesClear(ipVersion, []string{comment}, "mangle")
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("Failed to remove netprio rules for %q: %v", deviceName, errs)
	}

	return nil
}

// iptablesChainExists checks whether a chain exists in a table, and whether it has any rules.
func (d Xtables) iptablesChainExists(ipVersion uint, table string, chain string) (bool, bool, error) {
	var cmd string
	if ipVersion == 4 {
		cmd = "iptables"
	} else if ipVersion == 6 {
		cmd = "ip6tables"
	} else {
		return false, false, fmt.Errorf("Invalid IP version")
	}

	_, err := exec.LookPath(cmd)
	if err != nil {
		return false, false, fmt.Errorf("Failed checking %q chain %q exists in table %q: %w", cmd, chain, table, err)
	}

	// Attempt to dump the rules of the chain, if this fails then chain doesn't exist.
	rules, err := subprocess.RunCommand(cmd, "-w", "-t", table, "-S", chain)
	if err != nil {
		return false, false, nil
	}

	for _, rule := range util.SplitNTrimSpace(strings.TrimSpace(rules), "\n", -1, true) {
		if strings.HasPrefix(rule, "-A") {
			return true, true, nil
		}
	}

	return true, false, nil
}

// iptablesChainCreate creates a chain in a table.
func (d Xtables) iptablesChainCreate(ipVersion uint, table string, chain string) error {
	var cmd string
	if ipVersion == 4 {
		cmd = "iptables"
	} else if ipVersion == 6 {
		cmd = "ip6tables"
	} else {
		return fmt.Errorf("Invalid IP version")
	}

	// Attempt to create chain in table.
	_, err := subprocess.RunCommand(cmd, "-w", "-t", table, "-N", chain)
	if err != nil {
		return fmt.Errorf("Failed creating %q chain %q in table %q: %w", cmd, chain, table, err)
	}

	return nil
}

// iptablesChainDelete deletes a chain in a table.
func (d Xtables) iptablesChainDelete(ipVersion uint, table string, chain string, flushFirst bool) error {
	var cmd string
	if ipVersion == 4 {
		cmd = "iptables"
	} else if ipVersion == 6 {
		cmd = "ip6tables"
	} else {
		return fmt.Errorf("Invalid IP version")
	}

	// Attempt to flush rules from chain in table.
	if flushFirst {
		_, err := subprocess.RunCommand(cmd, "-w", "-t", table, "-F", chain)
		if err != nil {
			return fmt.Errorf("Failed flushing %q chain %q in table %q: %w", cmd, chain, table, err)
		}
	}

	// Attempt to delete chain in table.
	_, err := subprocess.RunCommand(cmd, "-w", "-t", table, "-X", chain)
	if err != nil {
		return fmt.Errorf("Failed deleting %q chain %q in table %q: %w", cmd, chain, table, err)
	}

	return nil
}

// NetworkApplyForwards apply network address forward rules to firewall.
func (d Xtables) NetworkApplyForwards(networkName string, rules []AddressForward) error {
	// Validate all rules first.
	for i, rule := range rules {
		if rule.ListenAddress == nil {
			return fmt.Errorf("Invalid rule %d, listen address is required", i)
		}

		if rule.TargetAddress == nil {
			return fmt.Errorf("Invalid rule %d, target address is required", i)
		}

		listenPortLen := len(rule.ListenPorts)
		if listenPortLen == 0 && rule.Protocol != "" {
			return fmt.Errorf("Invalid rule %d, default target rule but non-empty protocol", i)
		}

		targetPortLen := len(rule.TargetPorts)
		if targetPortLen > 1 && targetPortLen != listenPortLen {
			return fmt.Errorf("Invalid rule %d, mismatch between listen port(s) and target port(s) count", i)
		}
	}

	comment := d.networkForwardIPTablesComment(networkName)

	clearNetworkForwards := func() error {
		for _, ipVersion := range []uint{4, 6} {
			err := d.iptablesClear(ipVersion, []string{comment}, "nat")
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Clear any forward rules associated to the network.
	err := clearNetworkForwards()
	if err != nil {
		return err
	}

	reverter := revert.New()
	defer reverter.Fail()

	// Clear all network forwards if we fail, otherwise the forwards are only partially applied.
	reverter.Add(func() {
		err := clearNetworkForwards()
		if err != nil {
			logger.Error("Failed to clear firewall rules after failing to apply network forwards", logger.Ctx{"network_name": networkName, "error": err})
		}
	})

	// Build up rules, ordering by default target rules first, followed by port specific listen rules.
	// This is so the generated firewall rules will apply the port specific rules first (they are prepended).
	for _, listenPortsOnly := range []bool{false, true} {
		for _, rule := range rules {
			// Process the rules in order of outer loop.
			listenPortsLen := len(rule.ListenPorts)
			if (listenPortsOnly && listenPortsLen < 1) || (!listenPortsOnly && listenPortsLen > 0) {
				continue
			}

			ipVersion := uint(4)
			if rule.ListenAddress.To4() == nil {
				ipVersion = 6
			}

			listenAddressStr := rule.ListenAddress.String()
			targetAddressStr := rule.TargetAddress.String()

			if rule.Protocol != "" {
				if len(rule.TargetPorts) == 0 {
					rule.TargetPorts = rule.ListenPorts
				}

				targetPortRanges := portRangesFromSlice(rule.TargetPorts)
				for _, targetPortRange := range targetPortRanges {
					targetPortRangeStr := portRangeStr(targetPortRange, ":")

					// Apply MASQUERADE rule for each target range.
					// instance <-> instance.
					// Requires instance's bridge port has hairpin mode enabled when br_netfilter is loaded.
					err := d.iptablesPrepend(ipVersion, comment, "nat", "POSTROUTING", "-p", rule.Protocol, "--source", targetAddressStr, "--destination", targetAddressStr, "--dport", targetPortRangeStr, "-j", "MASQUERADE")
					if err != nil {
						return err
					}
				}

				dnatRanges := getOptimisedDNATRanges(&rule)
				for listenPortRange, targetPortRange := range dnatRanges {
					listenPortRangeStr := portRangeStr(listenPortRange, ":")
					targetDest := targetAddressStr

					if targetPortRange[1] == 1 {
						targetPortStr := portRangeStr(targetPortRange, ":")
						targetDest = fmt.Sprintf("%s:%s", targetAddressStr, targetPortStr)
						if ipVersion == 6 {
							targetDest = fmt.Sprintf("[%s]:%s", targetAddressStr, targetPortStr)
						}
					}

					// outbound <-> instance.
					err := d.iptablesPrepend(ipVersion, comment, "nat", "PREROUTING", "-p", rule.Protocol, "--destination", listenAddressStr, "--dport", listenPortRangeStr, "-j", "DNAT", "--to-destination", targetDest)
					if err != nil {
						return err
					}

					// host <-> instance.
					err = d.iptablesPrepend(ipVersion, comment, "nat", "OUTPUT", "-p", rule.Protocol, "--destination", listenAddressStr, "--dport", listenPortRangeStr, "-j", "DNAT", "--to-destination", targetDest)
					if err != nil {
						return err
					}
				}
			} else {
				// Format the destination host/port as appropriate.
				targetDest := targetAddressStr
				if ipVersion == 6 {
					targetDest = fmt.Sprintf("[%s]", targetAddressStr)
				}

				// outbound <-> instance.
				err := d.iptablesPrepend(ipVersion, comment, "nat", "PREROUTING", "--destination", listenAddressStr, "-j", "DNAT", "--to-destination", targetDest)
				if err != nil {
					return err
				}

				// host <-> instance.
				err = d.iptablesPrepend(ipVersion, comment, "nat", "OUTPUT", "--destination", listenAddressStr, "-j", "DNAT", "--to-destination", targetDest)
				if err != nil {
					return err
				}

				// instance <-> instance.
				// Requires instance's bridge port has hairpin mode enabled when br_netfilter is
				// loaded.
				err = d.iptablesPrepend(ipVersion, comment, "nat", "POSTROUTING", "--source", targetAddressStr, "--destination", targetAddressStr, "-j", "MASQUERADE")
				if err != nil {
					return err
				}
			}
		}
	}

	reverter.Success()
	return nil
}
