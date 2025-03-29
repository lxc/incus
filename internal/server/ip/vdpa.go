package ip

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

// vDPA Netlink name.
const (
	vDPAGenlName = "vdpa"
)

// vDPA Netlink command.
const (
	_ uint8 = iota
	_
	vDPACmdMgmtDevGet
	vDPACmdDevNew
	vDPACmdDevDel
	vDPACmdDevGet
	_
)

// vDPA Netlink Attributes.
const (
	_ = iota

	// bus name (optional) + dev name together make the parent device handle.
	vDPAAttrMgmtDevBusName          // string
	vDPAAttrMgmtDevDevName          // string
	vDPAAttrMgmtDevSupportedClasses // u64

	vDPAAttrDevName      // string
	vDPAAttrDevID        // u32
	vDPAAttrDevVendorID  // u32
	vDPAAttrDevMaxVqs    // u32
	vDPAAttrDevMaxVqSize // u16
	vDPAAttrDevMinVqSize // u16

	vDPAAttrDevNetCfgMacAddr // binary
	vDPAAttrDevNetStatus     // u8
	vDPAAttrDevNetCfgMaxVqp  // u16
	vDPAAttrGetNetCfgMTU     // u16
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

// VhostVdpa is the vhost-vdpa device information.
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

// ParseAttributes parses the attributes of a netlink message for a vDPA management device.
func (d *MgmtVDPADev) parseAttributes(attrs []syscall.NetlinkRouteAttr) error {
	for _, attr := range attrs {
		switch attr.Attr.Type {
		case vDPAAttrMgmtDevBusName:
			d.BusName = string(attr.Value[:len(attr.Value)-1])
		case vDPAAttrMgmtDevDevName:
			d.DevName = string(attr.Value[:len(attr.Value)-1])
		}
	}
	return nil
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

// ParseAttributes parses the attributes of a netlink message for a vDPA device.
func (d *VDPADev) parseAttributes(attrs []syscall.NetlinkRouteAttr) error {
	d.MgmtDev = &MgmtVDPADev{}
	for _, attr := range attrs {
		switch attr.Attr.Type {
		case vDPAAttrDevName:
			d.Name = string(attr.Value[:len(attr.Value)-1])
		case vDPAAttrDevMaxVqs:
			d.MaxVQs = binary.LittleEndian.Uint32(attr.Value[:len(attr.Value)])
		case vDPAAttrMgmtDevBusName:
			d.MgmtDev.BusName = string(attr.Value[:len(attr.Value)-1])
		case vDPAAttrMgmtDevDevName:
			d.MgmtDev.DevName = string(attr.Value[:len(attr.Value)-1])
		}
	}

	// Get the vhost-vdpa device associated with the vDPA device.
	vhostVDPA, err := getVhostVDPADevInPath(filepath.Join(vdpaBusDevDir, d.Name))
	if err != nil {
		return err
	}

	d.VhostVDPA = vhostVDPA
	return nil
}

// runVDPANetlinkCmd executes a vDPA netlink command and returns the response.
func runVDPANetlinkCmd(command uint8, flags int, data []*nl.RtAttr) ([][]byte, error) {
	f, err := netlink.GenlFamilyGet(vDPAGenlName)
	if err != nil {
		return nil, fmt.Errorf("Could not get the vDPA Netlink family : %v", err)
	}

	msg := &nl.Genlmsg{
		Command: command,
		Version: nl.GENL_CTRL_VERSION,
	}

	req := nl.NewNetlinkRequest(int(f.ID), commonNLFlags|flags)
	// Pass the data into the request header
	req.AddData(msg)
	for _, d := range data {
		req.AddData(d)
	}

	// Execute the request
	msgs, err := req.Execute(syscall.NETLINK_GENERIC, 0)
	if err != nil {
		return nil, fmt.Errorf("Could not execute vDPA Netlink request : %v", err)
	}

	return msgs, nil
}

// newNetlinkAttribute creates a new netlink attribute based on the attribute type and data.
func newNetlinkAttribute(attrType int, data any) (*nl.RtAttr, error) {
	switch attrType {
	case vDPAAttrMgmtDevBusName, vDPAAttrMgmtDevDevName, vDPAAttrDevName:
		strData, ok := data.(string)
		if !ok {
			return nil, fmt.Errorf("Netlink attribute type %d requires string data", attrType)
		}

		bytes := make([]byte, len(strData)+1)
		copy(bytes, strData)
		return nl.NewRtAttr(attrType, bytes), nil
	case vDPAAttrMgmtDevSupportedClasses:
		u64Data, ok := data.(uint64)
		if !ok {
			return nil, fmt.Errorf("Netlink attribute type %d requires uint64 data", attrType)
		}

		return nl.NewRtAttr(attrType, nl.Uint64Attr(u64Data)), nil
	case vDPAAttrDevID, vDPAAttrDevVendorID, vDPAAttrDevMaxVqs:
		u32Data, ok := data.(uint32)
		if !ok {
			return nil, fmt.Errorf("Netlink attribute type %d requires uint32 data", attrType)
		}

		return nl.NewRtAttr(attrType, nl.Uint32Attr(u32Data)), nil
	case vDPAAttrDevMaxVqSize, vDPAAttrDevMinVqSize, vDPAAttrDevNetCfgMaxVqp, vDPAAttrGetNetCfgMTU:
		u16Data, ok := data.(uint16)
		if !ok {
			return nil, fmt.Errorf("Netlink attribute type %d requires uint16 data", attrType)
		}

		return nl.NewRtAttr(attrType, nl.Uint16Attr(u16Data)), nil
	case vDPAAttrDevNetStatus:
		u8Data, ok := data.(uint8)
		if !ok {
			return nil, fmt.Errorf("Netlink attribute type %d requires uint8 data", attrType)
		}

		return nl.NewRtAttr(attrType, nl.Uint8Attr(u8Data)), nil
	case vDPAAttrDevNetCfgMacAddr:
		binData, ok := data.([]byte)
		if !ok {
			return nil, fmt.Errorf("Netlink attribute type %d requires []byte data", attrType)
		}

		return nl.NewRtAttr(attrType, binData), nil
	default:
		return nil, fmt.Errorf("Unknown netlink attribute type %d", attrType)
	}
}

// parseMgmtVDPADevList parses a list of vDPA management device netlink messages.
func parseMgmtVDPADevList(msgs [][]byte) ([]*MgmtVDPADev, error) {
	devices := make([]*MgmtVDPADev, 0, len(msgs))
	for _, m := range msgs {
		attrs, err := nl.ParseRouteAttr(m[nl.SizeofGenlmsg:])
		if err != nil {
			return nil, fmt.Errorf("Could not parse Netlink vDPA management device route attributes : %v", err)
		}

		dev := &MgmtVDPADev{}
		if err = dev.parseAttributes(attrs); err != nil {
			return nil, err
		}

		devices = append(devices, dev)
	}

	return devices, nil
}

// parseVDPADevList parses a list of vDPA device netlink messages.
func parseVDPADevList(msgs [][]byte) ([]*VDPADev, error) {
	devices := make([]*VDPADev, 0, len(msgs))
	for _, m := range msgs {
		attrs, err := nl.ParseRouteAttr(m[nl.SizeofGenlmsg:])
		if err != nil {
			return nil, fmt.Errorf("Could not parse Netlink vDPA device route attributes : %v", err)
		}

		dev := &VDPADev{}
		if err = dev.parseAttributes(attrs); err != nil {
			return nil, err
		}

		devices = append(devices, dev)
	}

	return devices, nil
}

// ListVDPAMgmtDevices returns the list of all vDPA management devices.
func ListVDPAMgmtDevices() ([]*MgmtVDPADev, error) {
	resp, err := runVDPANetlinkCmd(vDPACmdMgmtDevGet, syscall.NLM_F_DUMP, nil)
	if err != nil {
		return nil, err
	}

	mgtmDevs, err := parseMgmtVDPADevList(resp)
	if err != nil {
		return nil, err
	}

	return mgtmDevs, nil
}

// ListVDPADevices returns the list of all vDPA devices.
func ListVDPADevices() ([]*VDPADev, error) {
	resp, err := runVDPANetlinkCmd(vDPACmdDevGet, syscall.NLM_F_DUMP, nil)
	if err != nil {
		return nil, err
	}

	devices, err := parseVDPADevList(resp)
	if err != nil {
		return nil, err
	}

	return devices, nil
}

// AddVDPADevice adds a new vDPA device.
func AddVDPADevice(pciDevSlotName string, volatile map[string]string) (*VDPADev, error) {
	// List existing vDPA devices
	vdpaDevs, err := ListVDPADevices()
	if err != nil {
		return nil, err
	}

	existingVDPADevNames := make(map[string]struct{})
	for _, vdpaDev := range vdpaDevs {
		existingVDPADevNames[vdpaDev.Name] = struct{}{}
	}

	// Create the netlink attributes
	header := []*nl.RtAttr{}
	busName, err := newNetlinkAttribute(vDPAAttrMgmtDevBusName, "pci")
	if err != nil {
		return nil, fmt.Errorf("Failed creating vDPA `busName` netlink attr : %v", err)
	}

	header = append(header, busName)

	mgmtDevDevName, err := newNetlinkAttribute(vDPAAttrMgmtDevDevName, pciDevSlotName)
	if err != nil {
		return nil, fmt.Errorf("Failed creating vDPA `pciDevSlotName` netlink attr : %v", err)
	}

	header = append(header, mgmtDevDevName)

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

	devName, err := newNetlinkAttribute(vDPAAttrDevName, generatedVDPADevName)
	if err != nil {
		return nil, fmt.Errorf("Failed creating vDPA `generatedVDPADevName` netlink attr : %v", err)
	}

	header = append(header, devName)

	maxVQP, err := newNetlinkAttribute(vDPAAttrDevNetCfgMaxVqp, vDPAMaxVQP)
	if err != nil {
		return nil, fmt.Errorf("Failed creating vDPA `maxVQP` netlink attr : %v", err)
	}

	header = append(header, maxVQP)

	_, err = runVDPANetlinkCmd(vDPACmdDevNew, 0, header)
	if err != nil {
		return nil, fmt.Errorf("Failed creating vDPA device : %v", err)
	}

	// Now that the vDPA device has been created in the kernel, return the VDPADev struct
	msgs, err := runVDPANetlinkCmd(vDPACmdDevGet, 0, []*nl.RtAttr{devName})
	if err != nil {
		return nil, fmt.Errorf("Failed getting vDPA device : %v", err)
	}

	vdpaDevs, err = parseVDPADevList(msgs)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing vDPA device : %v", err)
	}

	// Update the volatile map
	volatile["last_state.vdpa.name"] = generatedVDPADevName

	return vdpaDevs[0], nil
}

// DeleteVDPADevice deletes a vDPA management device.
func DeleteVDPADevice(vDPADevName string) error {
	header := []*nl.RtAttr{}
	devName, err := newNetlinkAttribute(vDPAAttrDevName, vDPADevName)
	if err != nil {
		return fmt.Errorf("Failed creating vDPA `vDPADevName` netlink attr : %v", err)
	}

	header = append(header, devName)

	_, err = runVDPANetlinkCmd(vDPACmdDevDel, 0, header)
	if err != nil {
		return fmt.Errorf("Cannot delete VDPA dev: %v", err)
	}

	return nil
}
