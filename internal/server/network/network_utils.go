package network

import (
	"bufio"
	"bytes"
	"context"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"math/rand"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/v6/internal/iprange"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/device/nictype"
	"github.com/lxc/incus/v6/internal/server/dnsmasq"
	"github.com/lxc/incus/v6/internal/server/dnsmasq/dhcpalloc"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/ip"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/state"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

func networkValidPort(value string) error {
	if value == "" {
		return nil
	}

	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("Invalid value for an integer: %s", value)
	}

	if valueInt < 1 || valueInt > 65536 {
		return fmt.Errorf("Invalid port number: %s", value)
	}

	return nil
}

// RandomDevName returns a random device name with prefix.
// If the random string combined with the prefix exceeds 13 characters then empty string is returned.
// This is to ensure we support buggy dhclient applications: https://bugs.debian.org/cgi-bin/bugreport.cgi?bug=858580
func RandomDevName(prefix string) string {
	// Return a new random veth device name.
	randBytes := make([]byte, 4)
	_, _ = cryptoRand.Read(randBytes)
	iface := prefix + hex.EncodeToString(randBytes)
	if len(iface) > 13 {
		return ""
	}

	return iface
}

// MACDevName returns interface name with prefix 'inc' and MAC without leading 2 digits.
func MACDevName(mac net.HardwareAddr) string {
	devName := strings.Join(strings.Split(mac.String(), ":"), "")
	return fmt.Sprintf("inc%s", devName[2:])
}

// UsedByInstanceDevices looks for instance NIC devices using the network and runs the supplied usageFunc for each.
// Accepts optional filter arguments to specify a subset of instances.
func UsedByInstanceDevices(s *state.State, networkProjectName string, networkName string, networkType string, usageFunc func(inst db.InstanceArgs, nicName string, nicConfig map[string]string) error, filters ...cluster.InstanceFilter) error {
	// Get the instances.
	projects := map[string]api.Project{}
	instances := []db.InstanceArgs{}

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
			projects[inst.Project] = p
			instances = append(instances, inst)

			return nil
		}, filters...)
	})
	if err != nil {
		return err
	}

	// Go through the instances and run usageFunc.
	for _, inst := range instances {
		p := projects[inst.Project]

		// Get the instance's effective network project name.
		instNetworkProject := project.NetworkProjectFromRecord(&p)

		// Skip instances who's effective network project doesn't match this Network's project.
		if instNetworkProject != networkProjectName {
			return nil
		}

		// Look for NIC devices using this network.
		devices := db.ExpandInstanceDevices(inst.Devices.Clone(), inst.Profiles)
		for devName, devConfig := range devices {
			if isInUseByDevice(networkName, networkType, devConfig) {
				err := usageFunc(inst, devName, devConfig)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// UsedBy returns list of API resources using network. Accepts firstOnly argument to indicate that only the first
// resource using network should be returned. This can help to quickly check if the network is in use.
func UsedBy(s *state.State, networkProjectName string, networkID int64, networkName string, networkType string, firstOnly bool) ([]string, error) {
	var err error
	var usedBy []string

	// If managed network being passed in, check if it has any peerings in a created state.
	if networkID > 0 {
		var peers map[int64]*api.NetworkPeer

		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			peers, err = tx.GetNetworkPeers(ctx, networkID)

			return err
		})
		if err != nil {
			return nil, fmt.Errorf("Failed getting network peers: %w", err)
		}

		for _, peer := range peers {
			if peer.Status == api.NetworkStatusCreated {
				// Add the target project/network of the peering as using this network.
				usedBy = append(usedBy, api.NewURL().Path(version.APIVersion, "networks", peer.TargetNetwork).Project(peer.TargetProject).String())

				if firstOnly {
					return usedBy, nil
				}
			}
		}
	}

	// Only networks defined in the default project can be used by other networks. Cheapest to do.
	if networkProjectName == api.ProjectDefaultName {
		// Get all managed networks across all projects.
		var projectNetworks map[string]map[int64]api.Network

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			projectNetworks, err = tx.GetCreatedNetworks(ctx)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to load all networks: %w", err)
		}

		for projectName, networks := range projectNetworks {
			for _, network := range networks {
				if networkName == network.Name && networkProjectName == projectName {
					continue // Skip ourselves.
				}

				// The network's config references the network we are searching for. Either by
				// directly referencing our network or by referencing our interface as its parent.
				if network.Config["network"] == networkName || network.Config["parent"] == networkName {
					usedBy = append(usedBy, api.NewURL().Path(version.APIVersion, "networks", network.Name).Project(projectName).String())

					if firstOnly {
						return usedBy, nil
					}
				}
			}
		}
	}

	// Look for profiles. Next cheapest to do.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		profiles, err := cluster.GetProfiles(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, profile := range profiles {
			profileDevices, err := cluster.GetProfileDevices(ctx, tx.Tx(), profile.ID)
			if err != nil {
				return err
			}

			profileProject, err := cluster.GetProject(ctx, tx.Tx(), profile.Project)
			if err != nil {
				return err
			}

			apiProfileProject, err := profileProject.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			inUse, err := usedByProfileDevices(s, profileDevices, apiProfileProject, networkProjectName, networkName, networkType)
			if err != nil {
				return err
			}

			if inUse {
				usedBy = append(usedBy, api.NewURL().Path(version.APIVersion, "profiles", profile.Name).Project(profile.Project).String())

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

	// Check if any instance devices use this network.
	err = UsedByInstanceDevices(s, networkProjectName, networkName, networkType, func(inst db.InstanceArgs, nicName string, nicConfig map[string]string) error {
		usedBy = append(usedBy, api.NewURL().Path(version.APIVersion, "instances", inst.Name).Project(inst.Project).String())

		if firstOnly {
			// No need to consider other devices.
			return db.ErrInstanceListStop
		}

		return nil
	})
	if err != nil {
		if err == db.ErrInstanceListStop {
			return usedBy, nil
		}

		return nil, err
	}

	return usedBy, nil
}

// usedByProfileDevices indicates if network is referenced by a profile's NIC devices.
// Checks if the device's parent or network properties match the network name.
func usedByProfileDevices(s *state.State, profileDevices map[string]cluster.Device, profileProject *api.Project, networkProjectName string, networkName string, networkType string) (bool, error) {
	// Get the translated network project name from the profiles's project.

	// Skip profiles who's translated network project doesn't match the requested network's project.
	// Because its devices can't be using this network.
	profileNetworkProjectName := project.NetworkProjectFromRecord(profileProject)
	if networkProjectName != profileNetworkProjectName {
		return false, nil
	}

	for _, d := range deviceConfig.NewDevices(cluster.DevicesToAPI(profileDevices)) {
		if isInUseByDevice(networkName, networkType, d) {
			return true, nil
		}
	}

	return false, nil
}

// isInUseByDevices inspects a device's config to find references for a network being used.
func isInUseByDevice(networkName string, networkType string, d deviceConfig.Device) bool {
	if d["type"] != "nic" {
		return false
	}

	if d["network"] != "" && d["network"] == networkName {
		return true
	}

	// OVN networks can only use managed networks.
	if networkType == "ovn" {
		return false
	}

	if d["parent"] != "" && GetHostDevice(d["parent"], d["vlan"]) == networkName {
		return true
	}

	return false
}

// GetDevMTU retrieves the current MTU setting for a named network device.
func GetDevMTU(devName string) (uint32, error) {
	content, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/mtu", devName))
	if err != nil {
		return 0, err
	}

	// Parse value
	mtu, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 32)
	if err != nil {
		return 0, err
	}

	return uint32(mtu), nil
}

