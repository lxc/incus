package dhcpalloc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net"
	"os"

	"github.com/mdlayher/netx/eui64"

	"github.com/lxc/incus/internal/iprange"
	"github.com/lxc/incus/internal/server/dnsmasq"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
)

// ErrDHCPNotSupported indicates network doesn't support DHCP for this IP protocol.
var ErrDHCPNotSupported error = errors.New("Network doesn't support DHCP")

// DHCPValidIP returns whether an IP fits inside one of the supplied DHCP ranges and subnet.
func DHCPValidIP(subnet *net.IPNet, ranges []iprange.Range, IP net.IP) bool {
	inSubnet := subnet.Contains(IP)
	if !inSubnet {
		return false
	}

	if len(ranges) > 0 {
		for _, IPRange := range ranges {
			if bytes.Compare(IP, IPRange.Start) >= 0 && bytes.Compare(IP, IPRange.End) <= 0 {
				return true
			}
		}
	} else if inSubnet {
		return true
	}

	return false
}

// GetIP returns a net.IP representing the IP belonging to the subnet for the host number supplied.
func GetIP(subnet *net.IPNet, host int64) net.IP {
	// Convert IP to a big int.
	bigIP := big.NewInt(0)
	bigIP.SetBytes(subnet.IP.To16())

	// Deal with negative offsets.
	bigHost := big.NewInt(host)
	bigCount := big.NewInt(host)
	if host < 0 {
		mask, size := subnet.Mask.Size()

		bigHosts := big.NewFloat(0)
		bigHosts.SetFloat64((math.Pow(2, float64(size-mask))))
		bigHostsInt, _ := bigHosts.Int(nil)

		bigCount.Set(bigHostsInt)
		bigCount.Add(bigCount, bigHost)
	}

	// Get the new IP int.
	bigIP.Add(bigIP, bigCount)

	// Generate an IPv6.
	if subnet.IP.To4() == nil {
		newIP := bigIP.Bytes()
		return newIP
	}

	// Generate an IPv4.
	newIP := make(net.IP, 4)
	binary.BigEndian.PutUint32(newIP, uint32(bigIP.Int64()))
	return newIP
}

// Network represents an Incus network responsible for running dnsmasq.
type Network interface {
	Name() string
	Type() string
	Config() map[string]string
	DHCPv4Subnet() *net.IPNet
	DHCPv6Subnet() *net.IPNet
	DHCPv4Ranges() []iprange.Range
	DHCPv6Ranges() []iprange.Range
}

// Options to initialize the allocator with.
type Options struct {
	ProjectName string
	HostName    string
	DeviceName  string
	HostMAC     net.HardwareAddr
	Network     Network
}

// Transaction is a locked transaction of the dnsmasq config files that allows IP allocations for a host.
type Transaction struct {
	opts              *Options
	currentDHCPMAC    net.HardwareAddr
	currentDHCPv4     dnsmasq.DHCPAllocation
	currentDHCPv6     dnsmasq.DHCPAllocation
	allocationsDHCPv4 map[[4]byte]dnsmasq.DHCPAllocation
	allocationsDHCPv6 map[[16]byte]dnsmasq.DHCPAllocation
	allocatedIPv4     net.IP
	allocatedIPv6     net.IP
}

// AllocateIPv4 allocate an IPv4 static DHCP allocation.
func (t *Transaction) AllocateIPv4() (net.IP, error) {
	var err error

	// Should have a (at least empty) map if DHCP is supported.
	if t.allocationsDHCPv4 == nil {
		return nil, ErrDHCPNotSupported
	}

	dhcpSubnet := t.opts.Network.DHCPv4Subnet()
	if dhcpSubnet == nil {
		return nil, ErrDHCPNotSupported
	}

	// Check the existing allocated IP is still valid in the network's subnet & ranges, if not then
	// we'll need to generate a new one.
	if t.allocatedIPv4 != nil {
		ranges := t.opts.Network.DHCPv4Ranges()
		if !DHCPValidIP(dhcpSubnet, ranges, t.allocatedIPv4.To4()) {
			t.allocatedIPv4 = nil // We need a new IP allocated.
		}
	}

	// Allocate a new IPv4 address if needed.
	if t.allocatedIPv4 == nil {
		t.allocatedIPv4, err = t.getDHCPFreeIPv4(t.allocationsDHCPv4, t.opts.HostName, t.opts.HostMAC)
		if err != nil {
			return nil, err
		}
	}

	return t.allocatedIPv4, nil
}

