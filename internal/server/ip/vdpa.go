package ip

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/vishvananda/netlink"
)

// Base flags passed to all Netlink requests.
const (
	commonNLFlags = syscall.NLM_F_REQUEST | syscall.NLM_F_ACK
)

// MAX_VQP is the maximum number of VQPs supported by the vDPA device and is always the same as of now.
const (
	vDPAMaxVQP = uint16(16)
)

// vDPA device classes.
const (
	vdpaBusDevDir   = "/sys/bus/vdpa/devices"
	vdpaVhostDevDir = "/dev"
)

// VhostVDPA is the vhost-vdpa device information.
type VhostVDPA struct {
	Name string
	Path string
}

// MgmtVDPADev represents the vDPA management device information.
type MgmtVDPADev struct {
	BusName string // e.g. "pci"
	DevName string // e.g. "0000:00:08.2"
}

// VDPADev represents the vDPA device information.
type VDPADev struct {
	// Name of the vDPA created device. e.g. "vdpa0" (note: the iproute2 associated command would look like `vdpa dev add mgmtdev pci/<PCI_SLOT_NAME> name vdpa0 max_vqp <MAX_VQP>`).
	Name string
	// Max VQs supported by the vDPA device.
	MaxVQs uint32
	// Associated vDPA management device.
	MgmtDev *MgmtVDPADev
	// Associated vhost-vdpa device.
	VhostVDPA *VhostVDPA
}

// getVhostVDPADevInPath returns the VhostVDPA found in the provided parent device's path.
func getVhostVDPADevInPath(parentPath string) (*VhostVDPA, error) {
	fd, err := os.Open(parentPath)
	if err != nil {
		return nil, fmt.Errorf("Can not open %s: %v", parentPath, err)
	}

	defer fd.Close()

	entries, err := fd.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("Can not get DirEntries: %v", err)
	}

	for _, file := range entries {
		if strings.Contains(file.Name(), "vhost-vdpa") && file.IsDir() {
			devicePath := filepath.Join(vdpaVhostDevDir, file.Name())
			info, err := os.Stat(devicePath)
			if err != nil {
				return nil, fmt.Errorf("Vhost device %s is not a valid device", devicePath)
			}

			if info.Mode()&os.ModeDevice == 0 {
				return nil, fmt.Errorf("Vhost device %s is not a valid device", devicePath)
			}

			return &VhostVDPA{
				Name: file.Name(),
				Path: devicePath,
			}, nil
		}
	}

	return nil, fmt.Errorf("No vhost-vdpa device found in %s", parentPath)
}

// ListVDPAMgmtDevices returns the list of all vDPA management devices.
func ListVDPAMgmtDevices() ([]*MgmtVDPADev, error) {
	netlinkDevices, err := netlink.VDPAGetMGMTDevList()
	if err != nil {
		return nil, fmt.Errorf("failed to get vdpa mgmt dev list: %w", err)
	}

	devices := make([]*MgmtVDPADev, 0, len(netlinkDevices))
	for _, dev := range netlinkDevices {
		devices = append(devices, &MgmtVDPADev{
			BusName: dev.BusName,
			DevName: dev.DevName,
		})
	}

	return devices, nil
}

// ListVDPADevices returns the list of all vDPA devices.
func ListVDPADevices() ([]*VDPADev, error) {
	netlinkDevices, err := netlink.VDPAGetDevList()
	if err != nil {
		return nil, fmt.Errorf("failed to get vdpa dev list: %w", err)
	}

	devices := make([]*VDPADev, 0, len(netlinkDevices))
	for _, dev := range netlinkDevices {
		vhostVDPA, err := getVhostVDPADevInPath(filepath.Join(vdpaBusDevDir, dev.Name))
		if err != nil {
			return nil, err
		}

		devices = append(devices, &VDPADev{
			Name:      dev.Name,
			MaxVQs:    dev.MaxVQS,
			MgmtDev:   nil, // TODO: the netlink library does not expose the associated management device information
			VhostVDPA: vhostVDPA,
		})
	}

	return devices, nil
}

// AddVDPADevice adds a new vDPA device.
func AddVDPADevice(pciDevSlotName string, volatile map[string]string) (*VDPADev, error) {
	existingDevices, err := netlink.VDPAGetDevList()
	if err != nil {
		return nil, fmt.Errorf("failed to get vdpa dev list: %w", err)
	}

	existingVDPADevNames := make(map[string]struct{})
	for _, device := range existingDevices {
		existingVDPADevNames[device.Name] = struct{}{}
	}

	// Generate a unique attribute name for the vDPA device (i.e, vdpa0, vdpa1, etc.)
	baseVDPAName, idx, generatedVDPADevName := "vdpa", 0, ""
	for {
		generatedVDPADevName = fmt.Sprintf("%s%d", baseVDPAName, idx)
		_, ok := existingVDPADevNames[generatedVDPADevName]
		if !ok {
			break
		}

		idx++
	}

	err = netlink.VDPANewDev(generatedVDPADevName, "pci", pciDevSlotName, netlink.VDPANewDevParams{
		MaxVQP: vDPAMaxVQP,
	})
	if err != nil {
		return nil, err
	}

	dev, err := netlink.VDPAGetDevByName(generatedVDPADevName)
	if err != nil {
		return nil, fmt.Errorf("failed to get vdpa device %q: %w", generatedVDPADevName, err)
	}

	vhostVDPA, err := getVhostVDPADevInPath(filepath.Join(vdpaBusDevDir, dev.Name))
	if err != nil {
		return nil, fmt.Errorf("failed to get vdpa vhost %q: %w", dev.Name, err)
	}

	// Update the volatile map
	volatile["last_state.vdpa.name"] = generatedVDPADevName

	return &VDPADev{
		Name:      dev.Name,
		MaxVQs:    dev.MaxVQS,
		MgmtDev:   nil, // TODO: the netlink library does not expose the associated management device information
		VhostVDPA: vhostVDPA,
	}, nil
}

// DeleteVDPADevice deletes a vDPA management device.
func DeleteVDPADevice(vDPADevName string) error {
	return netlink.VDPADelDev(vDPADevName)
}
