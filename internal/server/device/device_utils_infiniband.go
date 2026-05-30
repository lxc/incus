package device

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/ip"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

// IBDevPrefix Infiniband devices prefix.
const IBDevPrefix = "infiniband.unix"

// infinibandDevices extracts the infiniband parent device from the supplied nic list and any free
// associated virtual functions (VFs) that are on the same card and port as the specified parent.
// This function expects that the supplied nic list does not include VFs that are already attached
// to running instances.
func infinibandDevices(nics *api.ResourcesNetwork, parent string) map[string]*api.ResourcesNetworkCardPort {
	ibDevs := make(map[string]*api.ResourcesNetworkCardPort)
	for _, card := range nics.Cards {
		for _, port := range card.Ports {
			// Skip non-infiniband ports.
			if port.Protocol != "infiniband" {
				continue
			}

			// Skip port if not parent.
			if port.ID != parent {
				continue
			}

			// Store infiniband port info.
			ibDevs[port.ID] = &port
		}

		// Skip virtual function (VF) extraction if SRIOV isn't supported on port.
		if card.SRIOV == nil {
			continue
		}

		// Record if parent has been found as a physical function (PF).
		parentDev, parentIsPF := ibDevs[parent]

		for _, VF := range card.SRIOV.VFs {
			for _, port := range VF.Ports {
				// Skip non-infiniband VFs.
				if port.Protocol != "infiniband" {
					continue
				}

				// Skip VF if parent is a PF and VF is not on same port as parent.
				if parentIsPF && parentDev.Port != port.Port {
					continue
				}

				// Skip VF if parent isn't a PF and VF doesn't match parent name.
				if !parentIsPF && port.ID != parent {
					continue
				}

				// Store infiniband VF port info.
				ibDevs[port.ID] = &port
			}
		}
	}

	return ibDevs
}

// infinibandAddDevices creates the UNIX devices for the provided IBF device and then configures the
// supplied runConfig with the Cgroup rules and mount instructions to pass the device into instance.
func infinibandAddDevices(s *state.State, devicesPath string, deviceName string, ibDev *api.ResourcesNetworkCardPort, runConf *deviceConfig.RunConfig) error {
	if ibDev.Infiniband == nil {
		return errors.New("No infiniband devices supplied")
	}

	// Add IsSM device if defined.
	if ibDev.Infiniband.IsSMName != "" {
		device := deviceConfig.Device{
			"source": fmt.Sprintf("/dev/infiniband/%s", ibDev.Infiniband.IsSMName),
		}

		err := unixDeviceSetup(s, devicesPath, IBDevPrefix, deviceName, device, false, runConf)
		if err != nil {
			return err
		}
	}

	// Add MAD device if defined.
	if ibDev.Infiniband.MADName != "" {
		device := deviceConfig.Device{
			"source": fmt.Sprintf("/dev/infiniband/%s", ibDev.Infiniband.MADName),
		}

		err := unixDeviceSetup(s, devicesPath, IBDevPrefix, deviceName, device, false, runConf)
		if err != nil {
			return err
		}
	}

	// Add Verb device if defined.
	if ibDev.Infiniband.VerbName != "" {
		device := deviceConfig.Device{
			"source": fmt.Sprintf("/dev/infiniband/%s", ibDev.Infiniband.VerbName),
		}

		err := unixDeviceSetup(s, devicesPath, IBDevPrefix, deviceName, device, false, runConf)
		if err != nil {
			return err
		}
	}

	return nil
}

// infinibandValidMAC validates an infiniband MAC address. Supports both short and long variants,
// e.g. "4a:c8:f9:1b:aa:57:ef:19" and "a0:00:0f:c0:fe:80:00:00:00:00:00:00:4a:c8:f9:1b:aa:57:ef:19".
func infinibandValidMAC(value string) error {
	_, err := net.ParseMAC(value)

	// Check valid lengths and delimiter.
	if err != nil || (len(value) != 23 && len(value) != 59) || strings.ContainsAny(value, "-.") {
		return errors.New("Invalid value, must be either 8 or 20 bytes of hex separated by colons")
	}

	return nil
}

