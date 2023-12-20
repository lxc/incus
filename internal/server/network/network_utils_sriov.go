package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/internal/server/db"
	dbCluster "github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/device/pci"
	"github.com/lxc/incus/internal/server/ip"
	"github.com/lxc/incus/internal/server/network/ovs"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

// sriovReservedDevicesMutex used to coordinate access for checking reserved devices.
var sriovReservedDevicesMutex sync.Mutex

// SRIOVVirtualFunctionMutex used to coordinate access for finding and claiming free virtual functions.
var SRIOVVirtualFunctionMutex sync.Mutex

var sysClassNet = "/sys/class/net"

// SRIOVGetHostDevicesInUse returns a map of host device names that have been used by devices in other instances
// and networks on the local member. Used when selecting physical and SR-IOV VF devices to avoid conflicts.
func SRIOVGetHostDevicesInUse(s *state.State) (map[string]struct{}, error) {
	sriovReservedDevicesMutex.Lock()
	defer sriovReservedDevicesMutex.Unlock()

	var err error
	var projectNetworks map[string]map[int64]api.Network

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get all managed networks across all projects.
		projectNetworks, err = tx.GetCreatedNetworks(ctx)
		if err != nil {
			return fmt.Errorf("Failed to load all networks: %w", err)
		}

		return err
	})
	if err != nil {
		return nil, err
	}

	filter := dbCluster.InstanceFilter{Node: &s.ServerName}
	reservedDevices := map[string]struct{}{}

	// Check if any instances are using the VF device.
	err = s.DB.Cluster.InstanceList(context.TODO(), func(dbInst db.InstanceArgs, p api.Project) error {
		// Expand configs so we take into account profile devices.
		dbInst.Config = db.ExpandInstanceConfig(dbInst.Config, dbInst.Profiles)
		dbInst.Devices = db.ExpandInstanceDevices(dbInst.Devices, dbInst.Profiles)

		for name, dev := range dbInst.Devices {
			// If device references a parent host interface name, mark that as reserved.
			parent := dev["parent"]
			if parent != "" {
				reservedDevices[parent] = struct{}{}
			}

			// If device references a volatile host interface name, mark that as reserved.
			hostName := dbInst.Config[fmt.Sprintf("volatile.%s.host_name", name)]
			if hostName != "" {
				reservedDevices[hostName] = struct{}{}
			}
		}

		return nil
	}, filter)
	if err != nil {
		return nil, err
	}

	// Check if any networks are using the VF device.
	for _, networks := range projectNetworks {
		for _, ni := range networks {
			// If network references a parent host interface name, mark that as reserved.
			parent := ni.Config["parent"]
			if parent != "" {
				reservedDevices[parent] = struct{}{}
			}
		}
	}

	return reservedDevices, nil
}

// SRIOVFindFreeVirtualFunction looks on the specified parent device for an unused virtual function.
// Returns the name of the interface and virtual function index ID if found, error if not.
func SRIOVFindFreeVirtualFunction(s *state.State, parentDev string) (string, int, error) {
	reservedDevices, err := SRIOVGetHostDevicesInUse(s)
	if err != nil {
		return "", -1, fmt.Errorf("Failed getting in use device list: %w", err)
	}

	sriovNumVFsFile := fmt.Sprintf("/sys/class/net/%s/device/sriov_numvfs", parentDev)
	sriovTotalVFsFile := fmt.Sprintf("/sys/class/net/%s/device/sriov_totalvfs", parentDev)

	// Verify that this is indeed a SR-IOV enabled device.
	if !util.PathExists(sriovNumVFsFile) {
		return "", -1, fmt.Errorf("Parent device %q doesn't support SR-IOV", parentDev)
	}

	// Get parent dev_port and dev_id values.
	pfDevPort, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/dev_port", parentDev))
	if err != nil {
		return "", -1, err
	}

	pfDevID, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/dev_id", parentDev))
	if err != nil {
		return "", -1, err
	}

	// Get number of currently enabled VFs.
	sriovNumVFsBuf, err := os.ReadFile(sriovNumVFsFile)
	if err != nil {
		return "", -1, err
	}

	sriovNumVFs, err := strconv.Atoi(strings.TrimSpace(string(sriovNumVFsBuf)))
	if err != nil {
		return "", -1, err
	}

	// Get number of possible VFs.
	sriovTotalVFsBuf, err := os.ReadFile(sriovTotalVFsFile)
	if err != nil {
		return "", -1, err
	}

	sriovTotalVFs, err := strconv.Atoi(strings.TrimSpace(string(sriovTotalVFsBuf)))
	if err != nil {
		return "", -1, err
	}

	// Ensure parent is up (needed for Intel at least).
	link := &ip.Link{Name: parentDev}
	err = link.SetUp()
	if err != nil {
		return "", -1, err
	}

	// Check if any free VFs are already enabled.
	vfID, nicName, err := sriovGetFreeVFInterface(reservedDevices, parentDev, sriovNumVFs, 0, pfDevID, pfDevPort)
	if err != nil {
		return "", -1, err
	}

	// Found a free VF.
	if nicName != "" {
		return nicName, vfID, nil
	} else if sriovNumVFs < sriovTotalVFs {
		logger.Debugf("Attempting to grow available VFs from %d to %d on device %q", sriovNumVFs, sriovTotalVFs, parentDev)

		// Bump the number of VFs to the maximum if not there yet.
		err = os.WriteFile(sriovNumVFsFile, []byte(fmt.Sprintf("%d", sriovTotalVFs)), 0644)
		if err != nil {
			return "", -1, fmt.Errorf("Failed growing available VFs from %d to %d on device %q: %w", sriovNumVFs, sriovTotalVFs, parentDev, err)
		}

		time.Sleep(time.Second) // Allow time for new VFs to appear.

		// Use next free VF index starting from the first newly created VF.
		vfID, nicName, err = sriovGetFreeVFInterface(reservedDevices, parentDev, sriovTotalVFs, sriovNumVFs, pfDevID, pfDevPort)
		if err != nil {
			return "", -1, err
		}

		// Found a free VF.
		if nicName != "" {
			return nicName, vfID, nil
		}
	}

	return "", -1, fmt.Errorf("All virtual functions on parent device %q are already in use", parentDev)
}