// AllocateIPv6 allocate an IPv6 static DHCP allocation.
func (t *Transaction) AllocateIPv6() (net.IP, error) {
	var err error

	// Should have a (at least empty) map if DHCP is supported.
	if t.allocationsDHCPv6 == nil {
		return nil, ErrDHCPNotSupported
	}

	dhcpSubnet := t.opts.Network.DHCPv6Subnet()
	if dhcpSubnet == nil {
		return nil, ErrDHCPNotSupported
	}

	// Check the existing allocated IP is still valid in the network's subnet & ranges, if not then
	// we'll need to generate a new one.
	if t.allocatedIPv6 != nil {
		ranges := t.opts.Network.DHCPv6Ranges()
		if !DHCPValidIP(dhcpSubnet, ranges, t.allocatedIPv6.To16()) {
			t.allocatedIPv6 = nil // We need a new IP allocated.
		}
	}

	// Allocate a new IPv6 address if needed.
	if t.allocatedIPv6 == nil {
		t.allocatedIPv6, err = t.getDHCPFreeIPv6(t.allocationsDHCPv6, t.opts.HostName, t.opts.HostMAC)
		if err != nil {
			return nil, err
		}
	}

	return t.allocatedIPv6, nil
}

// getDHCPFreeIPv4 attempts to find a free IPv4 address for the device.
// It first checks whether there is an existing allocation for the instance.
// If no previous allocation, then a free IP is picked from the ranges configured.
func (t *Transaction) getDHCPFreeIPv4(usedIPs map[[4]byte]dnsmasq.DHCPAllocation, deviceStaticFileName string, mac net.HardwareAddr) (net.IP, error) {
	ip, subnet, err := net.ParseCIDR(t.opts.Network.Config()["ipv4.address"])
	if err != nil {
		return nil, err
	}

	dhcpRanges := t.opts.Network.DHCPv4Ranges()

	// Lets see if there is already an allocation for our device and that it sits within subnet.
	// If there are custom DHCP ranges defined, check also that the IP falls within one of the ranges.
	for _, DHCP := range usedIPs {
		if (deviceStaticFileName == DHCP.StaticFileName || bytes.Equal(mac, DHCP.MAC)) && DHCPValidIP(subnet, dhcpRanges, DHCP.IP) {
			return DHCP.IP, nil
		}
	}

	// If no custom ranges defined, convert subnet pool to a range.
	if len(dhcpRanges) <= 0 {
		dhcpRanges = append(dhcpRanges, iprange.Range{
			Start: GetIP(subnet, 1).To4(),
			End:   GetIP(subnet, -2).To4()},
		)
	}

	// If no valid existing allocation found, try and find a free one in the subnet pool/ranges.
	for _, IPRange := range dhcpRanges {
		inc := big.NewInt(1)
		startBig := big.NewInt(0)
		startBig.SetBytes(IPRange.Start)
		endBig := big.NewInt(0)
		endBig.SetBytes(IPRange.End)

		for {
			if startBig.Cmp(endBig) >= 0 {
				break
			}

			IP := net.IP(startBig.Bytes())

			// Check IP generated is not Incus's IP.
			if IP.Equal(ip) {
				startBig.Add(startBig, inc)
				continue
			}

			// Check IP is not already allocated.
			var IPKey [4]byte
			copy(IPKey[:], IP.To4())

			_, inUse := usedIPs[IPKey]
			if inUse {
				startBig.Add(startBig, inc)
				continue
			}

			return IP, nil
		}
	}

	return nil, fmt.Errorf("No available IP could not be found")
}

// getDHCPFreeIPv6 attempts to find a free IPv6 address for the device.
// It first checks whether there is an existing allocation for the instance. Due to the limitations
// of dnsmasq lease file format, we can only search for previous static allocations.
// If no previous allocation, then if SLAAC (stateless) mode is enabled on the network, or if
// DHCPv6 stateful mode is enabled without custom ranges, then an EUI64 IP is generated from the
// device's MAC address. Finally if stateful custom ranges are enabled, then a free IP is picked
// from the ranges configured.
func (t *Transaction) getDHCPFreeIPv6(usedIPs map[[16]byte]dnsmasq.DHCPAllocation, deviceStaticFileName string, mac net.HardwareAddr) (net.IP, error) {
	ip, subnet, err := net.ParseCIDR(t.opts.Network.Config()["ipv6.address"])
	if err != nil {
		return nil, err
	}

	dhcpRanges := t.opts.Network.DHCPv6Ranges()

	// Lets see if there is already an allocation for our device and that it sits within subnet.
	// Because of dnsmasq's lease file format we can only match safely against static
	// allocations using instance name. If there are custom DHCP ranges defined, check also
	// that the IP falls within one of the ranges.
	for _, DHCP := range usedIPs {
		if deviceStaticFileName == DHCP.StaticFileName && DHCPValidIP(subnet, dhcpRanges, DHCP.IP) {
			return DHCP.IP, nil
		}
	}

	netConfig := t.opts.Network.Config()

	// Try using an EUI64 IP when in either SLAAC or DHCPv6 stateful mode without custom ranges.
	if util.IsFalseOrEmpty(netConfig["ipv6.dhcp.stateful"]) || netConfig["ipv6.dhcp.ranges"] == "" {
		IP, err := eui64.ParseMAC(subnet.IP, mac)
		if err != nil {
			return nil, err
		}

		// Check IP is not already allocated and not the Incus IP.
		var IPKey [16]byte
		copy(IPKey[:], IP.To16())
		_, inUse := usedIPs[IPKey]
		if !inUse && !IP.Equal(ip) {
			return IP, nil
		}
	}

	// If no custom ranges defined, convert subnet pool to a range.
	if len(dhcpRanges) <= 0 {
		dhcpRanges = append(dhcpRanges, iprange.Range{
			Start: GetIP(subnet, 1).To16(),
			End:   GetIP(subnet, -1).To16()},
		)
	}

	// If we get here, then someone already has our SLAAC IP, or we are using custom ranges.
	// Try and find a free one in the subnet pool/ranges.
	for _, IPRange := range dhcpRanges {
		inc := big.NewInt(1)
		startBig := big.NewInt(0)
		startBig.SetBytes(IPRange.Start)
		endBig := big.NewInt(0)
		endBig.SetBytes(IPRange.End)

		for {
			if startBig.Cmp(endBig) >= 0 {
				break
			}

			IP := net.IP(startBig.Bytes())

			// Check IP generated is not Incus's IP.
			if IP.Equal(ip) {
				startBig.Add(startBig, inc)
				continue
			}

			// Check IP is not already allocated.
			var IPKey [16]byte
			copy(IPKey[:], IP.To16())

			_, inUse := usedIPs[IPKey]
			if inUse {
				startBig.Add(startBig, inc)
				continue
			}

			return IP, nil
		}
	}

	return nil, fmt.Errorf("No available IP could not be found")
}

