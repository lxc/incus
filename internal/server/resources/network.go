package resources

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jaypipes/pcidb"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/shared/api"
)

var sysClassNet = "/sys/class/net"

var netProtocols = map[uint64]string{
	1:  "ethernet",
	19: "ATM",
	32: "infiniband",
}

func networkAddDeviceInfo(devicePath string, pciDB *pcidb.PCIDB, uname unix.Utsname, card *api.ResourcesNetworkCard) error {
	deviceDeviceDir, err := getDeviceDir(devicePath)
	if err != nil {
		return fmt.Errorf("Failed to read %q: %w", devicePath, err)
	}

	// VDPA
	vDPAMatches, err := filepath.Glob(filepath.Join(deviceDeviceDir, "vdpa*"))
	if err != nil {
		return fmt.Errorf("Malformed VDPA device name search pattern: %w", err)
	}

	if len(vDPAMatches) > 0 {
		vdpa := api.ResourcesNetworkCardVDPA{}

		splittedPath := strings.Split(vDPAMatches[0], "/")
		vdpa.Name = splittedPath[len(splittedPath)-1]
		vDPADevMatches, err := filepath.Glob(filepath.Join(vDPAMatches[0], "vhost-vdpa-*"))
		if err != nil {
			return fmt.Errorf("Malformed VDPA device name search pattern: %w", err)
		}

		if len(vDPADevMatches) > 0 {
			splittedPath = strings.Split(vDPADevMatches[0], "/")
			vdpa.Device = splittedPath[len(splittedPath)-1]
		} else {
			return fmt.Errorf("Failed to find VDPA device at device path %q", vDPAMatches[0])
		}

		// Add the VDPA data to the card
		card.VDPA = &vdpa
	}

	// SRIOV
	if sysfsExists(filepath.Join(deviceDeviceDir, "sriov_numvfs")) {
		sriov := api.ResourcesNetworkCardSRIOV{}

		// Get maximum and current VF count
		vfMaximum, err := readUint(filepath.Join(deviceDeviceDir, "sriov_totalvfs"))
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", filepath.Join(deviceDeviceDir, "sriov_totalvfs"), err)
		}

		vfCurrent, err := readUint(filepath.Join(deviceDeviceDir, "sriov_numvfs"))
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", filepath.Join(deviceDeviceDir, "sriov_numvfs"), err)
		}

		sriov.MaximumVFs = vfMaximum
		sriov.CurrentVFs = vfCurrent

		// Add the SRIOV data to the card
		card.SRIOV = &sriov
	}

	// NUMA node
	if sysfsExists(filepath.Join(deviceDeviceDir, "numa_node")) {
		numaNode, err := readInt(filepath.Join(deviceDeviceDir, "numa_node"))
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", filepath.Join(deviceDeviceDir, "numa_node"), err)
		}

		if numaNode > 0 {
			card.NUMANode = uint64(numaNode)
		}
	}

	// USB address
	usbAddr, err := usbAddress(deviceDeviceDir)
	if err != nil {
		return fmt.Errorf("Failed to find USB address for %q: %w", deviceDeviceDir, err)
	}

	if usbAddr != "" {
		card.USBAddress = usbAddr
	}

	// Vendor and product
	deviceVendorPath := filepath.Join(deviceDeviceDir, "vendor")
	if sysfsExists(deviceVendorPath) {
		id, err := os.ReadFile(deviceVendorPath)
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", deviceVendorPath, err)
		}

		card.VendorID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
	}

	deviceDevicePath := filepath.Join(deviceDeviceDir, "device")
	if sysfsExists(deviceDevicePath) {
		id, err := os.ReadFile(deviceDevicePath)
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", deviceDevicePath, err)
		}

		card.ProductID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
	}

	// Fill vendor and product names
	if pciDB != nil {
		vendor, ok := pciDB.Vendors[card.VendorID]
		if ok {
			card.Vendor = vendor.Name

			for _, product := range vendor.Products {
				if product.ID == card.ProductID {
					card.Product = product.Name
					break
				}
			}
		}
	}

	// Driver information
	driverPath := filepath.Join(deviceDeviceDir, "driver")
	if sysfsExists(driverPath) {
		linkTarget, err := filepath.EvalSymlinks(driverPath)
		if err != nil {
			return fmt.Errorf("Failed to find device directory %q: %w", driverPath, err)
		}

		// Set the driver name
		card.Driver = filepath.Base(linkTarget)

		// Try to get the version, fallback to kernel version
		out, err := os.ReadFile(filepath.Join(driverPath, "module", "version"))
		if err == nil {
			card.DriverVersion = strings.TrimSpace(string(out))
		} else {
			card.DriverVersion = strings.TrimRight(string(uname.Release[:]), "\x00")
		}
	}

	// Port information
	netPath := filepath.Join(devicePath, "net")
	if sysfsExists(netPath) {
		card.Ports = []api.ResourcesNetworkCardPort{}

		entries, err := os.ReadDir(netPath)
		if err != nil {
			return fmt.Errorf("Failed to list %q: %w", netPath, err)
		}

		// Iterate and record port data
		for _, entry := range entries {
			interfacePath := filepath.Join(netPath, entry.Name())
			info := &api.ResourcesNetworkCardPort{
				ID: entry.Name(),
			}

			// Add type
			if sysfsExists(filepath.Join(interfacePath, "type")) {
				devType, err := readUint(filepath.Join(interfacePath, "type"))
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", filepath.Join(interfacePath, "type"), err)
				}

				protocol, ok := netProtocols[devType]
				if !ok {
					info.Protocol = "unknown"
				} else {
					info.Protocol = protocol
				}
			}

			// Add MAC address
			if info.Address == "" && sysfsExists(filepath.Join(interfacePath, "address")) {
				address, err := os.ReadFile(filepath.Join(interfacePath, "address"))
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", filepath.Join(interfacePath, "address"), err)
				}

				info.Address = strings.TrimSpace(string(address))
			}

			// Add port number
			if sysfsExists(filepath.Join(interfacePath, "dev_port")) {
				port, err := readUint(filepath.Join(interfacePath, "dev_port"))
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", filepath.Join(interfacePath, "dev_port"), err)
				}

				info.Port = port
			}

			// Add infiniband specific information
			if info.Protocol == "infiniband" && sysfsExists(filepath.Join(devicePath, "infiniband")) {
				infiniband := &api.ResourcesNetworkCardPortInfiniband{}

				madPath := filepath.Join(devicePath, "infiniband_mad")
				if sysfsExists(madPath) {
					ibPort := info.Port + 1

					entries, err := os.ReadDir(madPath)
					if err != nil {
						return fmt.Errorf("Failed to list %q: %w", madPath, err)
					}

					for _, entry := range entries {
						entryName := entry.Name()
						currentPort, err := readUint(filepath.Join(madPath, entryName, "port"))
						if err != nil {
							return fmt.Errorf("Failed to read %q: %w", filepath.Join(madPath, entryName, "port"), err)
						}

						if currentPort != ibPort {
							continue
						}

						if !sysfsExists(filepath.Join(madPath, entryName, "dev")) {
							continue
						}

						dev, err := os.ReadFile(filepath.Join(madPath, entryName, "dev"))
						if err != nil {
							return fmt.Errorf("Failed to read %q: %w", filepath.Join(madPath, entryName, "dev"), err)
						}

						if strings.HasPrefix(entryName, "issm") {
							infiniband.IsSMName = entryName
							infiniband.IsSMDevice = strings.TrimSpace(string(dev))
						}

						if strings.HasPrefix(entryName, "umad") {
							infiniband.MADName = entryName
							infiniband.MADDevice = strings.TrimSpace(string(dev))
						}
					}
				}

				verbsPath := filepath.Join(devicePath, "infiniband_verbs")
				if sysfsExists(verbsPath) {
					entries, err := os.ReadDir(verbsPath)
					if err != nil {
						return fmt.Errorf("Failed to list %q: %w", verbsPath, err)
					}

					if len(entries) == 1 {
						verbName := entries[0].Name()
						infiniband.VerbName = verbName

						if !sysfsExists(filepath.Join(verbsPath, verbName, "dev")) {
							continue
						}

						dev, err := os.ReadFile(filepath.Join(verbsPath, verbName, "dev"))
						if err != nil {
							return fmt.Errorf("Failed to read %q: %w", filepath.Join(verbsPath, verbName, "dev"), err)
						}

						infiniband.VerbDevice = strings.TrimSpace(string(dev))
					}
				}

				info.Infiniband = infiniband
			}

			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Getting physical port info for VFs makes no sense
				card.Ports = append(card.Ports, *info)
				continue
			}

			// Attempt to add ethtool details (ignore failures)
			err = ethtoolAddPortInfo(info)
			if err != nil {
				continue
			}

			card.Ports = append(card.Ports, *info)
		}

		if len(card.Ports) > 0 {
			err = ethtoolAddCardInfo(card.Ports[0].ID, card)
			if err != nil {
				return fmt.Errorf("Failed to add card info: %w", err)
			}
		}
	}

	return nil
}