// sriovGetFreeVFInterface checks the system for a free VF interface that belongs to the same device and port as
// the parent device starting from the startVFID to the vfCount-1. Returns VF ID and VF interface name if found or
// -1 and empty string if no free interface found. A free interface is one that is bound on the host, not in the
// reservedDevices map, is down and has no global IPs defined on it.
func sriovGetFreeVFInterface(reservedDevices map[string]struct{}, parentDev string, vfCount int, startVFID int, pfDevID []byte, pfDevPort []byte) (int, string, error) {
	for vfID := startVFID; vfID < vfCount; vfID++ {
		vfListPath := fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", parentDev, vfID)

		if !util.PathExists(vfListPath) {
			continue // The vfListPath won't exist if the VF has been unbound and used with a VM.
		}

		ents, err := os.ReadDir(vfListPath)
		if err != nil {
			return -1, "", fmt.Errorf("Failed reading VF interface directory %q: %w", vfListPath, err)
		}

		for _, ent := range ents {
			// We expect the entry to be a directory for the VF's interface name.
			if !ent.IsDir() {
				continue
			}

			nicName := ent.Name()

			// We can't use this VF interface as it is reserved by another device.
			_, exists := reservedDevices[nicName]
			if exists {
				continue
			}

			// Get VF dev_port and dev_id values.
			vfDevPort, err := os.ReadFile(fmt.Sprintf("%s/%s/dev_port", vfListPath, nicName))
			if err != nil {
				return -1, "", err
			}

			vfDevID, err := os.ReadFile(fmt.Sprintf("%s/%s/dev_id", vfListPath, nicName))
			if err != nil {
				return -1, "", err
			}

			// Skip VFs if they do not relate to the same device and port as the parent PF.
			// Some card vendors change the device ID for each port.
			if !bytes.Equal(pfDevPort, vfDevPort) || !bytes.Equal(pfDevID, vfDevID) {
				continue
			}

			addresses, isUp, err := InterfaceStatus(nicName)
			if err != nil {
				return -1, "", err
			}

			// Ignore if interface is up or if interface has unicast IP addresses (may be in use by
			// another application already).
			if isUp || len(addresses) > 0 {
				continue
			}

			// Found a free VF.
			return vfID, nicName, err
		}
	}

	return -1, "", nil
}

// SRIOVGetVFDevicePCISlot returns the PCI slot name for a network virtual function device.
func SRIOVGetVFDevicePCISlot(parentDev string, vfID string) (pci.Device, error) {
	ueventFile := fmt.Sprintf("/sys/class/net/%s/device/virtfn%s/uevent", parentDev, vfID)
	pciDev, err := pci.ParseUeventFile(ueventFile)
	if err != nil {
		return pciDev, err
	}

	return pciDev, nil
}

// SRIOVSwitchdevEnabled returns true if switchdev mode is enabled on the given device.
func SRIOVSwitchdevEnabled(deviceName string) bool {
	var buf bytes.Buffer

	ueventFile := fmt.Sprintf("%s/%s/device/uevent", sysClassNet, deviceName)

	pciDev, err := pci.ParseUeventFile(ueventFile)
	if err != nil {
		return false
	}

	slotName := fmt.Sprintf("pci/%s", pciDev.SlotName)

	err = subprocess.RunCommandWithFds(context.TODO(), nil, &buf, "devlink", "-j", "dev", "eswitch", "show", slotName)
	if err != nil {
		return false
	}

	dev := map[string]map[string]struct {
		Mode string
	}{}

	err = json.NewDecoder(&buf).Decode(&dev)
	if err != nil {
		return false
	}

	if dev["dev"][slotName].Mode == "switchdev" {
		return true
	}

	return false
}