// AllocateTask initializes a new locked Transaction for a specific host and executes the supplied function on it.
// The lock on the dnsmasq config is released when the function returns.
func AllocateTask(opts *Options, f func(*Transaction) error) error {
	l := logger.AddContext(logger.Ctx{"driver": opts.Network.Type(), "network": opts.Network.Name(), "project": opts.ProjectName, "host": opts.HostName})

	dnsmasq.ConfigMutex.Lock()
	defer dnsmasq.ConfigMutex.Unlock()

	var err error
	t := &Transaction{opts: opts}

	// Read current static IP allocation configured from dnsmasq host config (if exists).
	deviceStaticFileName := dnsmasq.StaticAllocationFileName(opts.ProjectName, opts.HostName, opts.DeviceName)
	t.currentDHCPMAC, t.currentDHCPv4, t.currentDHCPv6, err = dnsmasq.DHCPStaticAllocation(opts.Network.Name(), deviceStaticFileName)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Set the current allocated IPs internally (these may be changed later), and if they are it will trigger
	// a dnsmasq static host config file rebuild.
	t.allocatedIPv4 = t.currentDHCPv4.IP
	t.allocatedIPv6 = t.currentDHCPv6.IP

	// Get all existing allocations in network if leases file exists. If not then we will detect this later
	// due to the existing allocations maps being nil.
	if util.PathExists(internalUtil.VarPath("networks", opts.Network.Name(), "dnsmasq.leases")) {
		t.allocationsDHCPv4, t.allocationsDHCPv6, err = dnsmasq.DHCPAllAllocations(opts.Network.Name())
		if err != nil {
			return err
		}
	}

	// Run the supplied allocation function.
	err = f(t)
	if err != nil {
		return err
	}

	// If the user's function didn't fail despite DHCP allocation not being available for either protocol,
	// then presumably they ignored allocation errors. So fail here, rather than try to write the new config
	// out, which will fail with a less helpful error about missing paths.
	if t.allocationsDHCPv4 == nil && t.allocationsDHCPv6 == nil {
		return ErrDHCPNotSupported
	}

	// If MAC or either IPv4 or IPv6 assigned is different than what is in dnsmasq config, rebuild config.
	macChanged := !bytes.Equal(opts.HostMAC, t.currentDHCPMAC)
	ipv4Changed := t.allocatedIPv4 != nil && !t.currentDHCPv4.IP.Equal(t.allocatedIPv4.To4())
	ipv6Changed := t.allocatedIPv6 != nil && !t.currentDHCPv6.IP.Equal(t.allocatedIPv6.To16())

	if macChanged || ipv4Changed || ipv6Changed {
		var IPv4Str, IPv6Str string

		if t.allocatedIPv4 != nil {
			IPv4Str = t.allocatedIPv4.String()
		}

		if t.allocatedIPv6 != nil {
			IPv6Str = t.allocatedIPv6.String()
		}

		// Write out new dnsmasq static host allocation config file.
		err = dnsmasq.UpdateStaticEntry(opts.Network.Name(), opts.ProjectName, opts.HostName, opts.DeviceName, opts.Network.Config(), opts.HostMAC.String(), IPv4Str, IPv6Str)
		if err != nil {
			return err
		}

		l.Debug("Updated static DHCP entry", logger.Ctx{"mac": opts.HostMAC.String(), "IPv4": IPv4Str, "IPv6": IPv6Str})

		// Reload dnsmasq.
		err = dnsmasq.Kill(opts.Network.Name(), true)
		if err != nil {
			return err
		}
	}

	return nil
}
