package drivers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/google/uuid"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

const nftablesNamespace = "incus"
const nftablesContentTemplate = "nftablesContent"

// nftablesChainSeparator The "." character is specifically chosen here so as to prevent the ability for collisions
// between project prefix (which is empty if project is default) and device name combinations that both are allowed
// to contain underscores (where as instance name is not).
const nftablesChainSeparator = "."

// nftablesMinVersion We need at least 0.9.1 as this was when the arp ether saddr filters were added.
const nftablesMinVersion = "0.9.1"

// Nftables is an implmentation of Incus firewall using nftables.
type Nftables struct{}

// String returns the driver name.
func (d Nftables) String() string {
	return "nftables"
}

// Compat returns whether the driver backend is in use, and any host compatibility errors.
func (d Nftables) Compat() (bool, error) {
	// Get the kernel version.
	uname, err := linux.Uname()
	if err != nil {
		return false, err
	}

	// We require a >= 5.2 kernel to avoid weird conflicts with xtables and support for inet table NAT rules.
	releaseLen := len(uname.Release)
	if releaseLen > 1 {
		verErr := fmt.Errorf("Kernel version does not meet minimum requirement of 5.2")
		releaseParts := strings.SplitN(uname.Release, ".", 3)
		if len(releaseParts) < 2 {
			return false, fmt.Errorf("Failed parsing kernel version number into parts: %w", err)
		}

		majorVer := releaseParts[0]
		majorVerInt, err := strconv.Atoi(majorVer)
		if err != nil {
			return false, fmt.Errorf("Failed parsing kernel major version number %q: %w", majorVer, err)
		}

		if majorVerInt < 5 {
			return false, verErr
		}

		if majorVerInt == 5 {
			minorVer := releaseParts[1]
			minorVerInt, err := strconv.Atoi(minorVer)
			if err != nil {
				return false, fmt.Errorf("Failed parsing kernel minor version number %q: %w", minorVer, err)
			}

			if minorVerInt < 2 {
				return false, verErr
			}
		}
	}

	// Check if nftables nft command exists, if not use xtables.
	_, err = exec.LookPath("nft")
	if err != nil {
		return false, fmt.Errorf("Backend command %q missing", "nft")
	}

	// Get nftables version.
	nftVersion, err := d.hostVersion()
	if err != nil {
		return false, fmt.Errorf("Failed detecting nft version: %w", err)
	}

	// Check nft version meets minimum required.
	minVer, _ := version.NewDottedVersion(nftablesMinVersion)
	if nftVersion.Compare(minVer) < 0 {
		return false, fmt.Errorf("nft version %q is too low, need %q or above", nftVersion, nftablesMinVersion)
	}

	// Check that nftables works at all (some kernels let you list ruleset despite missing support).
	testTable := fmt.Sprintf("incus_test_%s", uuid.New().String())

	_, err = subprocess.RunCommandCLocale("nft", "create", "table", testTable)
	if err != nil {
		return false, fmt.Errorf("Failed to create a test table: %w", err)
	}

	_, err = subprocess.RunCommandCLocale("nft", "delete", "table", testTable)
	if err != nil {
		return false, fmt.Errorf("Failed to delete a test table: %w", err)
	}

	// Check whether in use by parsing ruleset and looking for existing rules.
	ruleset, err := d.nftParseRuleset()
	if err != nil {
		return false, fmt.Errorf("Failed parsing nftables existing ruleset: %w", err)
	}

	for _, item := range ruleset {
		if item.ItemType == "rule" {
			return true, nil // At least one rule found indicates in use.
		}
	}

	return false, nil
}

// nftGenericItem represents some common fields amongst the different nftables types.
type nftGenericItem struct {
	ItemType string `json:"-"`      // Type of item (table, chain or rule). Populated by Incus.
	Family   string `json:"family"` // Family of item (ip, ip6, bridge etc).
	Table    string `json:"table"`  // Table the item belongs to (for chains and rules).
	Chain    string `json:"chain"`  // Chain the item belongs to (for rules).
	Name     string `json:"name"`   // Name of item (for tables and chains).
}

