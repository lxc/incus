package firewall

import (
	"net"

	"github.com/lxc/incus/v6/internal/server/firewall/drivers"
)

// Firewall represents an Incus firewall.
type Firewall interface {
	String() string
	Compat() (bool, error)

	NetworkSetup(networkName string, opts drivers.Opts) error
	NetworkClear(networkName string, delete bool, ipVersions []uint) error
	NetworkApplyACLRules(networkName string, rules []drivers.ACLRule) error
	NetworkApplyForwards(networkName string, rules []drivers.AddressForward) error

	InstanceSetupBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4Nets []*net.IPNet, IPv6Nets []*net.IPNet, parentManaged bool) error
	InstanceClearBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4Nets []*net.IPNet, IPv6Nets []*net.IPNet) error

	InstanceSetupProxyNAT(projectName string, instanceName string, deviceName string, forward *drivers.AddressForward) error
	InstanceClearProxyNAT(projectName string, instanceName string, deviceName string) error

	InstanceSetupRPFilter(projectName string, instanceName string, deviceName string, hostName string) error
	InstanceClearRPFilter(projectName string, instanceName string, deviceName string) error

	InstanceSetupNetPrio(projectName string, instanceName string, deviceName string, netPrio uint32) error
	InstanceClearNetPrio(projectName string, instanceName string, deviceName string) error
}