// SRIOVFindRepresentorPort finds the associated representor port name for a switchdev VF ID.
func SRIOVFindRepresentorPort(nicEntries []fs.DirEntry, pfSwitchID string, pfID int, vfID int) string {
	for _, nic := range nicEntries {
		nicSwitchID, err := os.ReadFile(filepath.Join(sysClassNet, nic.Name(), "phys_switch_id"))
		if err != nil {
			continue // Skip non-physical interfaces.
		}

		if string(nicSwitchID) != pfSwitchID {
			continue // Skip interfaces not connected to PF's switchdev.
		}

		// Check if this representor port matches the PF and VF by parsing phys_port_name.
		physPortName, err := os.ReadFile(filepath.Join(sysClassNet, nic.Name(), "phys_port_name"))
		if err != nil {
			continue // Skip interfaces with no physical port name.
		}

		var nicPFID, nicVFID int
		_, err = fmt.Sscanf(string(physPortName), "pf%dvf%d", &nicPFID, &nicVFID)
		if err != nil {
			continue // Skip non-VF interfaces.
		}

		if nicPFID == pfID && nicVFID == vfID {
			return nic.Name() // We have a match.
		}
	}

	return ""
}

func SRIOVGetSwitchAndPFID(parentDev string) (string, int, error) {
	physPortName, err := os.ReadFile(filepath.Join(sysClassNet, parentDev, "phys_port_name"))
	if err != nil {
		return "", -1, err // Skip non-physical ports.
	}

	// Check the port is a physical port and not an existing representor port connected to the bridge
	// but belonging to a physical device. This avoids trying to find a free VF repeatedly for the same
	// PF by mistakenly considering an existing representor ported connected to the bridge as a PF.
	if strings.HasPrefix(string(physPortName), "pf") || !strings.HasPrefix(string(physPortName), "p") {
		return "", -1, fmt.Errorf("Not a physical port: %s", string(physPortName))
	}

	var pfID int
	_, err = fmt.Sscanf(string(physPortName), "p%d", &pfID)
	if err != nil {
		return "", -1, fmt.Errorf("Not a PF: %s.", string(physPortName)) // Skip non-PF interfaces.
	}

	// Check if switchdev is enabled on physical port.
	if !SRIOVSwitchdevEnabled(parentDev) {
		return "", -1, fmt.Errorf("Not a switchdev capable device: %s", parentDev)
	}

	physSwitchID, err := os.ReadFile(filepath.Join(sysClassNet, parentDev, "phys_switch_id"))
	if err != nil {
		return "", -1, fmt.Errorf("Unable to get phys_switch_id: %w", err)
	}

	return string(physSwitchID), pfID, nil
}

// SRIOVFindFreeVFAndRepresentor tries to find a free SR-IOV virtual function of a PF connected to an OVS bridge.
// To do this it first looks at the ports on the OVS bridge specified and identifies which ones are PF ports in
// switchdev mode. It then tries to find a free VF on that PF and the representor port associated to the VF ID.
// It returns the PF name, representor port name, VF name, and VF ID.
func SRIOVFindFreeVFAndRepresentor(state *state.State, ovsBridgeName string) (string, string, string, int, error) {
	nics, err := os.ReadDir(sysClassNet)
	if err != nil {
		return "", "", "", -1, fmt.Errorf("Failed to read directory %q: %w", sysClassNet, err)
	}

	vswitch, err := ovs.NewVSwitch()
	if err != nil {
		return "", "", "", -1, fmt.Errorf("Failed to connect to OVS: %w", err)
	}

	// Get all ports on the integration bridge.
	ports, err := vswitch.BridgePortList(ovsBridgeName)
	if err != nil {
		return "", "", "", -1, fmt.Errorf("Failed to get port list: %w", err)
	}

	// Iterate through the list of ports and identify the PFs by trying to locate a VF (virtual function).
	for _, port := range ports {
		physSwitchID, pfID, err := SRIOVGetSwitchAndPFID(port)
		if err != nil {
			continue
		}

		vfName, vfID, err := SRIOVFindFreeVirtualFunction(state, port)
		if err != nil {
			continue
		}

		// Track down the representor port. The number of representor ports depends on the number of enabled VFs.
		// All representor ports have the same phys_switch_id as a PF connected to the same switch, and there may be
		// multiple PFs on one switch.
		representorPort := SRIOVFindRepresentorPort(nics, string(physSwitchID), pfID, vfID)
		if representorPort != "" {
			return port, representorPort, vfName, vfID, nil
		}
	}

	return "", "", "", -1, fmt.Errorf("No free virtual function and representor port found")
}