// GetTXQueueLength retrieves the current txqlen setting for a named network device.
func GetTXQueueLength(devName string) (uint32, error) {
	content, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/tx_queue_len", devName))
	if err != nil {
		return 0, err
	}

	// Parse value
	txqlen, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 32)
	if err != nil {
		return 0, err
	}

	return uint32(txqlen), nil
}

// DefaultGatewaySubnetV4 returns subnet of default gateway interface.
func DefaultGatewaySubnetV4() (*net.IPNet, string, error) {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return nil, "", err
	}

	defer func() { _ = file.Close() }()

	ifaceName := ""

	scanner := bufio.NewReader(file)
	for {
		line, _, err := scanner.ReadLine()
		if err != nil {
			break
		}

		fields := strings.Fields(string(line))

		if fields[1] == "00000000" && fields[7] == "00000000" {
			ifaceName = fields[0]
			break
		}
	}

	if ifaceName == "" {
		return nil, "", fmt.Errorf("No default gateway for IPv4")
	}

	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return nil, "", err
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, "", err
	}

	var subnet *net.IPNet

	for _, addr := range addrs {
		addrIP, addrNet, err := net.ParseCIDR(addr.String())
		if err != nil {
			return nil, "", err
		}

		if addrIP.To4() == nil {
			continue
		}

		if subnet != nil {
			return nil, "", fmt.Errorf("More than one IPv4 subnet on default interface")
		}

		subnet = addrNet
	}

	if subnet == nil {
		return nil, "", fmt.Errorf("No IPv4 subnet on default interface")
	}

	return subnet, ifaceName, nil
}