// infinibandSetDevMAC detects whether the supplied MAC is a short or long form variant.
// If the short form variant is supplied then only the last 8 bytes of the ibDev device's hwaddr
// are changed. If the long form variant is supplied then the full 20 bytes of the ibDev device's
// hwaddr are changed.
func infinibandSetDevMAC(ibDev string, hwaddr string) error {
	// Handle 20 byte variant, e.g. a0:00:14:c0:fe:80:00:00:00:00:00:00:4a:c8:f9:1b:aa:57:ef:19.
	if len(hwaddr) == 59 {
		return NetworkSetDevMAC(ibDev, hwaddr)
	}

	// Handle 8 byte variant, e.g. 4a:c8:f9:1b:aa:57:ef:19.
	if len(hwaddr) == 23 {
		curHwaddr, err := NetworkGetDevMAC(ibDev)
		if err != nil {
			return err
		}

		return NetworkSetDevMAC(ibDev, fmt.Sprintf("%s%s", curHwaddr[:36], hwaddr))
	}

	return errors.New("Invalid length")
}

// infinibandValidGUID validates an Infiniband node or port GUID,
// e.g. "4a:c8:f9:1b:aa:57:ef:19".
func infinibandValidGUID(value string) error {
	_, err := net.ParseMAC(value)

	// A GUID is 8 bytes of hex separated by colons (23 characters).
	if err != nil || len(value) != 23 || strings.ContainsAny(value, "-.") {
		return errors.New("Invalid value, must be 8 bytes of hex separated by colons")
	}

	return nil
}

// infinibandVFID returns the SR-IOV virtual function index of vfName relative to its parent
// physical function. It returns -1 if vfName isn't a virtual function of parent.
func infinibandVFID(parent string, vfName string) (int, error) {
	sriovNumVFsBuf, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/device/sriov_numvfs", parent))
	if err != nil {
		return -1, err
	}

	sriovNumVFs, err := strconv.Atoi(strings.TrimSpace(string(sriovNumVFsBuf)))
	if err != nil {
		return -1, err
	}

	for vfID := range sriovNumVFs {
		ents, err := os.ReadDir(fmt.Sprintf("/sys/class/net/%s/device/virtfn%d/net", parent, vfID))
		if err != nil {
			continue // The directory won't exist if the VF has been unbound and used with a VM.
		}

		for _, ent := range ents {
			if ent.Name() == vfName {
				return vfID, nil
			}
		}
	}

	return -1, nil
}

// infinibandZeroGUID clears an administratively assigned GUID, leaving the kernel to assign one.
const infinibandZeroGUID = "00:00:00:00:00:00:00:00"

// infinibandSetVFGUIDs sets the node and/or port GUID of a virtual function through netlink.
// Empty values are left unchanged. Mainline only exposes the administratively assigned GUIDs
// through netlink, so the previous values can't be read back and aren't snapshotted.
func infinibandSetVFGUIDs(parent string, vfID int, nodeGUID string, portGUID string) error {
	link := &ip.Link{Name: parent}

	if nodeGUID != "" {
		err := link.SetVfNodeGUID(strconv.Itoa(vfID), nodeGUID)
		if err != nil {
			return fmt.Errorf("Failed to set node_guid %q on %q (VF %d): %w", nodeGUID, parent, vfID, err)
		}
	}

	if portGUID != "" {
		err := link.SetVfPortGUID(strconv.Itoa(vfID), portGUID)
		if err != nil {
			return fmt.Errorf("Failed to set port_guid %q on %q (VF %d): %w", portGUID, parent, vfID, err)
		}
	}

	return nil
}

// infinibandRestoreVFGUIDs clears any node or port GUID previously set on the virtual function
// recorded in volatile. Mainline can't read the original GUID, so the administrative override is
// reset to all-zeros (kernel assigned) rather than restored to a prior value.
func infinibandRestoreVFGUIDs(parent string, nodeGUID string, portGUID string, volatile map[string]string) error {
	if volatile["last_state.vf.id"] == "" {
		return nil
	}

	vfID, err := strconv.Atoi(volatile["last_state.vf.id"])
	if err != nil {
		return fmt.Errorf("Failed parsing VF ID %q for %q: %w", volatile["last_state.vf.id"], parent, err)
	}

	clearNode := ""
	if nodeGUID != "" {
		clearNode = infinibandZeroGUID
	}

	clearPort := ""
	if portGUID != "" {
		clearPort = infinibandZeroGUID
	}

	return infinibandSetVFGUIDs(parent, vfID, clearNode, clearPort)
}