// GetNetwork returns a filled api.ResourcesNetwork struct ready for use by Incus.
func GetNetwork() (*api.ResourcesNetwork, error) {
	network := api.ResourcesNetwork{}
	network.Cards = []api.ResourcesNetworkCard{}

	// Get uname for driver version
	uname := unix.Utsname{}
	err := unix.Uname(&uname)
	if err != nil {
		return nil, fmt.Errorf("Failed to get uname: %w", err)
	}

	// Load PCI database
	pciDB, err := pcidb.New()
	if err != nil {
		pciDB = nil
	}

	// Temporary variables
	pciKnown := []string{}
	pciVFs := map[string][]api.ResourcesNetworkCard{}

	// Detect all Networks available through kernel network interface
	if sysfsExists(sysClassNet) {
		entries, err := os.ReadDir(sysClassNet)
		if err != nil {
			return nil, fmt.Errorf("Failed to list %q: %w", sysClassNet, err)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(sysClassNet, entryName)
			devicePath := filepath.Join(entryPath, "device")

			// Only keep physical network devices
			if !sysfsExists(filepath.Join(entryPath, "device")) {
				continue
			}

			// Setup the entry
			card := api.ResourcesNetworkCard{}

			// PCI address.
			pciAddr, err := pciAddress(devicePath)
			if err != nil {
				return nil, fmt.Errorf("Failed to find PCI address for %q: %w", devicePath, err)
			}

			if pciAddr != "" {
				card.PCIAddress = pciAddr

				// Skip devices we already know about
				if slices.Contains(pciKnown, card.PCIAddress) {
					continue
				}

				pciKnown = append(pciKnown, card.PCIAddress)
			}

			// Add device information for PFs
			err = networkAddDeviceInfo(devicePath, pciDB, uname, &card)
			if err != nil {
				return nil, fmt.Errorf("Failed to add device information for %q: %w", devicePath, err)
			}

			// Add to list
			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(filepath.Join(devicePath, "physfn"))
				if err != nil {
					return nil, fmt.Errorf("Failed to find %q: %w", filepath.Join(devicePath, "physfn"), err)
				}

				parentAddress := filepath.Base(linkTarget)

				_, ok := pciVFs[parentAddress]
				if !ok {
					pciVFs[parentAddress] = []api.ResourcesNetworkCard{}
				}

				pciVFs[parentAddress] = append(pciVFs[parentAddress], card)
			} else {
				network.Cards = append(network.Cards, card)
			}
		}
	}

	// Detect remaining Networks on PCI bus
	if sysfsExists(sysBusPci) {
		entries, err := os.ReadDir(sysBusPci)
		if err != nil {
			return nil, fmt.Errorf("Failed to list %q: %w", sysBusPci, err)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			devicePath := filepath.Join(sysBusPci, entryName)

			// Skip devices we already know about
			if slices.Contains(pciKnown, entryName) {
				continue
			}

			// Only care about identifiable devices
			if !sysfsExists(filepath.Join(devicePath, "class")) {
				continue
			}

			class, err := os.ReadFile(filepath.Join(devicePath, "class"))
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", filepath.Join(devicePath, "class"), err)
			}

			// Only care about VGA devices
			if !strings.HasPrefix(string(class), "0x02") {
				continue
			}

			// Start building up data
			card := api.ResourcesNetworkCard{}
			card.PCIAddress = entryName

			// Add device information
			err = networkAddDeviceInfo(devicePath, pciDB, uname, &card)
			if err != nil {
				return nil, fmt.Errorf("Failed to add device information for %q: %w", devicePath, err)
			}

			// Add to list
			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(filepath.Join(devicePath, "physfn"))
				if err != nil {
					return nil, fmt.Errorf("Failed to find %q: %w", filepath.Join(devicePath, "physfn"), err)
				}

				parentAddress := filepath.Base(linkTarget)

				_, ok := pciVFs[parentAddress]
				if !ok {
					pciVFs[parentAddress] = []api.ResourcesNetworkCard{}
				}

				pciVFs[parentAddress] = append(pciVFs[parentAddress], card)
			} else {
				network.Cards = append(network.Cards, card)
			}
		}
	}

	// Add SRIOV devices and count devices
	network.Total = 0
	for _, card := range network.Cards {
		if card.SRIOV != nil {
			card.SRIOV.VFs = pciVFs[card.PCIAddress]
			network.Total += uint64(len(card.SRIOV.VFs))
		}

		network.Total++
	}

	return &network, nil
}