// UpdateDNSMasqStatic rebuilds the DNSMasq static allocations.
func UpdateDNSMasqStatic(s *state.State, networkName string) error {
	// We don't want to race with ourselves here.
	dnsmasq.ConfigMutex.Lock()
	defer dnsmasq.ConfigMutex.Unlock()

	// Get all the networks.
	var networks []string
	if networkName == "" {
		var err error

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Pass api.ProjectDefaultName here, as currently dnsmasq (bridged) networks do not support projects.
			networks, err = tx.GetNetworks(ctx, api.ProjectDefaultName)

			return err
		})
		if err != nil {
			return err
		}
	} else {
		networks = []string{networkName}
	}

	// Get all the instances.
	insts, err := instance.LoadNodeAll(s, instancetype.Any)
	if err != nil {
		return err
	}

	// Build a list of dhcp host entries.
	entries := map[string][][]string{}
	for _, inst := range insts {
		// Go through all its devices (including profiles).
		for deviceName, d := range inst.ExpandedDevices() {
			// Skip uninteresting entries.
			if d["type"] != "nic" {
				continue
			}

			nicType, err := nictype.NICType(s, inst.Project().Name, d)
			if err != nil || nicType != "bridged" {
				continue
			}

			// Temporarily populate parent from network setting if used.
			if d["network"] != "" {
				d["parent"] = d["network"]
			}

			// Skip devices not connected to managed networks.
			if !slices.Contains(networks, d["parent"]) {
				continue
			}

			// Fill in the hwaddr from volatile.
			d, err = inst.FillNetworkDevice(deviceName, d)
			if err != nil {
				continue
			}

			// Add the new host entries.
			_, ok := entries[d["parent"]]
			if !ok {
				entries[d["parent"]] = [][]string{}
			}

			if (util.IsTrue(d["security.ipv4_filtering"]) && d["ipv4.address"] == "") || (util.IsTrue(d["security.ipv6_filtering"]) && d["ipv6.address"] == "") {
				deviceStaticFileName := dnsmasq.StaticAllocationFileName(inst.Project().Name, inst.Name(), deviceName)
				_, curIPv4, curIPv6, err := dnsmasq.DHCPStaticAllocation(d["parent"], deviceStaticFileName)
				if err != nil && !os.IsNotExist(err) {
					return err
				}

				if d["ipv4.address"] == "" && curIPv4.IP != nil {
					d["ipv4.address"] = curIPv4.IP.String()
				}

				if d["ipv6.address"] == "" && curIPv6.IP != nil {
					d["ipv6.address"] = curIPv6.IP.String()
				}
			}

			entries[d["parent"]] = append(entries[d["parent"]], []string{d["hwaddr"], inst.Project().Name, inst.Name(), d["ipv4.address"], d["ipv6.address"], deviceName})
		}
	}

	// Update the host files.
	for _, network := range networks {
		entries := entries[network]

		// Skip networks we don't manage (or don't have DHCP enabled).
		if !util.PathExists(internalUtil.VarPath("networks", network, "dnsmasq.pid")) {
			continue
		}

		// Pass api.ProjectDefaultName here, as currently dnsmasq (bridged) networks do not support projects.
		n, err := LoadByName(s, api.ProjectDefaultName, network)
		if err != nil {
			return fmt.Errorf("Failed to load network %q in project %q for dnsmasq update: %w", api.ProjectDefaultName, network, err)
		}

		config := n.Config()

		// Wipe everything clean.
		files, err := os.ReadDir(internalUtil.VarPath("networks", network, "dnsmasq.hosts"))
		if err != nil {
			return err
		}

		for _, entry := range files {
			err = os.Remove(internalUtil.VarPath("networks", network, "dnsmasq.hosts", entry.Name()))
			if err != nil {
				return err
			}
		}

		// Apply the changes.
		for entryIdx, entry := range entries {
			hwaddr := entry[0]
			projectName := entry[1]
			cName := entry[2]
			ipv4Address := entry[3]
			ipv6Address := entry[4]
			deviceName := entry[5]
			line := hwaddr

			// Look for duplicates.
			duplicate := false
			for iIdx, i := range entries {
				if project.Instance(entry[1], entry[2]) == project.Instance(i[1], i[2]) {
					// Skip ourselves.
					continue
				}

				if entry[0] == i[0] {
					// Find broken configurations
					logger.Errorf("Duplicate MAC detected: %s and %s", project.Instance(entry[1], entry[2]), project.Instance(i[1], i[2]))
				}

				if i[3] == "" && i[4] == "" {
					// Skip unconfigured.
					continue
				}

				if entry[3] == i[3] && entry[4] == i[4] {
					// Find identical containers (copies with static configuration).
					if entryIdx > iIdx {
						duplicate = true
					} else {
						line = fmt.Sprintf("%s,%s", line, i[0])
						logger.Debugf("Found containers with duplicate IPv4/IPv6: %s and %s", project.Instance(entry[1], entry[2]), project.Instance(i[1], i[2]))
					}
				}
			}

			if duplicate {
				continue
			}

			// Generate the dhcp-host line.
			err := dnsmasq.UpdateStaticEntry(network, projectName, cName, deviceName, config, hwaddr, ipv4Address, ipv6Address)
			if err != nil {
				return err
			}
		}

		// Signal dnsmasq.
		err = dnsmasq.Kill(network, true)
		if err != nil {
			return err
		}
	}

	return nil
}