// nftParseRuleset parses the ruleset and returns the generic parts as a slice of items.
func (d Nftables) nftParseRuleset() ([]nftGenericItem, error) {
	// Dump ruleset as JSON. Use -nn flags to avoid doing DNS lookups of IPs mentioned in any rules.
	cmd := exec.Command("nft", "--json", "-nn", "list", "ruleset")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	defer func() { _ = cmd.Wait() }()

	// This only extracts certain generic parts of the ruleset, see man libnftables-json for more info.
	v := &struct {
		Nftables []map[string]nftGenericItem `json:"nftables"`
	}{}

	err = json.NewDecoder(stdout).Decode(v)
	if err != nil {
		return nil, err
	}

	items := []nftGenericItem{}
	for _, item := range v.Nftables {
		rule, foundRule := item["rule"]
		chain, foundChain := item["chain"]
		table, foundTable := item["table"]
		if foundRule {
			rule.ItemType = "rule"
			items = append(items, rule)
		} else if foundChain {
			chain.ItemType = "chain"
			items = append(items, chain)
		} else if foundTable {
			table.ItemType = "table"
			items = append(items, table)
		}
	}

	err = cmd.Wait()
	if err != nil {
		return nil, err
	}

	return items, nil
}

// GetVersion returns the version of nftables.
func (d Nftables) hostVersion() (*version.DottedVersion, error) {
	output, err := subprocess.RunCommandCLocale("nft", "--version")
	if err != nil {
		return nil, fmt.Errorf("Failed to check nftables version: %w", err)
	}

	lines := strings.Split(string(output), " ")
	return version.Parse(strings.TrimPrefix(lines[1], "v"))
}

// networkSetupForwardingPolicy allows forwarding dependent on boolean argument.
func (d Nftables) networkSetupForwardingPolicy(networkName string, ip4Allow *bool, ip6Allow *bool) error {
	tplFields := map[string]any{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         "inet",
	}

	if ip4Allow != nil {
		ip4Action := "reject"

		if *ip4Allow {
			ip4Action = "accept"
		}

		tplFields["ip4Action"] = ip4Action
	}

	if ip6Allow != nil {
		ip6Action := "reject"

		if *ip6Allow {
			ip6Action = "accept"
		}

		tplFields["ip6Action"] = ip6Action
	}

	err := d.applyNftConfig(nftablesNetForwardingPolicy, tplFields)
	if err != nil {
		return fmt.Errorf("Failed adding forwarding policy rules for network %q (%s): %w", networkName, tplFields["family"], err)
	}

	return nil
}

// networkSetupOutboundNAT configures outbound NAT.
// If srcIP is non-nil then SNAT is used with the specified address, otherwise MASQUERADE mode is used.
// Append mode is always on and so the append argument is ignored.
func (d Nftables) networkSetupOutboundNAT(networkName string, SNATV4 *SNATOpts, SNATV6 *SNATOpts) error {
	rules := make(map[string]*SNATOpts, 0)

	tplFields := map[string]any{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         "inet",
	}

	// If SNAT IP not supplied then use the IP of the outbound interface (MASQUERADE).
	if SNATV4 != nil {
		rules["ip"] = SNATV4
	}

	if SNATV6 != nil {
		rules["ip6"] = SNATV6
	}

	tplFields["rules"] = rules

	err := d.applyNftConfig(nftablesNetOutboundNAT, tplFields)
	if err != nil {
		return fmt.Errorf("Failed adding outbound NAT rules for network %q (%s): %w", networkName, tplFields["family"], err)
	}

	return nil
}

// networkSetupICMPDHCPDNSAccess sets up basic nftables overrides for ICMP, DHCP and DNS.
func (d Nftables) networkSetupICMPDHCPDNSAccess(networkName string, ipVersions []uint) error {
	ipFamilies := []string{}
	for _, ipVersion := range ipVersions {
		switch ipVersion {
		case 4:
			ipFamilies = append(ipFamilies, "ip")
		case 6:
			ipFamilies = append(ipFamilies, "ip6")
		}
	}

	tplFields := map[string]any{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         "inet",
		"ipFamilies":     ipFamilies,
	}

	err := d.applyNftConfig(nftablesNetICMPDHCPDNS, tplFields)
	if err != nil {
		return fmt.Errorf("Failed adding ICMP, DHCP and DNS access rules for network %q (%s): %w", networkName, tplFields["family"], err)
	}

	return nil
}

func (d Nftables) networkSetupACLChainAndJumpRules(networkName string) error {
	tplFields := map[string]any{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         "inet",
	}

	config := &strings.Builder{}
	err := nftablesNetACLSetup.Execute(config, tplFields)
	if err != nil {
		return fmt.Errorf("Failed running %q template: %w", nftablesNetACLSetup.Name(), err)
	}

	err = subprocess.RunCommandWithFds(context.TODO(), strings.NewReader(config.String()), nil, "nft", "-f", "-")
	if err != nil {
		return err
	}

	return nil
}