// GetNetworkState returns the OS configuration for the network interface.
func GetNetworkState(name string) (*api.NetworkState, error) {
	// Get some information
	netIf, err := net.InterfaceByName(name)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusNotFound, "Network interface %q not found", name)
	}

	netState := "down"
	netType := "unknown"

	if netIf.Flags&net.FlagBroadcast > 0 {
		netType = "broadcast"
	}

	if netIf.Flags&net.FlagPointToPoint > 0 {
		netType = "point-to-point"
	}

	if netIf.Flags&net.FlagLoopback > 0 {
		netType = "loopback"
	}

	if netIf.Flags&net.FlagUp > 0 {
		netState = "up"
	}

	network := api.NetworkState{
		Addresses: []api.NetworkStateAddress{},
		Counters:  api.NetworkStateCounters{},
		Hwaddr:    netIf.HardwareAddr.String(),
		Mtu:       netIf.MTU,
		State:     netState,
		Type:      netType,
	}

	// Populate address information.
	addrs, err := netIf.Addrs()
	if err == nil {
		for _, addr := range addrs {
			fields := strings.SplitN(addr.String(), "/", 2)
			if len(fields) != 2 {
				continue
			}

			family := "inet"
			if strings.Contains(fields[0], ":") {
				family = "inet6"
			}

			scope := "global"
			if strings.HasPrefix(fields[0], "127") {
				scope = "local"
			}

			if fields[0] == "::1" {
				scope = "local"
			}

			if strings.HasPrefix(fields[0], "169.254") {
				scope = "link"
			}

			if strings.HasPrefix(fields[0], "fe80:") {
				scope = "link"
			}

			address := api.NetworkStateAddress{}
			address.Family = family
			address.Address = fields[0]
			address.Netmask = fields[1]
			address.Scope = scope

			network.Addresses = append(network.Addresses, address)
		}
	}

	// Populate bond details.
	bondPath := fmt.Sprintf("/sys/class/net/%s/bonding", name)
	if sysfsExists(bondPath) {
		bonding := api.NetworkStateBond{}

		// Bond mode.
		strValue, err := os.ReadFile(filepath.Join(bondPath, "mode"))
		if err == nil {
			bonding.Mode = strings.Split(strings.TrimSpace(string(strValue)), " ")[0]
		}

		// Bond transmit policy.
		strValue, err = os.ReadFile(filepath.Join(bondPath, "xmit_hash_policy"))
		if err == nil {
			bonding.TransmitPolicy = strings.Split(strings.TrimSpace(string(strValue)), " ")[0]
		}

		// Up delay.
		uintValue, err := readUint(filepath.Join(bondPath, "updelay"))
		if err == nil {
			bonding.UpDelay = uintValue
		}

		// Down delay.
		uintValue, err = readUint(filepath.Join(bondPath, "downdelay"))
		if err == nil {
			bonding.DownDelay = uintValue
		}

		// MII frequency.
		uintValue, err = readUint(filepath.Join(bondPath, "miimon"))
		if err == nil {
			bonding.MIIFrequency = uintValue
		}

		// MII state.
		strValue, err = os.ReadFile(filepath.Join(bondPath, "mii_status"))
		if err == nil {
			bonding.MIIState = strings.TrimSpace(string(strValue))
		}

		// Lower devices.
		strValue, err = os.ReadFile(filepath.Join(bondPath, "slaves"))
		if err == nil {
			bonding.LowerDevices = strings.Split(strings.TrimSpace(string(strValue)), " ")
		}

		network.Bond = &bonding
	}

	// Populate bridge details.
	bridgePath := fmt.Sprintf("/sys/class/net/%s/bridge", name)
	if sysfsExists(bridgePath) {
		bridge := api.NetworkStateBridge{}

		// Bridge ID.
		strValue, err := os.ReadFile(filepath.Join(bridgePath, "bridge_id"))
		if err == nil {
			bridge.ID = strings.TrimSpace(string(strValue))
		}

		// Bridge STP.
		uintValue, err := readUint(filepath.Join(bridgePath, "stp_state"))
		if err == nil {
			bridge.STP = uintValue == 1
		}

		// Bridge forward delay.
		uintValue, err = readUint(filepath.Join(bridgePath, "forward_delay"))
		if err == nil {
			bridge.ForwardDelay = uintValue
		}

		// Bridge default VLAN.
		uintValue, err = readUint(filepath.Join(bridgePath, "default_pvid"))
		if err == nil {
			bridge.VLANDefault = uintValue
		}

		// Bridge VLAN filtering.
		uintValue, err = readUint(filepath.Join(bridgePath, "vlan_filtering"))
		if err == nil {
			bridge.VLANFiltering = uintValue == 1
		}

		// Upper devices.
		bridgeIfPath := fmt.Sprintf("/sys/class/net/%s/brif", name)
		if sysfsExists(bridgeIfPath) {
			entries, err := os.ReadDir(bridgeIfPath)
			if err == nil {
				bridge.UpperDevices = []string{}
				for _, entry := range entries {
					bridge.UpperDevices = append(bridge.UpperDevices, entry.Name())
				}
			}
		}

		network.Bridge = &bridge
	}

	// Populate VLAN details.
	type vlan struct {
		lower string
		vid   uint64
	}

	vlans := map[string]vlan{}

	vlanPath := "/proc/net/vlan/config"
	if sysfsExists(vlanPath) {
		entries, err := os.ReadFile(vlanPath)
		if err != nil {
			return nil, err
		}

		for _, line := range strings.Split(string(entries), "\n") {
			fields := strings.Split(line, "|")
			if len(fields) != 3 {
				continue
			}

			vName := strings.TrimSpace(fields[0])
			vVID, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
			if err != nil {
				continue
			}

			vLower := strings.TrimSpace(fields[2])

			vlans[vName] = vlan{
				lower: vLower,
				vid:   vVID,
			}
		}
	}

	// Check if the interface is a VLAN.
	entry, ok := vlans[name]
	if ok {
		network.VLAN = &api.NetworkStateVLAN{
			LowerDevice: entry.lower,
			VID:         entry.vid,
		}
	}

	// Get counters.
	counters, err := GetNetworkCounters(name)
	if err != nil {
		return nil, err
	}

	network.Counters = *counters

	return &network, nil
}

// GetNetworkCounters returns the current packet counters for the network interface.
func GetNetworkCounters(name string) (*api.NetworkStateCounters, error) {
	counters := api.NetworkStateCounters{}

	// Get counters
	content, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		if os.IsNotExist(err) {
			return &counters, nil
		}

		return nil, err
	}

	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)

		if len(fields) != 17 {
			continue
		}

		intName := strings.TrimSuffix(fields[0], ":")
		if intName != name {
			continue
		}

		rxBytes, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, err
		}

		rxPackets, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, err
		}

		txBytes, err := strconv.ParseInt(fields[9], 10, 64)
		if err != nil {
			return nil, err
		}

		txPackets, err := strconv.ParseInt(fields[10], 10, 64)
		if err != nil {
			return nil, err
		}

		counters.BytesSent = txBytes
		counters.BytesReceived = rxBytes
		counters.PacketsSent = txPackets
		counters.PacketsReceived = rxPackets
		break
	}

	return &counters, nil
}
