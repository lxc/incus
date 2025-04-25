package ip

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vishvananda/netlink"
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

// VDPADev represents the vDPA device information.
type VDPADev struct {
	// Name of the vDPA created device. e.g. "vdpa0" (note: the iproute2 associated command would look like `vdpa dev add mgmtdev pci/<PCI_SLOT_NAME> name vdpa0 max_vqp <MAX_VQP>`).
	Name string
	// Max VQs supported by the vDPA device.
	MaxVQs uint32
	// Associated vhost-vdpa device.
	VhostVDPA *VhostVDPA
}

// getVhostVDPADevInPath returns the VhostVDPA found in the provided parent device's path.
func getVhostVDPADevInPath(parentPath string) (*VhostVDPA, error) {
	fd, err := os.Open(parentPath)
	if err != nil {
		return nil, fmt.Errorf("Can not open %s: %v", parentPath, err)
	}

	defer func() {
		_ = fd.Close()
	}()

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

// AddVDPADevice adds a new vDPA device.
func AddVDPADevice(pciDevSlotName string, volatile map[string]string) (*VDPADev, error) {
	existingDevices, err := netlink.VDPAGetDevList()
	if err != nil {
		return nil, fmt.Errorf("Failed to get vdpa dev list: %w", err)
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
		return nil, fmt.Errorf("Failed to get vdpa device %q: %w", generatedVDPADevName, err)
	}

	vhostVDPA, err := getVhostVDPADevInPath(filepath.Join(vdpaBusDevDir, dev.Name))
	if err != nil {
		return nil, fmt.Errorf("Failed to get vdpa vhost %q: %w", dev.Name, err)
	}

	// Update the volatile map
	volatile["last_state.vdpa.name"] = generatedVDPADevName

	return &VDPADev{
		Name:      dev.Name,
		MaxVQs:    dev.MaxVQS,
		VhostVDPA: vhostVDPA,
	}, nil
}

// DeleteVDPADevice deletes a vDPA management device.
func DeleteVDPADevice(vDPADevName string) error {
	return netlink.VDPADelDev(vDPADevName)
}