func randomSubnetV4() (string, error) {
	for i := 0; i < 100; i++ {
		cidr := fmt.Sprintf("10.%d.%d.1/24", rand.Intn(255), rand.Intn(255))
		_, subnet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}

		if inRoutingTable(subnet) {
			continue
		}

		if pingSubnet(subnet) {
			continue
		}

		return cidr, nil
	}

	return "", fmt.Errorf("Failed to automatically find an unused IPv4 subnet, manual configuration required")
}

func randomSubnetV6() (string, error) {
	for i := 0; i < 100; i++ {
		cidr := fmt.Sprintf("fd42:%x:%x:%x::1/64", rand.Intn(65535), rand.Intn(65535), rand.Intn(65535))
		_, subnet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}

		if inRoutingTable(subnet) {
			continue
		}

		if pingSubnet(subnet) {
			continue
		}

		return cidr, nil
	}

	return "", fmt.Errorf("Failed to automatically find an unused IPv6 subnet, manual configuration required")
}

func inRoutingTable(subnet *net.IPNet) bool {
	filename := "route"
	if subnet.IP.To4() == nil {
		filename = "ipv6_route"
	}

	file, err := os.Open(fmt.Sprintf("/proc/net/%s", filename))
	if err != nil {
		return false
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewReader(file)
	for {
		line, _, err := scanner.ReadLine()
		if err != nil {
			break
		}

		fields := strings.Fields(string(line))

		// Get the IP
		var ip net.IP
		if filename == "ipv6_route" {
			ip, err = hex.DecodeString(fields[0])
			if err != nil {
				continue
			}
		} else {
			bytes, err := hex.DecodeString(fields[1])
			if err != nil {
				continue
			}

			ip = net.IPv4(bytes[3], bytes[2], bytes[1], bytes[0])
		}

		// Get the mask
		var mask net.IPMask
		if filename == "ipv6_route" {
			size, err := strconv.ParseInt(fmt.Sprintf("0x%s", fields[1]), 0, 64)
			if err != nil {
				continue
			}

			mask = net.CIDRMask(int(size), 128)
		} else {
			bytes, err := hex.DecodeString(fields[7])
			if err != nil {
				continue
			}

			mask = net.IPv4Mask(bytes[3], bytes[2], bytes[1], bytes[0])
		}

		// Generate a new network
		lineNet := net.IPNet{IP: ip, Mask: mask}

		// Ignore default gateway
		if lineNet.IP.Equal(net.ParseIP("::")) {
			continue
		}

		if lineNet.IP.Equal(net.ParseIP("0.0.0.0")) {
			continue
		}

		// Check if we have a route to our new subnet
		if lineNet.Contains(subnet.IP) {
			return true
		}
	}

	return false
}

// pingIP sends a single ping packet to the specified IP, returns nil error if IP is reachable.
// If ctx doesn't have a deadline then the default timeout used is 1s.
func pingIP(ctx context.Context, ip net.IP) error {
	cmd := "ping"
	if ip.To4() == nil {
		cmd = "ping6"
	}

	timeout := time.Second * 1
	deadline, ok := ctx.Deadline()
	if ok {
		timeout = time.Until(deadline)
	}

	_, err := subprocess.RunCommandContext(ctx, cmd, "-n", "-q", ip.String(), "-c", "1", "-w", fmt.Sprintf("%d", int(timeout.Seconds())))

	return err
}

func pingSubnet(subnet *net.IPNet) bool {
	var fail bool
	var failLock sync.Mutex
	var wgChecks sync.WaitGroup

	ping := func(ip net.IP) {
		defer wgChecks.Done()

		if pingIP(context.TODO(), ip) != nil {
			return
		}

		// Remote answered
		failLock.Lock()
		fail = true
		failLock.Unlock()
	}

	poke := func(ip net.IP) {
		defer wgChecks.Done()

		addr := fmt.Sprintf("%s:22", ip.String())
		if ip.To4() == nil {
			addr = fmt.Sprintf("[%s]:22", ip.String())
		}

		_, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			// Remote answered
			failLock.Lock()
			fail = true
			failLock.Unlock()
			return
		}
	}

	// Ping first IP
	wgChecks.Add(1)
	go ping(dhcpalloc.GetIP(subnet, 1))

	// Poke port on first IP
	wgChecks.Add(1)
	go poke(dhcpalloc.GetIP(subnet, 1))

	// Ping check
	if subnet.IP.To4() != nil {
		// Ping last IP
		wgChecks.Add(1)
		go ping(dhcpalloc.GetIP(subnet, -2))

		// Poke port on last IP
		wgChecks.Add(1)
		go poke(dhcpalloc.GetIP(subnet, -2))
	}

	wgChecks.Wait()

	return fail
}