// NetworkSetup configure network firewall.
func (d Nftables) NetworkSetup(networkName string, opts Opts) error {
	// Do this first before adding other network rules, so jump to ACL rules come first.
	if opts.ACL {
		err := d.networkSetupACLChainAndJumpRules(networkName)
		if err != nil {
			return err
		}
	}

	if opts.SNATV4 != nil || opts.SNATV6 != nil {
		err := d.networkSetupOutboundNAT(networkName, opts.SNATV4, opts.SNATV6)
		if err != nil {
			return err
		}
	}

	dhcpDNSAccess := []uint{}
	var ip4ForwardingAllow, ip6ForwardingAllow *bool

	if opts.FeaturesV4 != nil || opts.FeaturesV6 != nil {
		if opts.FeaturesV4 != nil {
			if opts.FeaturesV4.ICMPDHCPDNSAccess {
				dhcpDNSAccess = append(dhcpDNSAccess, 4)
			}

			ip4ForwardingAllow = &opts.FeaturesV4.ForwardingAllow
		}

		if opts.FeaturesV6 != nil {
			if opts.FeaturesV6.ICMPDHCPDNSAccess {
				dhcpDNSAccess = append(dhcpDNSAccess, 6)
			}

			ip6ForwardingAllow = &opts.FeaturesV6.ForwardingAllow
		}

		err := d.networkSetupForwardingPolicy(networkName, ip4ForwardingAllow, ip6ForwardingAllow)
		if err != nil {
			return err
		}

		err = d.networkSetupICMPDHCPDNSAccess(networkName, dhcpDNSAccess)
		if err != nil {
			return err
		}
	}

	return nil
}

// NetworkClear removes the Incus network related chains.
// The delete and ipeVersions arguments have no effect for nftables driver.
func (d Nftables) NetworkClear(networkName string, _ bool, _ []uint) error {
	removeChains := []string{
		"fwd", "pstrt", "in", "out", // Chains used for network operation rules.
		"aclin", "aclout", "aclfwd", "acl", // Chains used by ACL rules.
		"fwdprert", "fwdout", "fwdpstrt", // Chains used by Address Forward rules.
		"egress", // Chains added for limits.priority option
	}

	// Remove chains created by network rules.
	// Remove from ip and ip6 tables to ensure cleanup for instances started before we moved to inet table
	err := d.removeChains([]string{"inet", "ip", "ip6", "netdev"}, networkName, removeChains...)
	if err != nil {
		return fmt.Errorf("Failed clearing nftables rules for network %q: %w", networkName, err)
	}

	return nil
}

// instanceDeviceLabel returns the unique label used for instance device chains.
func (d Nftables) instanceDeviceLabel(projectName, instanceName, deviceName string) string {
	return fmt.Sprintf("%s%s%s", project.Instance(projectName, instanceName), nftablesChainSeparator, deviceName)
}

// InstanceSetupBridgeFilter sets up the filter rules to apply bridged device IP filtering.
func (d Nftables) InstanceSetupBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4Nets []*net.IPNet, IPv6Nets []*net.IPNet, parentManaged bool) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)

	mac, err := net.ParseMAC(hwAddr)
	if err != nil {
		return err
	}

	tplFields := map[string]any{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"family":         "bridge",
		"deviceLabel":    deviceLabel,
		"parentName":     parentName,
		"hostName":       hostName,
		"hwAddr":         hwAddr,
		"hwAddrHex":      fmt.Sprintf("0x%s", hex.EncodeToString(mac)),
	}

	// Filter unwanted ethernet frames when using IP filtering.
	if len(IPv4Nets)+len(IPv6Nets) > 0 {
		tplFields["filterUnwantedFrames"] = true
	}

	if IPv4Nets != nil && len(IPv4Nets) == 0 {
		tplFields["ipv4FilterAll"] = true
	}

	ipv4Nets := make([]string, 0, len(IPv4Nets))
	for _, ipv4Net := range IPv4Nets {
		ipv4Nets = append(ipv4Nets, ipv4Net.String())
	}

	if IPv6Nets != nil && len(IPv6Nets) == 0 {
		tplFields["ipv6FilterAll"] = true
	}

	ipv6Nets := make([]map[string]string, 0, len(IPv6Nets))
	for _, ipv6Net := range IPv6Nets {
		ones, _ := ipv6Net.Mask.Size()
		prefix, err := subnetPrefixHex(ipv6Net)
		if err != nil {
			return err
		}

		ipv6Nets = append(ipv6Nets, map[string]string{
			"net":       ipv6Net.String(),
			"nBits":     strconv.Itoa(ones),
			"hexPrefix": fmt.Sprintf("0x%s", prefix),
		})
	}

	tplFields["ipv4Nets"] = ipv4Nets
	tplFields["ipv6Nets"] = ipv6Nets

	err = d.applyNftConfig(nftablesInstanceBridgeFilter, tplFields)
	if err != nil {
		return fmt.Errorf("Failed adding bridge filter rules for instance device %q (%s): %w", deviceLabel, tplFields["family"], err)
	}

	return nil
}

