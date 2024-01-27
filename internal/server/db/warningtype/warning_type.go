//go:build linux && cgo && !agent

package warningtype

// Type is a numeric code indentifying the type of warning.
type Type int

const (
	// Undefined represents an undefined warning.
	Undefined Type = iota
	// MissingCGroupBlkio represents the missing CGroup blkio warning.
	MissingCGroupBlkio
	// MissingCGroupBlkioWeight represents the missing CGroup blkio.weight warning.
	MissingCGroupBlkioWeight
	// MissingCGroupCPUController represents the missing CGroup CPU controller warning.
	MissingCGroupCPUController
	// MissingCGroupCPUsetController represents the missing GCgroup CPUset controller warning.
	MissingCGroupCPUsetController
	// MissingCGroupCPUacctController represents the missing GCgroup CPUacct controller warning.
	MissingCGroupCPUacctController
	// MissingCGroupDevicesController represents the missing GCgroup devices controller warning.
	MissingCGroupDevicesController
	// MissingCGroupFreezerController represents the missing GCgroup freezer controller warning.
	MissingCGroupFreezerController
	// MissingCGroupHugetlbController represents the missing GCgroup hugetlb controller warning.
	MissingCGroupHugetlbController
	// MissingCGroupMemoryController represents the missing GCgroup memory controller warning.
	MissingCGroupMemoryController
	// Spacer where MissingCGroupNetworkPriorityController used to be.
	_
	// MissingCGroupPidsController represents the missing GCgroup pids controller warning.
	MissingCGroupPidsController
	// MissingCGroupMemorySwapAccounting represents the missing GCgroup memory swap accounting warning.
	MissingCGroupMemorySwapAccounting
	// ClusterTimeSkew represents the cluster time skew warning.
	ClusterTimeSkew
	// AppArmorNotAvailable represents the AppArmor not available warning.
	AppArmorNotAvailable
	// MissingVirtiofsd represents the missing virtiofsd warning.
	MissingVirtiofsd
	// AppArmorDisabledDueToRawDnsmasq represents the disabled AppArmor due to raw.dnsmasq warning.
	AppArmorDisabledDueToRawDnsmasq
	// LargerIPv6PrefixThanSupported represents the larger IPv6 prefix than supported warning.
	LargerIPv6PrefixThanSupported
	// ProxyBridgeNetfilterNotEnabled represents the proxy bridge netfilter not enable warning.
	ProxyBridgeNetfilterNotEnabled
	// NetworkUnvailable represents a network that cannot be initialized on the local server.
	NetworkUnvailable
	// OfflineClusterMember represents the offline cluster members warning.
	OfflineClusterMember
	// InstanceAutostartFailure represents the failure of instance autostart process after three retries.
	InstanceAutostartFailure
	// InstanceTypeNotOperational represents the lack of support for an instance driver.
	InstanceTypeNotOperational
	// StoragePoolUnvailable represents a storage pool that cannot be initialized on the local server.
	StoragePoolUnvailable
	// UnableToUpdateClusterCertificate represents the unable to update cluster certificate warning.
	UnableToUpdateClusterCertificate
)

// TypeNames associates a warning code to its name.
var TypeNames = map[Type]string{
	Undefined:                         "Undefined warning",
	MissingCGroupBlkio:                "Couldn't find the CGroup blkio",
	MissingCGroupBlkioWeight:          "Couldn't find the CGroup blkio.weight",
	MissingCGroupCPUController:        "Couldn't find the CGroup CPU controller",
	MissingCGroupCPUsetController:     "Couldn't find the CGroup CPUset controller",
	MissingCGroupCPUacctController:    "Couldn't find the CGroup CPUacct controller",
	MissingCGroupDevicesController:    "Couldn't find the CGroup devices controller",
	MissingCGroupFreezerController:    "Couldn't find the CGroup freezer controller",
	MissingCGroupHugetlbController:    "Couldn't find the CGroup hugetlb controller",
	MissingCGroupMemoryController:     "Couldn't find the CGroup memory controller",
	MissingCGroupPidsController:       "Couldn't find the CGroup pids controller",
	MissingCGroupMemorySwapAccounting: "Couldn't find the CGroup memory swap accounting",
	ClusterTimeSkew:                   "Time skew detected between leader and local",
	AppArmorNotAvailable:              "AppArmor support has been disabled",
	MissingVirtiofsd:                  "Missing virtiofsd",
	AppArmorDisabledDueToRawDnsmasq:   "Skipping AppArmor for dnsmasq due to raw.dnsmasq being set",
	LargerIPv6PrefixThanSupported:     "IPv6 networks with a prefix larger than 64 aren't properly supported by dnsmasq",
	ProxyBridgeNetfilterNotEnabled:    "Proxy bridge netfilter not enabled",
	NetworkUnvailable:                 "Network unavailable",
	OfflineClusterMember:              "Offline cluster member",
	InstanceAutostartFailure:          "Failed to autostart instance",
	InstanceTypeNotOperational:        "Instance type not operational",
	StoragePoolUnvailable:             "Storage pool unavailable",
	UnableToUpdateClusterCertificate:  "Unable to update cluster certificate",
}

// Severity returns the severity of the warning type.
func (t Type) Severity() Severity {
	switch t {
	case Undefined:
		return SeverityLow
	case MissingCGroupBlkio:
		return SeverityLow
	case MissingCGroupBlkioWeight:
		return SeverityLow
	case MissingCGroupCPUController:
		return SeverityLow
	case MissingCGroupCPUsetController:
		return SeverityLow
	case MissingCGroupCPUacctController:
		return SeverityLow
	case MissingCGroupDevicesController:
		return SeverityLow
	case MissingCGroupFreezerController:
		return SeverityLow
	case MissingCGroupHugetlbController:
		return SeverityLow
	case MissingCGroupMemoryController:
		return SeverityLow
	case MissingCGroupPidsController:
		return SeverityLow
	case MissingCGroupMemorySwapAccounting:
		return SeverityLow
	case ClusterTimeSkew:
		return SeverityLow
	case AppArmorNotAvailable:
		return SeverityLow
	case MissingVirtiofsd:
		return SeverityLow
	case AppArmorDisabledDueToRawDnsmasq:
		return SeverityLow
	case LargerIPv6PrefixThanSupported:
		return SeverityLow
	case ProxyBridgeNetfilterNotEnabled:
		return SeverityLow
	case NetworkUnvailable:
		return SeverityHigh
	case OfflineClusterMember:
		return SeverityLow
	case InstanceAutostartFailure:
		return SeverityLow
	case InstanceTypeNotOperational:
		return SeverityLow
	case StoragePoolUnvailable:
		return SeverityHigh
	case UnableToUpdateClusterCertificate:
		return SeverityLow
	}

	return SeverityLow
}