// GetHostDevice returns the interface name to use for a combination of parent device name and VLAN ID.
// If no vlan ID supplied, parent name is returned unmodified. If non-empty VLAN ID is supplied then it will look
// for an existing VLAN device and return that, otherwise it will return the default "parent.vlan" format as name.
func GetHostDevice(parent string, vlan string) string {
	// If no VLAN, just use the raw device
	if vlan == "" {
		return parent
	}

	// If no VLANs are configured, use the default pattern
	defaultVlan := fmt.Sprintf("%s.%s", parent, vlan)
	if !util.PathExists("/proc/net/vlan/config") {
		return defaultVlan
	}

	// Look for an existing VLAN
	f, err := os.Open("/proc/net/vlan/config")
	if err != nil {
		return defaultVlan
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Only grab the lines we're interested in
		s := strings.Split(scanner.Text(), "|")
		if len(s) != 3 {
			continue
		}

		vlanIface := strings.TrimSpace(s[0])
		vlanID := strings.TrimSpace(s[1])
		vlanParent := strings.TrimSpace(s[2])

		if vlanParent == parent && vlanID == vlan {
			return vlanIface
		}
	}

	// Return the default pattern
	return defaultVlan
}

// GetNeighbourIPs returns the IP addresses in the neighbour cache for a particular interface and MAC.
func GetNeighbourIPs(interfaceName string, hwaddr net.HardwareAddr) ([]ip.Neigh, error) {
	if hwaddr == nil {
		return nil, nil
	}

	neigh := &ip.Neigh{DevName: interfaceName, MAC: hwaddr}
	neighbours, err := neigh.Show()
	if err != nil {
		return nil, fmt.Errorf("Failed to get IP neighbours for interface %q: %w", interfaceName, err)
	}

	return neighbours, nil
}

// GetLeaseAddresses returns the lease addresses for a network and hwaddr.
func GetLeaseAddresses(networkName string, hwaddr string) ([]net.IP, error) {
	leaseFile := internalUtil.VarPath("networks", networkName, "dnsmasq.leases")
	if !util.PathExists(leaseFile) {
		return nil, fmt.Errorf("Leases file not found for network %q", networkName)
	}

	content, err := os.ReadFile(leaseFile)
	if err != nil {
		return nil, err
	}

	addresses := []net.IP{}

	for _, lease := range strings.Split(string(content), "\n") {
		fields := strings.Fields(lease)
		if len(fields) < 5 {
			continue
		}

		// Parse the MAC.
		mac := GetMACSlice(fields[1])
		macStr := strings.Join(mac, ":")

		if len(macStr) < 17 && fields[4] != "" {
			macStr = fields[4][len(fields[4])-17:]
		}

		if macStr != hwaddr {
			continue
		}

		// Parse the IP.
		ip := net.ParseIP(fields[2])
		if ip != nil {
			addresses = append(addresses, ip)
		}
	}

	return addresses, nil
}

// GetMACSlice parses MAC address.
func GetMACSlice(hwaddr string) []string {
	var buf []string

	if !strings.Contains(hwaddr, ":") {
		s, err := strconv.ParseUint(hwaddr, 10, 64)
		if err == nil {
			hwaddr = fmt.Sprintf("%x\n", s)
			var tuple string
			for i, r := range hwaddr {
				tuple = tuple + string(r)
				if i > 0 && (i+1)%2 == 0 {
					buf = append(buf, tuple)
					tuple = ""
				}
			}
		}
	} else {
		buf = strings.Split(strings.ToLower(hwaddr), ":")
	}

	return buf
}

// usesIPv4Firewall returns whether network config will need to use the IPv4 firewall.
func usesIPv4Firewall(netConfig map[string]string) bool {
	if netConfig == nil {
		return false
	}

	if util.IsTrueOrEmpty(netConfig["ipv4.firewall"]) {
		return true
	}

	if util.IsTrue(netConfig["ipv4.nat"]) {
		return true
	}

	return false
}

// usesIPv6Firewall returns whether network config will need to use the IPv6 firewall.
func usesIPv6Firewall(netConfig map[string]string) bool {
	if netConfig == nil {
		return false
	}

	if util.IsTrueOrEmpty(netConfig["ipv6.firewall"]) {
		return true
	}

	if util.IsTrue(netConfig["ipv6.nat"]) {
		return true
	}

	return false
}

// RandomHwaddr generates a random MAC address from the provided random source.
func randomHwaddr(r *rand.Rand) string {
	// Generate a new random MAC address using the usual prefix.
	ret := bytes.Buffer{}
	for _, c := range "00:16:3e:xx:xx:xx" {
		if c == 'x' {
			ret.WriteString(fmt.Sprintf("%x", r.Int31n(16)))
		} else {
			ret.WriteString(string(c))
		}
	}

	return ret.String()
}