// InstanceClearBridgeFilter removes any filter rules that were added to apply bridged device IP filtering.
func (d Nftables) InstanceClearBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, _ []*net.IPNet, _ []*net.IPNet) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)

	// Remove chains created by bridge filter rules.
	err := d.removeChains([]string{"bridge"}, deviceLabel, "in", "fwd")
	if err != nil {
		return fmt.Errorf("Failed clearing bridge filter rules for instance device %q: %w", deviceLabel, err)
	}

	return nil
}

// InstanceSetupProxyNAT creates DNAT rules for proxy devices.
func (d Nftables) InstanceSetupProxyNAT(projectName string, instanceName string, deviceName string, forward *AddressForward) error {
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

	ipFamily := "ip"
	if forward.ListenAddress.To4() == nil {
		ipFamily = "ip6"
	}

	listenAddressStr := forward.ListenAddress.String()
	targetAddressStr := forward.TargetAddress.String()

	// Generate slices of rules to add.
	var dnatRules []map[string]any
	var snatRules []map[string]any

	targetPortRanges := portRangesFromSlice(forward.TargetPorts)
	for _, targetPortRange := range targetPortRanges {
		targetPortRangeStr := portRangeStr(targetPortRange, "-")
		snatRules = append(snatRules, map[string]any{
			"ipFamily":    ipFamily,
			"protocol":    forward.Protocol,
			"targetHost":  targetAddressStr,
			"targetPorts": targetPortRangeStr,
		})
	}

	dnatRanges := getOptimisedDNATRanges(forward)
	for listenPortRange, targetPortRange := range dnatRanges {
		// Format the destination host/port as appropriate
		targetDest := targetAddressStr
		if targetPortRange[1] == 1 {
			targetPortStr := portRangeStr(targetPortRange, ":")
			targetDest = fmt.Sprintf("%s:%s", targetAddressStr, targetPortStr)
			if ipFamily == "ip6" {
				targetDest = fmt.Sprintf("[%s]:%s", targetAddressStr, targetPortStr)
			}
		}

		dnatRules = append(dnatRules, map[string]any{
			"ipFamily":      ipFamily,
			"protocol":      forward.Protocol,
			"listenAddress": listenAddressStr,
			"listenPorts":   portRangeStr(listenPortRange, "-"),
			"targetDest":    targetDest,
		})
	}

	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)
	tplFields := map[string]any{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"chainPrefix":    "", // Empty prefix for backwards compatibility with existing device chains.
		"family":         "inet",
		"label":          deviceLabel,
		"dnatRules":      dnatRules,
		"snatRules":      snatRules,
	}

	config := &strings.Builder{}
	err := nftablesNetProxyNAT.Execute(config, tplFields)
	if err != nil {
		return fmt.Errorf("Failed running %q template: %w", nftablesNetProxyNAT.Name(), err)
	}

	err = subprocess.RunCommandWithFds(context.TODO(), strings.NewReader(config.String()), nil, "nft", "-f", "-")
	if err != nil {
		return err
	}

	return nil
}

// InstanceClearProxyNAT remove DNAT rules for proxy devices.
func (d Nftables) InstanceClearProxyNAT(projectName string, instanceName string, deviceName string) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)

	// Remove from ip and ip6 tables to ensure cleanup for instances started before we moved to inet table.
	err := d.removeChains([]string{"inet", "ip", "ip6"}, deviceLabel, "out", "prert", "pstrt")
	if err != nil {
		return fmt.Errorf("Failed clearing proxy rules for instance device %q: %w", deviceLabel, err)
	}

	return nil
}