// parseIPRange parses an IP range in the format "start-end" and converts it to a iprange.Range.
// If allowedNets are supplied, then each IP in the range is checked that it belongs to at least one of them.
// IPs in the range can be zero prefixed, e.g. "::1" or "0.0.0.1", however they should not overlap with any
// supplied allowedNets prefixes. If they are within an allowed network, any zero prefixed addresses are
// returned combined with the first allowed network they are within.
// If no allowedNets supplied they are returned as-is.
func parseIPRange(ipRange string, allowedNets ...*net.IPNet) (*iprange.Range, error) {
	inAllowedNet := func(ip net.IP, allowedNet *net.IPNet) net.IP {
		if ip == nil {
			return nil
		}

		ipv4 := ip.To4()

		// Only match IPv6 addresses against IPv6 networks.
		if ipv4 == nil && allowedNet.IP.To4() != nil {
			return nil
		}

		// Combine IP with network prefix if IP starts with a zero.
		// If IP is v4, then compare against 4-byte representation, otherwise use 16 byte representation.
		if (ipv4 != nil && ipv4[0] == 0) || (ipv4 == nil && ip[0] == 0) {
			allowedNet16 := allowedNet.IP.To16()
			ipCombined := make(net.IP, net.IPv6len)
			for i, b := range ip {
				ipCombined[i] = allowedNet16[i] | b
			}

			ip = ipCombined
		}

		// Check start IP is within one of the allowed networks.
		if !allowedNet.Contains(ip) {
			return nil
		}

		return ip
	}

	rangeParts := strings.SplitN(ipRange, "-", 2)
	if len(rangeParts) != 2 {
		return nil, fmt.Errorf("IP range %q must contain start and end IP addresses", ipRange)
	}

	startIP := net.ParseIP(rangeParts[0])
	endIP := net.ParseIP(rangeParts[1])

	if startIP == nil {
		return nil, fmt.Errorf("Start IP %q is invalid", rangeParts[0])
	}

	if endIP == nil {
		return nil, fmt.Errorf("End IP %q is invalid", rangeParts[1])
	}

	if bytes.Compare(startIP, endIP) > 0 {
		return nil, fmt.Errorf("Start IP %q must be less than End IP %q", startIP, endIP)
	}

	if len(allowedNets) > 0 {
		matchFound := false
		for _, allowedNet := range allowedNets {
			if allowedNet == nil {
				return nil, fmt.Errorf("Invalid allowed network")
			}

			combinedStartIP := inAllowedNet(startIP, allowedNet)
			if combinedStartIP == nil {
				continue
			}

			combinedEndIP := inAllowedNet(endIP, allowedNet)
			if combinedEndIP == nil {
				continue
			}

			// If both match then replace parsed IPs with combined IPs and stop searching.
			matchFound = true
			startIP = combinedStartIP
			endIP = combinedEndIP
			break
		}

		if !matchFound {
			return nil, fmt.Errorf("IP range %q does not fall within any of the allowed networks %v", ipRange, allowedNets)
		}
	}

	return &iprange.Range{
		Start: startIP,
		End:   endIP,
	}, nil
}

// parseIPRanges parses a comma separated list of IP ranges using parseIPRange.
func parseIPRanges(ipRangesList string, allowedNets ...*net.IPNet) ([]*iprange.Range, error) {
	ipRanges := strings.Split(ipRangesList, ",")
	netIPRanges := make([]*iprange.Range, 0, len(ipRanges))
	for _, ipRange := range ipRanges {
		netIPRange, err := parseIPRange(strings.TrimSpace(ipRange), allowedNets...)
		if err != nil {
			return nil, err
		}

		netIPRanges = append(netIPRanges, netIPRange)
	}

	return netIPRanges, nil
}

// VLANInterfaceCreate creates a VLAN interface on parent interface (if needed).
// Returns boolean indicating if VLAN interface was created.
func VLANInterfaceCreate(parent string, vlanDevice string, vlanID string, gvrp bool) (bool, error) {
	if vlanID == "" {
		return false, nil
	}

	if InterfaceExists(vlanDevice) {
		return false, nil
	}

	// Bring the parent interface up so we can add a vlan to it.
	link := &ip.Link{Name: parent}
	err := link.SetUp()
	if err != nil {
		return false, fmt.Errorf("Failed to bring up parent %q: %w", parent, err)
	}

	vlan := &ip.Vlan{
		Link: ip.Link{
			Name:   vlanDevice,
			Parent: parent,
		},
		VlanID: vlanID,
		Gvrp:   gvrp,
	}

	err = vlan.Add()
	if err != nil {
		return false, fmt.Errorf("Failed to create VLAN interface %q on %q: %w", vlanDevice, parent, err)
	}

	err = vlan.SetUp()
	if err != nil {
		return false, fmt.Errorf("Failed to bring up interface %q: %w", vlanDevice, err)
	}

	// Attempt to disable IPv6 router advertisement acceptance.
	_ = localUtil.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", vlanDevice), "0")

	// We created a new vlan interface, return true.
	return true, nil
}