// applyNftConfig loads the specified config template and then applies it to the common template before sending to
// the nft command to be atomically applied to the system.
func (d Nftables) applyNftConfig(tpl *template.Template, tplFields map[string]any) error {
	// Load the specified template into the common template's parse tree under the nftableContentTemplate
	// name so that the nftableContentTemplate template can use it with the generic name.
	_, err := nftablesCommonTable.AddParseTree(nftablesContentTemplate, tpl.Tree)
	if err != nil {
		return fmt.Errorf("Failed loading %q template: %w", tpl.Name(), err)
	}

	config := &strings.Builder{}
	err = nftablesCommonTable.Execute(config, tplFields)
	if err != nil {
		return fmt.Errorf("Failed running %q template: %w", tpl.Name(), err)
	}

	err = subprocess.RunCommandWithFds(context.TODO(), strings.NewReader(config.String()), nil, "nft", "-f", "-")
	if err != nil {
		return fmt.Errorf("Failed apply nftables config: %w", err)
	}

	return nil
}

// removeChains removes the specified chains from the specified families.
// If not empty, chain suffix is appended to each chain name, separated with "_".
func (d Nftables) removeChains(families []string, chainSuffix string, chains ...string) error {
	ruleset, err := d.nftParseRuleset()
	if err != nil {
		return err
	}

	fullChains := chains
	if chainSuffix != "" {
		fullChains = make([]string, 0, len(chains))
		for _, chain := range chains {
			fullChains = append(fullChains, fmt.Sprintf("%s%s%s", chain, nftablesChainSeparator, chainSuffix))
		}
	}

	// Search ruleset for chains we are looking for.
	foundChains := make(map[string]nftGenericItem)
	for _, family := range families {
		for _, item := range ruleset {
			if item.ItemType == "chain" && item.Family == family && item.Table == nftablesNamespace && slices.Contains(fullChains, item.Name) {
				foundChains[item.Name] = item
			}
		}
	}

	// Delete the chains in the order specified in chains slice (to avoid dependency issues).
	for _, fullChain := range fullChains {
		item, found := foundChains[fullChain]
		if !found {
			continue
		}

		_, err = subprocess.RunCommand("nft", "flush", "chain", item.Family, nftablesNamespace, item.Name, ";", "delete", "chain", item.Family, nftablesNamespace, item.Name)
		if err != nil {
			return fmt.Errorf("Failed deleting nftables chain %q (%s): %w", item.Name, item.Family, err)
		}
	}

	return nil
}

// InstanceSetupRPFilter activates reverse path filtering for the specified instance device on the host interface.
func (d Nftables) InstanceSetupRPFilter(projectName string, instanceName string, deviceName string, hostName string) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)
	tplFields := map[string]any{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"deviceLabel":    deviceLabel,
		"hostName":       hostName,
		"family":         "inet",
	}

	err := d.applyNftConfig(nftablesInstanceRPFilter, tplFields)
	if err != nil {
		return fmt.Errorf("Failed adding reverse path filter rules for instance device %q (%s): %w", deviceLabel, tplFields["family"], err)
	}

	return nil
}

// InstanceClearRPFilter removes reverse path filtering for the specified instance device on the host interface.
func (d Nftables) InstanceClearRPFilter(projectName string, instanceName string, deviceName string) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)

	// Remove from ip and ip6 tables to ensure cleanup for instances started before we moved to inet table.
	err := d.removeChains([]string{"inet", "ip", "ip6"}, deviceLabel, "prert")
	if err != nil {
		return fmt.Errorf("Failed clearing reverse path filter rules for instance device %q: %w", deviceLabel, err)
	}

	return nil
}

// InstanceSetupNetPrio activates setting of skb->priority for the specified instance device on the host interface.
func (d Nftables) InstanceSetupNetPrio(projectName string, instanceName string, deviceName string, netPrio uint32) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)
	tplFields := map[string]any{
		"namespace":      nftablesNamespace,
		"family":         "netdev",
		"chainSeparator": nftablesChainSeparator,
		"deviceLabel":    deviceLabel,
		"deviceName":     deviceName,
		"netPrio":        netPrio,
	}

	err := d.applyNftConfig(nftablesInstanceNetPrio, tplFields)
	if err != nil {
		return fmt.Errorf("Failed adding netprio rules for instance device %q: %w", deviceLabel, err)
	}

	return nil
}

// InstanceClearNetPrio removes setting of skb->priority for the specified instance device on the host interface.
func (d Nftables) InstanceClearNetPrio(projectName string, instanceName string, deviceName string) error {
	if deviceName == "" {
		return fmt.Errorf("Failed clearing netprio rules for instance %q in project %q: device name is empty", projectName, instanceName)
	}

	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)
	chainLabel := fmt.Sprintf("netprio%s%s", nftablesChainSeparator, deviceLabel)

	err := d.removeChains([]string{"netdev"}, chainLabel, "egress")
	if err != nil {
		return fmt.Errorf("Failed clearing netprio rules for instance device %q: %w", deviceLabel, err)
	}

	return nil
}

// NetworkApplyACLRules applies ACL rules to the existing firewall chains.
func (d Nftables) NetworkApplyACLRules(networkName string, rules []ACLRule) error {
	nftRules := make([]string, 0)
	for _, rule := range rules {
		// First try generating rules with IPv4 or IP agnostic criteria.
		nftRule, partial, err := d.aclRuleCriteriaToRules(networkName, 4, &rule)
		if err != nil {
			return err
		}

		if nftRule != "" {
			nftRules = append(nftRules, nftRule)
		}

		if partial {
			// If we couldn't fully generate the ruleset with only IPv4 or IP agnostic criteria, then
			// fill in the remaining parts using IPv6 criteria.
			nftRule, _, err = d.aclRuleCriteriaToRules(networkName, 6, &rule)
			if err != nil {
				return err
			}

			if nftRule == "" {
				return fmt.Errorf("Invalid empty rule generated")
			}

			nftRules = append(nftRules, nftRule)
		} else if nftRule == "" {
			return fmt.Errorf("Invalid empty rule generated")
		}
	}

	tplFields := map[string]any{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         "inet",
		"rules":          nftRules,
	}

	config := &strings.Builder{}
	err := nftablesNetACLRules.Execute(config, tplFields)
	if err != nil {
		return fmt.Errorf("Failed running %q template: %w", nftablesNetACLRules.Name(), err)
	}

	err = subprocess.RunCommandWithFds(context.TODO(), strings.NewReader(config.String()), nil, "nft", "-f", "-")
	if err != nil {
		return err
	}

	return nil
}

// aclRuleCriteriaToRules converts an ACL rule into 1 or more nftables rules.
func (d Nftables) aclRuleCriteriaToRules(networkName string, ipVersion uint, rule *ACLRule) (string, bool, error) {
	var args []string

	if rule.Direction == "ingress" {
		args = append(args, "oifname", networkName) // Coming from host into network's interface.
	} else {
		args = append(args, "iifname", networkName) // Coming from network's interface into host.
	}

	// Add subject filters.
	isPartialRule := false

	if rule.Source != "" {
		matchArgs, partial, err := d.aclRuleSubjectToACLMatch("saddr", ipVersion, util.SplitNTrimSpace(rule.Source, ",", -1, false)...)
		if err != nil {
			return "", false, err
		}

		if matchArgs == nil {
			return "", true, nil // Rule is not appropriate for ipVersion.
		}

		if partial && !isPartialRule {
			isPartialRule = true
		}

		args = append(args, matchArgs...)
	}

	if rule.Destination != "" {
		matchArgs, partial, err := d.aclRuleSubjectToACLMatch("daddr", ipVersion, util.SplitNTrimSpace(rule.Destination, ",", -1, false)...)
		if err != nil {
			return "", false, err
		}

		if matchArgs == nil {
			return "", partial, nil // Rule is not appropriate for ipVersion.
		}

		if partial && !isPartialRule {
			isPartialRule = true
		}

		args = append(args, matchArgs...)
	}

	// Add protocol filters.
	if slices.Contains([]string{"tcp", "udp"}, rule.Protocol) {
		args = append(args, "meta", "l4proto", rule.Protocol)

		if rule.SourcePort != "" {
			args = append(args, d.aclRulePortToACLMatch("sport", util.SplitNTrimSpace(rule.SourcePort, ",", -1, false)...)...)
		}

		if rule.DestinationPort != "" {
			args = append(args, d.aclRulePortToACLMatch("dport", util.SplitNTrimSpace(rule.DestinationPort, ",", -1, false)...)...)
		}
	} else if slices.Contains([]string{"icmp4", "icmp6"}, rule.Protocol) {
		var icmpIPVersion uint
		var protoName string

		switch rule.Protocol {
		case "icmp4":
			protoName = "icmp"
			icmpIPVersion = 4
			args = append(args, "ip", "protocol", protoName)
		case "icmp6":
			protoName = "icmpv6"
			icmpIPVersion = 6
			args = append(args, "ip6", "nexthdr", protoName)
		}

		if ipVersion != icmpIPVersion {
			// If we got this far it means that source/destination are either empty or are filled
			// with at least some subjects in the same family as ipVersion. So if the icmpIPVersion
			// doesn't match the ipVersion then it means the rule contains mixed-version subjects
			// which is invalid when using an IP version specific ICMP protocol.
			if rule.Source != "" || rule.Destination != "" {
				return "", false, fmt.Errorf("Invalid use of %q protocol with non-IPv%d source/destination criteria", rule.Protocol, ipVersion)
			}

			// Otherwise it means this is just a blanket ICMP rule and is only appropriate for use
			// with the corresponding ipVersion nft command.
			return "", true, nil // Rule is not appropriate for ipVersion.
		}

		if rule.ICMPType != "" {
			args = append(args, protoName, "type", rule.ICMPType)

			if rule.ICMPCode != "" {
				args = append(args, protoName, "code", rule.ICMPCode)
			}
		}
	}

	// Handle logging.
	if rule.Log {
		args = append(args, "log")

		if rule.LogName != "" {
			// Add a trailing space to prefix for readability in logs.
			args = append(args, "prefix", fmt.Sprintf(`"%s "`, rule.LogName))
		}
	}

	// Handle action.
	action := rule.Action
	if action == "allow" {
		action = "accept"
	}

	args = append(args, action)

	return strings.Join(args, " "), isPartialRule, nil
}