// InterfaceRemove removes a network interface by name.
func InterfaceRemove(nic string) error {
	link := &ip.Link{Name: nic}
	err := link.Delete()
	return err
}

// InterfaceExists returns true if network interface exists.
func InterfaceExists(nic string) bool {
	if nic != "" && util.PathExists(fmt.Sprintf("/sys/class/net/%s", nic)) {
		return true
	}

	return false
}

// IPInSlice returns true if slice has IP element.
func IPInSlice(key net.IP, list []net.IP) bool {
	for _, entry := range list {
		if entry.Equal(key) {
			return true
		}
	}
	return false
}

// SubnetContains returns true if outerSubnet contains innerSubnet.
func SubnetContains(outerSubnet *net.IPNet, innerSubnet *net.IPNet) bool {
	if outerSubnet == nil || innerSubnet == nil {
		return false
	}

	if !outerSubnet.Contains(innerSubnet.IP) {
		return false
	}

	outerOnes, outerBits := outerSubnet.Mask.Size()
	innerOnes, innerBits := innerSubnet.Mask.Size()

	// Check number of bits in mask match.
	if innerBits != outerBits {
		return false
	}

	// Check that the inner subnet isn't outside of the outer subnet.
	if innerOnes < outerOnes {
		return false
	}

	return true
}

// SubnetContainsIP returns true if outsetSubnet contains IP address.
func SubnetContainsIP(outerSubnet *net.IPNet, ip net.IP) bool {
	// Convert ip to ipNet.
	ipIsIP4 := ip.To4() != nil

	prefix := 32
	if !ipIsIP4 {
		prefix = 128
	}

	_, ipSubnet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", ip.String(), prefix))
	if err != nil {
		return false
	}

	ipSubnet.IP = ip

	return SubnetContains(outerSubnet, ipSubnet)
}

// SubnetIterate iterates through each IP in a subnet calling a function for each IP.
// If the ipFunc returns a non-nil error then the iteration stops and the error is returned.
func SubnetIterate(subnet *net.IPNet, ipFunc func(ip net.IP) error) error {
	inc := big.NewInt(1)

	// Convert route start IP to native representations to allow incrementing.
	startIP := subnet.IP.To4()
	if startIP == nil {
		startIP = subnet.IP.To16()
	}

	startBig := big.NewInt(0)
	startBig.SetBytes(startIP)

	// Iterate through IPs in subnet, calling ipFunc for each one.
	for {
		ip := net.IP(startBig.Bytes())
		if !subnet.Contains(ip) {
			break
		}

		err := ipFunc(ip)
		if err != nil {
			return err
		}

		startBig.Add(startBig, inc)
	}

	return nil
}

// SubnetParseAppend parses one or more string CIDR subnets. Appends to the supplied slice. Returns subnets slice.
func SubnetParseAppend(subnets []*net.IPNet, parseSubnet ...string) ([]*net.IPNet, error) {
	for _, subnetStr := range parseSubnet {
		_, subnet, err := net.ParseCIDR(subnetStr)
		if err != nil {
			return nil, fmt.Errorf("Invalid subnet %q: %w", subnetStr, err)
		}

		subnets = append(subnets, subnet)
	}

	return subnets, nil
}

// IPRangesOverlap checks whether two ip ranges have ip addresses in common.
func IPRangesOverlap(r1, r2 *iprange.Range) bool {
	if r1.End == nil {
		return r2.ContainsIP(r1.Start)
	}

	if r2.End == nil {
		return r1.ContainsIP(r2.Start)
	}

	return r1.ContainsIP(r2.Start) || r1.ContainsIP(r2.End)
}

// InterfaceStatus returns the global unicast IP addresses configured on an interface and whether it is up or not.
func InterfaceStatus(nicName string) ([]net.IP, bool, error) {
	iface, err := net.InterfaceByName(nicName)
	if err != nil {
		return nil, false, fmt.Errorf("Failed loading interface %q: %w", nicName, err)
	}

	isUp := iface.Flags&net.FlagUp != 0

	addresses, err := iface.Addrs()
	if err != nil {
		return nil, isUp, fmt.Errorf("Failed getting interface addresses for %q: %w", nicName, err)
	}

	var globalUnicastIPs []net.IP
	for _, address := range addresses {
		ip, _, _ := net.ParseCIDR(address.String())
		if ip == nil {
			continue
		}

		if ip.IsGlobalUnicast() {
			globalUnicastIPs = append(globalUnicastIPs, ip)
		}
	}

	return globalUnicastIPs, isUp, nil
}

// ParsePortRange validates a port range in the form start-end.
func ParsePortRange(r string) (int64, int64, error) {
	entries := strings.Split(r, "-")
	if len(entries) > 2 {
		return -1, -1, fmt.Errorf("Invalid port range %q", r)
	}

	base, err := strconv.ParseInt(entries[0], 10, 64)
	if err != nil {
		return -1, -1, err
	}

	size := int64(1)
	if len(entries) > 1 {
		size, err = strconv.ParseInt(entries[1], 10, 64)
		if err != nil {
			return -1, -1, err
		}

		if size <= base {
			return -1, -1, fmt.Errorf("End port should be higher than start port")
		}

		size -= base
		size++
	}

	return base, size, nil
}