// aclRuleSubjectToACLMatch converts direction (source/destination) and subject criteria list into xtables args.
// Returns nil if none of the subjects are appropriate for the ipVersion.
func (d Nftables) aclRuleSubjectToACLMatch(direction string, ipVersion uint, subjectCriteria ...string) ([]string, bool, error) {
	fieldParts := make([]string, 0, len(subjectCriteria))

	partial := false

	// For each criterion check if value looks like IP CIDR.
	for _, subjectCriterion := range subjectCriteria {
		if validate.IsNetworkRange(subjectCriterion) == nil {
			criterionParts := strings.SplitN(subjectCriterion, "-", 2)
			if len(criterionParts) > 1 {
				ip := net.ParseIP(criterionParts[0])
				if ip != nil {
					var subjectIPVersion uint = 4
					if ip.To4() == nil {
						subjectIPVersion = 6
					}

					if ipVersion != subjectIPVersion {
						partial = true
						continue // Skip subjects that are not for the ipVersion we are looking for.
					}

					fieldParts = append(fieldParts, fmt.Sprintf("%s-%s", criterionParts[0], criterionParts[1]))
				}
			} else {
				return nil, false, fmt.Errorf("Invalid IP range %q", subjectCriterion)
			}
		} else {
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
					partial = true
					continue // Skip subjects that are not for the ipVersion we are looking for.
				}

				fieldParts = append(fieldParts, subjectCriterion)
			} else {
				return nil, false, fmt.Errorf("Unsupported nftables subject %q", subjectCriterion)
			}
		}
	}

	if len(fieldParts) > 0 {
		ipFamily := "ip"
		if ipVersion == 6 {
			ipFamily = "ip6"
		}

		return []string{ipFamily, direction, fmt.Sprintf("{%s}", strings.Join(fieldParts, ","))}, partial, nil
	}

	return nil, partial, nil // No subjects suitable for ipVersion.
}

// aclRulePortToACLMatch converts protocol (tcp/udp), direction (sports/dports) and port criteria list into
// xtables args.
func (d Nftables) aclRulePortToACLMatch(direction string, portCriteria ...string) []string {
	fieldParts := make([]string, 0, len(portCriteria))

	for _, portCriterion := range portCriteria {
		criterionParts := strings.SplitN(portCriterion, "-", 2)
		if len(criterionParts) > 1 {
			fieldParts = append(fieldParts, fmt.Sprintf("%s-%s", criterionParts[0], criterionParts[1]))
		} else {
			fieldParts = append(fieldParts, criterionParts[0])
		}
	}

	return []string{"th", direction, fmt.Sprintf("{%s}", strings.Join(fieldParts, ","))}
}

// NetworkApplyForwards apply network address forward rules to firewall.
func (d Nftables) NetworkApplyForwards(networkName string, rules []AddressForward) error {
	var dnatRules []map[string]any
	var snatRules []map[string]any

	// Build up rules, ordering by port specific listen rules first, followed by default target rules.
	// This is so the generated firewall rules will apply the port specific rules first.
	for _, listenPortsOnly := range []bool{true, false} {
		for ruleIndex, rule := range rules {
			// Process the rules in order of outer loop.
			listenPortsLen := len(rule.ListenPorts)
			if (listenPortsOnly && listenPortsLen < 1) || (!listenPortsOnly && listenPortsLen > 0) {
				continue
			}

			// Validate the rule.
			if rule.ListenAddress == nil {
				return fmt.Errorf("Invalid rule %d, listen address is required", ruleIndex)
			}

			if rule.TargetAddress == nil {
				return fmt.Errorf("Invalid rule %d, target address is required", ruleIndex)
			}

			if listenPortsLen == 0 && rule.Protocol != "" {
				return fmt.Errorf("Invalid rule %d, default target rule but non-empty protocol", ruleIndex)
			}

			switch len(rule.TargetPorts) {
			case 0:
				// No target ports specified, use listen ports (only valid when protocol is specified).
				rule.TargetPorts = rule.ListenPorts
			case 1:
				// Single target port specified, OK.
			case len(rule.ListenPorts):
				// One-to-one match with listen ports, OK.
			default:
				return fmt.Errorf("Invalid rule %d, mismatch between listen port(s) and target port(s) count", ruleIndex)
			}

			ipFamily := "ip"
			if rule.ListenAddress.To4() == nil {
				ipFamily = "ip6"
			}

			listenAddressStr := rule.ListenAddress.String()
			targetAddressStr := rule.TargetAddress.String()

			if rule.Protocol != "" {
				targetPortRanges := portRangesFromSlice(rule.TargetPorts)
				for _, targetPortRange := range targetPortRanges {
					targetPortRangeStr := portRangeStr(targetPortRange, "-")
					snatRules = append(snatRules, map[string]any{
						"ipFamily":    ipFamily,
						"protocol":    rule.Protocol,
						"targetHost":  targetAddressStr,
						"targetPorts": targetPortRangeStr,
					})
				}

				dnatRanges := getOptimisedDNATRanges(&rule)
				for listenPortRange, targetPortRange := range dnatRanges {
					// Format the destination host/port as appropriate
					targetDest := targetAddressStr
					if targetPortRange[1] == 1 {
						targetPortStr := portRangeStr(targetPortRange, ":")
						targetDest = fmt.Sprintf("%s:%s", targetAddressStr, targetPortStr)
						if ipFamily == "ip6" {
							targetDest = fmt.Sprintf("[%s]:%s", targetAddressStr, targetPortStr)
						}
					}

					dnatRules = append(dnatRules, map[string]any{
						"ipFamily":      ipFamily,
						"protocol":      rule.Protocol,
						"listenAddress": listenAddressStr,
						"listenPorts":   portRangeStr(listenPortRange, "-"),
						"targetDest":    targetDest,
					})
				}
			} else {
				// Format the destination host/port as appropriate.
				targetDest := targetAddressStr
				if ipFamily == "ip6" {
					targetDest = fmt.Sprintf("[%s]", targetAddressStr)
				}

				dnatRules = append(dnatRules, map[string]any{
					"ipFamily":      ipFamily,
					"listenAddress": listenAddressStr,
					"targetDest":    targetDest,
					"targetHost":    targetAddressStr,
				})

				snatRules = append(snatRules, map[string]any{
					"ipFamily":   ipFamily,
					"targetHost": targetAddressStr,
				})
			}
		}
	}

	tplFields := map[string]any{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"chainPrefix":    "fwd", // Differentiate from proxy device forwards.
		"family":         "inet",
		"label":          networkName,
		"dnatRules":      dnatRules,
		"snatRules":      snatRules,
	}

	// Apply rules or remove chains if no rules generated.
	if len(dnatRules) > 0 || len(snatRules) > 0 {
		config := &strings.Builder{}
		err := nftablesNetProxyNAT.Execute(config, tplFields)
		if err != nil {
			return fmt.Errorf("Failed running %q template: %w", nftablesNetProxyNAT.Name(), err)
		}

		err = subprocess.RunCommandWithFds(context.TODO(), strings.NewReader(config.String()), nil, "nft", "-f", "-")
		if err != nil {
			return err
		}
	} else {
		err := d.removeChains([]string{"inet", "ip", "ip6"}, networkName, "fwdprert", "fwdout", "fwdpstrt")
		if err != nil {
			return fmt.Errorf("Failed clearing nftables forward rules for network %q: %w", networkName, err)
		}
	}

	return nil
}