// ParseIPToNet parses a standalone IP address into a net.IPNet (with the IP field set to the IP supplied).
// The address family is detected and the subnet size set to /32 for IPv4 or /128 for IPv6.
func ParseIPToNet(ipAddress string) (*net.IPNet, error) {
	subnetSize := 32
	if strings.Contains(ipAddress, ":") {
		subnetSize = 128
	}

	listenAddress, listenAddressNet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", ipAddress, subnetSize))
	if err != nil {
		return nil, err
	}

	listenAddressNet.IP = listenAddress // Add IP back into parsed subnet.

	return listenAddressNet, err
}

// ParseIPCIDRToNet parses an IP in CIDR format into a net.IPNet (with the IP field set to the IP supplied).
func ParseIPCIDRToNet(ipAddressCIDR string) (*net.IPNet, error) {
	listenAddress, listenAddressNet, err := net.ParseCIDR(ipAddressCIDR)
	if err != nil {
		return nil, err
	}

	listenAddressNet.IP = listenAddress // Add IP back into parsed subnet.

	return listenAddressNet, err
}

// IPToNet converts an IP to a single host IPNet.
func IPToNet(ip net.IP) net.IPNet {
	len := 32
	if ip.To4() == nil {
		len = 128
	}

	return net.IPNet{
		IP:   ip,
		Mask: net.CIDRMask(len, len),
	}
}

// NICUsesNetwork returns true if the nicDev's "network" or "parent" property matches one of the networks names.
func NICUsesNetwork(nicDev map[string]string, networks ...*api.Network) bool {
	for _, network := range networks {
		if network.Name == nicDev["network"] || network.Name == nicDev["parent"] {
			return true
		}
	}

	return false
}

// BridgeNetfilterEnabled checks whether the bridge netfilter feature is loaded and enabled.
// If it is not an error is returned. This is needed in order for instances connected to a bridge to access DNAT
// listeners on the host, as otherwise the packets from the bridge do have the SNAT netfilter rules applied.
func BridgeNetfilterEnabled(ipVersion uint) error {
	sysctlName := "iptables"
	if ipVersion == 6 {
		sysctlName = "ip6tables"
	}

	sysctlPath := fmt.Sprintf("net/bridge/bridge-nf-call-%s", sysctlName)
	sysctlVal, err := localUtil.SysctlGet(sysctlPath)
	if err != nil {
		return fmt.Errorf("br_netfilter kernel module not loaded")
	}

	sysctlVal = strings.TrimSpace(sysctlVal)
	if sysctlVal != "1" {
		return fmt.Errorf("sysctl net.bridge.bridge-nf-call-%s not enabled", sysctlName)
	}

	return nil
}

// ProxyParseAddr validates a proxy address and parses it into its constituent parts.
func ProxyParseAddr(data string) (*deviceConfig.ProxyAddress, error) {
	// Split into <protocol> and <address>.
	fields := strings.SplitN(data, ":", 2)

	if !slices.Contains([]string{"tcp", "udp", "unix"}, fields[0]) {
		return nil, fmt.Errorf("Unknown protocol type %q", fields[0])
	}

	if len(fields) < 2 || fields[1] == "" {
		return nil, fmt.Errorf("Missing address")
	}

	newProxyAddr := &deviceConfig.ProxyAddress{
		ConnType: fields[0],
		Abstract: strings.HasPrefix(fields[1], "@"),
	}

	// unix addresses cannot have ports.
	if newProxyAddr.ConnType == "unix" {
		newProxyAddr.Address = fields[1]

		return newProxyAddr, nil
	}

	// Split <address> into <address> and <ports>.
	address, port, err := net.SplitHostPort(fields[1])
	if err != nil {
		return nil, err
	}

	// Validate that it's a valid address.
	if slices.Contains([]string{"udp", "tcp"}, newProxyAddr.ConnType) {
		err := validate.Optional(validate.IsNetworkAddress)(address)
		if err != nil {
			return nil, err
		}
	}

	newProxyAddr.Address = address

	// Split <ports> into individual ports and port ranges.
	ports := strings.SplitN(port, ",", -1)

	newProxyAddr.Ports = make([]uint64, 0, len(ports))

	for _, p := range ports {
		portFirst, portRange, err := ParsePortRange(p)
		if err != nil {
			return nil, err
		}

		for i := int64(0); i < portRange; i++ {
			newProxyAddr.Ports = append(newProxyAddr.Ports, uint64(portFirst+i))
		}
	}

	if len(newProxyAddr.Ports) <= 0 {
		return nil, fmt.Errorf("At least one port is required")
	}

	return newProxyAddr, nil
}
