package uefi

import (
	"fmt"
	"strconv"
	"strings"
)

// WrapDP wraps a device path dissector.
func wrapDP(f func(*reader, uint8) (string, error)) func(uint8, []byte) (string, error) {
	return func(subtype uint8, b []byte) (string, error) {
		r := newReader(b)
		v, err := f(r, subtype)
		if err != nil {
			return "", err
		}

		if !r.eof() {
			return "", errUnexpectedData
		}

		return v, nil
	}
}

// dpFormatter helps formatting device paths with optional arguments.
type dpFormatter struct {
	name          string
	firstOptional int
	args          []string
}

// add adds an argument to a device path formatter.
func (d *dpFormatter) add(value string, optional ...bool) {
	opt := false
	if len(optional) > 0 {
		opt = optional[0]
	}

	d.args = append(d.args, value)
	if !opt {
		d.firstOptional = len(d.args)
	}
}

// addMandatory adds mandatory arguments to a device path formatter.
func (d *dpFormatter) addMandatory(values ...string) {
	for _, value := range values {
		d.add(value)
	}
}

// String returns the formatted device path.
func (d *dpFormatter) String() string {
	return fmt.Sprintf("%s(%s)", d.name, strings.Join(d.args[:d.firstOptional], ","))
}

// hardwareDevicePath dissects a device path node with type 0x01.
var hardwareDevicePath = wrapDP(func(r *reader, subtype uint8) (string, error) {
	switch subtype {
	case 0x01: // PCI.
		fn, err := r.readU8()
		if err != nil {
			return "", err
		}

		dev, err := r.readU8()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Pci(0x%x,0x%x)", dev, fn), nil
	case 0x02: // PCCARD.
		fn, err := r.readU8()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("PcCard(0x%x)", fn), nil
	case 0x03: // Memory Mapped.
		memType, err := r.readU32()
		if err != nil {
			return "", err
		}

		memStart, err := r.readU64()
		if err != nil {
			return "", err
		}

		memEnd, err := r.readU64()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("MemoryMapped(0x%x,0x%x,0x%x)", memType, memStart, memEnd), nil
	case 0x04: // Vendor.
		guid, err := r.readGUID()
		if err != nil {
			return "", err
		}

		remaining, err := r.read(r.rem())
		if err != nil {
			return "", err
		}

		path := dpFormatter{name: "VenHw"}
		path.add(guid)
		path.add(fmt.Sprintf("%x", remaining), len(remaining) == 0)
		return path.String(), nil
	case 0x05: // Controller.
		ctrl, err := r.readU32()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Ctrl(0x%x)", ctrl), nil
	case 0x06: // BMC.
		bmcType, err := r.readU8()
		if err != nil {
			return "", err
		}

		baseAddr, err := r.readU8()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("BMC(0x%x,0x%x)", bmcType, baseAddr), nil
	}

	return "", errUnexpectedData
})

// acpiDevicePath dissects a device path node with type 0x02.
var acpiDevicePath = wrapDP(func(r *reader, subtype uint8) (string, error) {
	switch subtype {
	case 0x01: // ACPI Device Path.
		hid, err := r.readEISA()
		if err != nil {
			return "", err
		}

		uid, err := r.readU32()
		if err != nil {
			return "", err
		}

		switch hid {
		case "PNP0301":
			return fmt.Sprintf("Keyboard(0x%x)", uid), nil
		case "PNP0401":
			return fmt.Sprintf("ParallelPort(0x%x)", uid), nil
		case "PNP0501":
			return fmt.Sprintf("Serial(0x%x)", uid), nil
		case "PNP0604":
			return fmt.Sprintf("Floppy(0x%x)", uid), nil
		case "PNP0A03":
			return fmt.Sprintf("PciRoot(0x%x)", uid), nil
		case "PNP0A08":
			return fmt.Sprintf("PcieRoot(0x%x)", uid), nil
		default:
			path := dpFormatter{name: "Acpi"}
			path.add(hid)
			path.add(fmt.Sprintf("0x%x", uid), uid == 0)
			return path.String(), nil
		}

	case 0x02: // Expanded ACPI Device Path.
		hid, err := r.readEISA()
		if err != nil {
			return "", err
		}

		uid, err := r.readU32()
		if err != nil {
			return "", err
		}

		cid, err := r.readEISA()
		if err != nil {
			return "", err
		}

		hidStr, err := r.readZn8()
		if err != nil {
			return "", err
		}

		uidStr, err := r.readZn8()
		if err != nil {
			return "", err
		}

		cidStr, err := r.readZn8()
		if err != nil {
			return "", err
		}

		displayedUID := fmt.Sprintf("0x%x", uid)
		if len(uidStr) > 0 {
			displayedUID = uidStr
		}

		if hid == "PNP0A03" || cid == "PNP0A03" && hid != "PNP0A08" {
			return fmt.Sprintf("PciRoot(0x%x)", displayedUID), nil
		} else if hid == "PNP0A08" || cid == "PNP0A08" {
			return fmt.Sprintf("PcieRoot(0x%x)", displayedUID), nil
		}

		if len(hidStr) == 0 && len(cidStr) == 0 && len(uidStr) > 0 {
			return fmt.Sprintf("AcpiExp(%s,%s,%s)", hid, cid, uidStr), nil
		}

		path := dpFormatter{name: "AcpiEx"}
		path.addMandatory(hid, cid, fmt.Sprintf("0x%x", uid))
		path.add(hidStr, len(hidStr) == 0)
		path.add(cidStr, len(cidStr) == 0)
		path.add(uidStr, len(uidStr) == 0)
		return path.String(), nil
	case 0x03: // _ADR Device Path.
		var adrs []string
		for !r.eof() {
			adr, err := r.readU32()
			if err != nil {
				return "", err
			}

			adrs = append(adrs, fmt.Sprintf("0x%x", adr))
		}

		return fmt.Sprintf("AcpiAdr(%s)", strings.Join(adrs, ",")), nil
	case 0x04: // NVDIMM Device.
		nfit, err := r.readU32()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("NvdimmAcpiAdr(0x%x)", nfit), nil
	}

	return "", errUnexpectedData
})

// fcDevicePath dissects a Fibre Channel device path node. The `ex` parameter switches the binary
// parsing logic to big-endian.
func fcDevicePath(r *reader, ex bool) (string, error) {
	err := r.skip(4)
	if err != nil {
		return "", err
	}

	var wwn, lun uint64
	name := "Fibre"
	if ex {
		name = "FibreEx"
		wwn, err = r.readU64BE()
		if err != nil {
			return "", err
		}

		lun, err = r.readU64BE()
	} else {
		wwn, err = r.readU64()
		if err != nil {
			return "", err
		}

		lun, err = r.readU64()
	}

	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s(0x%x,0x%x)", name, wwn, lun), nil
}

// sasDevicePath dissects a SAS device path node. The `ex` parameter switches the binary parsing
// logic to big-endian.
func sasDevicePath(r *reader, ex bool, reserved uint32) (string, error) {
	var address, lun uint64
	var err error
	name := "Sas"
	if ex {
		name = "SasEx"
		address, err = r.readU64BE()
		if err != nil {
			return "", err
		}

		lun, err = r.readU64BE()
	} else {
		address, err = r.readU64()
		if err != nil {
			return "", err
		}

		lun, err = r.readU64()
	}

	if err != nil {
		return "", err
	}

	args := []string{fmt.Sprintf("0x%x", address)}

	topology, err := r.readU8()
	if err != nil {
		return "", err
	}

	moreInfo := false
	moreInfoNext := false
	switch topology & 0x0F {
	case 0x02:
		moreInfoNext = true
		fallthrough
	case 0x01:
		moreInfo = true
	}

	sasSATA := "NoTopology"
	external := false
	var connectStr string
	if moreInfo {
		if topology&0x10 == 0x10 {
			sasSATA = "SATA"
		} else {
			sasSATA = "SAS"
		}

		if topology&0x20 == 0x20 {
			external = true
		}

		connect := topology >> 6
		var ok bool
		connectStr, ok = map[uint8]string{0: "Direct", 1: "Expanded"}[connect]
		if !ok {
			connectStr = fmt.Sprintf("0x%x", connect)
		}
	}

	bay, err := r.readU8()
	if err != nil {
		return "", err
	}

	rtp, err := r.readU16()
	if err != nil {
		return "", err
	}

	// We need to print the LUN if a RTP is specified in order to disambiguate.
	if lun != 0 || rtp != 0 {
		args = append(args, fmt.Sprintf("0x%x", lun))
	}

	if rtp != 0 {
		args = append(args, fmt.Sprintf("0x%x", rtp))
	}

	// We need to print the topology if reserved data are specified in order to disambiguate.
	if sasSATA != "NoTopology" || reserved != 0 {
		args = append(args, sasSATA)
	}

	if sasSATA != "NoTopology" {
		if external {
			args = append(args, "External")
		}

		if connectStr != "Direct" {
			args = append(args, connectStr)
		}
	}

	if moreInfoNext {
		// We need to print the bay as a plain integer in order to disambiguate, and because of the
		// strange 1-offset.
		args = append(args, fmt.Sprintf("%d", bay+1))
	}

	if reserved != 0 {
		args = append(args, fmt.Sprintf("0x%x", reserved))
	}

	return fmt.Sprintf("%s(%s)", name, strings.Join(args, ",")), nil
}

// messagingDevicePath dissects a device path node with type 0x03.
var messagingDevicePath = wrapDP(func(r *reader, subtype uint8) (string, error) {
	switch subtype {
	case 0x01: // ATAPI.
		controller, err := r.readE8(map[uint8]string{0: "Primary", 1: "Secondary"}, true)
		if err != nil {
			return "", err
		}

		drive, err := r.readE8(map[uint8]string{0: "Master", 1: "Slave"}, true)
		if err != nil {
			return "", err
		}

		lun, err := r.readU16()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Ata(%s,%s,0x%x)", controller, drive, lun), nil
	case 0x02: // SCSI.
		pun, err := r.readU16()
		if err != nil {
			return "", err
		}

		lun, err := r.readU16()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Scsi(0x%x,0x%x)", pun, lun), nil
	case 0x03: // Fibre Channel.
		return fcDevicePath(r, false)
	case 0x04: // 1394.
		err := r.skip(4)
		if err != nil {
			return "", err
		}

		guid, err := r.readGUID()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("I1394(%s)", guid), nil
	case 0x05: // USB.
		port, err := r.readU8()
		if err != nil {
			return "", err
		}

		intf, err := r.readU8()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("USB(0x%x,0x%x)", port, intf), nil
	case 0x06: // I2O Random Block Storage Class.
		tid, err := r.readU32()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("I2O(0x%x)", tid), nil
	case 0x09: // InfiniBand.
		flags, err := r.readU32()
		if err != nil {
			return "", err
		}

		gid, err := r.read(16)
		if err != nil {
			return "", err
		}

		serviceID, err := r.readU64()
		if err != nil {
			return "", err
		}

		targetID, err := r.readU64()
		if err != nil {
			return "", err
		}

		deviceID, err := r.readU64()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Infiniband(0x%x,0x%x,0x%x,0x%x,0x%x)", flags, gid, serviceID, targetID, deviceID), nil
	case 0x0a: // Vendor.
		guid, err := r.readGUID()
		if err != nil {
			return "", err
		}

		switch guid {
		case EfiPcAnsiGuid:
			return "VenPcAnsi()", nil
		case EfiVT100Guid:
			return "VenVt100()", nil
		case EfiVT100PlusGuid:
			return "VenVt100Plus()", nil
		case EfiVTUTF8Guid:
			return "VenUtf8()", nil
		case EfiUartDevicePathGuid:
			flow, err := r.readE32(map[uint32]string{0: "None", 1: "Hardware", 2: "XonXoff"}, true)
			if err != nil {
				return "", err
			}

			return fmt.Sprintf("UartFlowCtrl(%s)", flow), nil
		case EfiSasDevicePathGuid:
			reserved, err := r.readU32()
			if err != nil {
				return "", err
			}

			return sasDevicePath(r, false, reserved)
		case EfiDebugPortProtocolGuid:
			return "DebugPort()", nil
		}

		remaining, err := r.read(r.rem())
		if err != nil {
			return "", err
		}

		path := dpFormatter{name: "VenMsg"}
		path.add(guid)
		path.add(fmt.Sprintf("%x", remaining), len(remaining) == 0)
		return path.String(), nil
	case 0x0b: // MAC Address for a network interface.
		mac, err := r.read(32)
		if err != nil {
			return "", err
		}

		ifType, err := r.readU8()
		if err != nil {
			return "", err
		}

		if ifType == 0x00 || ifType == 0x01 {
			mac = mac[:6]
		}

		path := dpFormatter{name: "MAC"}
		path.add(fmt.Sprintf("%x", mac))
		path.add(fmt.Sprintf("0x%x", ifType), ifType == 0x00)
		return path.String(), nil
	case 0x0c: // IPv4.
		localRaw, err := r.read(4)
		if err != nil {
			return "", err
		}

		remoteRaw, err := r.read(4)
		if err != nil {
			return "", err
		}

		localPort, err := r.readU16()
		if err != nil {
			return "", err
		}

		remotePort, err := r.readU16()
		if err != nil {
			return "", err
		}

		protocol, err := r.readE16(map[uint16]string{6: "TCP", 17: "UDP"}, false)
		if err != nil {
			return "", err
		}

		ipType, err := r.readE8(map[uint8]string{0: "DHCP", 1: "Static"}, true)
		if err != nil {
			return "", err
		}

		gatewayRaw, err := r.read(4)
		if err != nil {
			return "", err
		}

		maskRaw, err := r.read(4)
		if err != nil {
			return "", err
		}

		localIP := formatIP(localRaw, localPort)
		remoteIP := formatIP(remoteRaw, remotePort)
		gatewayIP := formatIP(gatewayRaw)
		maskIP := formatIP(maskRaw)

		path := dpFormatter{name: "IPv4"}
		path.add(remoteIP)
		path.add(protocol, protocol == "UDP")
		path.add(ipType, ipType == "DHCP")
		path.add(localIP, localIP == "0.0.0.0")
		path.add(gatewayIP, gatewayIP == "0.0.0.0")
		path.add(maskIP, maskIP == "0.0.0.0")
		return path.String(), nil
	case 0x0d: // IPv6.
		localRaw, err := r.read(16)
		if err != nil {
			return "", err
		}

		remoteRaw, err := r.read(16)
		if err != nil {
			return "", err
		}

		localPort, err := r.readU16()
		if err != nil {
			return "", err
		}

		remotePort, err := r.readU16()
		if err != nil {
			return "", err
		}

		protocol, err := r.readE16(map[uint16]string{6: "TCP", 17: "UDP"}, false)
		if err != nil {
			return "", err
		}

		origin, err := r.readE8(map[uint8]string{0: "Static", 1: "StatelessAutoConfigure", 2: "StatefulAutoConfigure"}, true)
		if err != nil {
			return "", err
		}

		prefix, err := r.readU8()
		if err != nil {
			return "", err
		}

		gatewayRaw, err := r.read(16)
		if err != nil {
			return "", err
		}

		localIP := formatIP6(localRaw, localPort)
		remoteIP := formatIP6(remoteRaw, remotePort)
		gatewayIP := formatIP6(gatewayRaw)

		path := dpFormatter{name: "IPv6"}
		path.add(remoteIP)
		// The specification states that there is a default value even if the next field doesn’t.
		path.add(protocol, protocol == "UDP")
		path.add(origin)
		path.add(localIP, localIP == "[::]")
		path.add(gatewayIP, gatewayIP == "[::]")
		// The specification doesn’t give any hint on how to display the prefix length. We are choosing
		// to display is as an integer defaulting to 64.
		path.add(strconv.Itoa(int(prefix)), prefix == 64)
		return path.String(), nil
	case 0x0e: // UART.
		err := r.skip(4)
		if err != nil {
			return "", err
		}

		baudRate, err := r.readU64()
		if err != nil {
			return "", err
		}

		dataBits, err := r.readU8()
		if err != nil {
			return "", err
		}

		parity, err := r.readE8(map[uint8]string{0: "D", 1: "N", 2: "E", 3: "O", 4: "M", 5: "S"}, false)
		if err != nil {
			return "", err
		}

		stopBits, err := r.readE8(map[byte]string{0: "D", 1: "1", 2: "1.5", 3: "2"}, false)
		if err != nil {
			return "", err
		}

		path := dpFormatter{name: "Uart"}
		path.add(strconv.FormatUint(baudRate, 10), baudRate == 115200)
		path.add(strconv.Itoa(int(dataBits)), dataBits == 8)
		path.add(parity, parity == "D")
		path.add(stopBits, stopBits == "D")
		return path.String(), nil
	case 0x0f: // USB Class.
		vid, err := r.readU16()
		if err != nil {
			return "", err
		}

		pid, err := r.readU16()
		if err != nil {
			return "", err
		}

		usbClass, err := r.readU8()
		if err != nil {
			return "", err
		}

		usbSubclass, err := r.readU8()
		if err != nil {
			return "", err
		}

		protocol, err := r.readU8()
		if err != nil {
			return "", err
		}

		// UsbAppSpecific is not defined in UEFI, but helps us discriminate subclasses after.
		name, knownClass := map[uint8]string{1: "UsbAudio", 2: "UsbCDCControl", 3: "UsbHID", 6: "UsbImage", 7: "UsbPrinter", 8: "UsbMassStorage", 9: "UsbHub", 10: "UsbCDCData", 11: "UsbSmartCard", 14: "UsbVideo", 220: "UsbDiagnostic", 224: "UsbWireless", 254: "UsbAppSpecific"}[usbClass]
		knownSubclass := false
		if name == "UsbAppSpecific" {
			name, knownSubclass = map[uint8]string{1: "UsbDeviceFirmwareUpdate", 2: "UsbIrdaBridge", 3: "UsbTestAndMeasurement"}[usbSubclass]
			// Use the generic handler if the subclass is not known.
			if !knownSubclass {
				knownClass = false
			}
		}

		if !knownClass {
			name = "UsbClass"
		}

		path := dpFormatter{name: name}
		path.add(fmt.Sprintf("0x%x", vid), vid == 0xFFFF)
		path.add(fmt.Sprintf("0x%x", pid), pid == 0xFFFF)
		if !knownClass {
			path.add(fmt.Sprintf("0x%x", usbClass), usbClass == 0xFF)
		}

		if !knownSubclass {
			path.add(fmt.Sprintf("0x%x", usbSubclass), usbSubclass == 0xFF)
		}

		path.add(fmt.Sprintf("0x%x", protocol), protocol == 0xFF)
		return path.String(), nil
	case 0x10: // USB WWID.
		usbInterface, err := r.readU16()
		if err != nil {
			return "", err
		}

		vid, err := r.readU16()
		if err != nil {
			return "", err
		}

		pid, err := r.readU16()
		if err != nil {
			return "", err
		}

		sn, err := r.readZ16(r.rem() / 2)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("UsbWwid(0x%x,0x%x,0x%x,%q)", vid, pid, usbInterface, sn), nil
	case 0x11: // Device Logical unit.
		lun, err := r.readU8()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Unit(0x%x)", lun), nil
	case 0x12: // SATA.
		hpn, err := r.readU16()
		if err != nil {
			return "", err
		}

		pmpn, err := r.readU16()
		if err != nil {
			return "", err
		}

		lun, err := r.readU16()
		if err != nil {
			return "", err
		}

		path := dpFormatter{name: "Sata"}
		path.add(fmt.Sprintf("0x%x", hpn))
		path.add(fmt.Sprintf("0x%x", pmpn), pmpn == 0xFFFF)
		// The UEFI specification marks this parameter as mandatory, but we don’t.
		path.add(fmt.Sprintf("0x%x", lun), lun == 0x0000)
		return path.String(), nil
	case 0x13: // iSCSI.
		protocol, err := r.readE16(map[uint16]string{0: "TCP"}, false)
		if err != nil {
			return "", err
		}

		options, err := r.readU16()
		if err != nil {
			return "", err
		}

		headerDigest, ok := map[uint16]string{0x0000: "None", 0x0002: "CRC32C"}[options&0x0003]
		if !ok {
			return "", errUnexpectedData
		}

		dataDigest, ok := map[uint16]string{0x0000: "None", 0x0008: "CRC32C"}[options&0x000c]
		if !ok {
			return "", errUnexpectedData
		}

		authentication, ok := map[uint16]string{0x0000: "CHAP_BI", 0x0800: "None", 0x1000: "CHAP_UNI"}[options&0x1c00]
		if !ok {
			return "", errUnexpectedData
		}

		lun, err := r.readU64BE()
		if err != nil {
			return "", err
		}

		portalGroup, err := r.readU16()
		if err != nil {
			return "", err
		}

		targetName, err := r.readZ16(r.rem() / 2)
		if err != nil {
			return "", err
		}

		path := dpFormatter{name: "iSCSI"}
		path.addMandatory(targetName, fmt.Sprintf("0x%x", portalGroup), fmt.Sprintf("0x%x", lun))
		path.add(headerDigest, headerDigest == "None")
		path.add(dataDigest, dataDigest == "None")
		path.add(authentication, authentication == "None")
		path.add(protocol, protocol == "TCP")
		return path.String(), nil
	case 0x14: // Vlan (802.1q).
		vlan, err := r.readU16()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Vlan(%d)", vlan), nil
	case 0x15: // Fibre Channel Ex.
		return fcDevicePath(r, true)
	case 0x16: // SAS Ex.
		return sasDevicePath(r, true, 0)
	case 0x17: // NVM Express Namespace.
		nsid, err := r.readU32()
		if err != nil {
			return "", err
		}

		eui, err := r.readEUI64()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("NVMe(0x%x,%s)", nsid, eui), nil
	case 0x18: // Universal Resource Identifier (URI) Device Path.
		uri, err := r.readZ8(r.rem())
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Uri(%s)", uri), nil
	case 0x19: // UFS.
		pun, err := r.readU8()
		if err != nil {
			return "", err
		}

		lun, err := r.readU8()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("UFS(0x%x,0x%x)", pun, lun), nil
	case 0x1a: // SD.
		slot, err := r.readU8()
		if err != nil {
			return "", err
		}

		path := dpFormatter{name: "SD"}
		path.add(fmt.Sprintf("0x%x", slot), slot != 0)
		return path.String(), nil
	case 0x1b: // Bluetooth.
		addr, err := r.read(6)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Bluetooth(%x)", addr), nil
	case 0x1c: // Wi-Fi Device Path.
		ssid, err := r.read(32)
		if err != nil {
			return "", err
		}

		// Sane string parsing strategies unfortunately don’t apply to SSIDs, which can contain null
		// bytes.
		return fmt.Sprintf("Wi-Fi(%q)", strings.TrimRight(string(ssid), "\x00")), nil
	case 0x1d: // eMMC.
		slot, err := r.readU8()
		if err != nil {
			return "", err
		}

		path := dpFormatter{name: "eMMC"}
		path.add(fmt.Sprintf("0x%x", slot), slot != 0)
		return path.String(), nil
	case 0x1e: // BluetoothLE.
		addr, err := r.read(6)
		if err != nil {
			return "", err
		}

		addrType, err := r.readU8()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("BluetoothLE(%x,0x%x)", addr, addrType), nil
	case 0x1f: // DNS Device Path.
		v6, err := r.readB8()
		if err != nil {
			return "", err
		}

		var ips []string
		for !r.eof() {
			ip, err := r.read(16)
			if err != nil {
				return "", err
			}

			if v6 {
				ips = append(ips, formatIP6(ip))
			} else {
				ips = append(ips, formatIP(ip))
			}
		}

		return fmt.Sprintf("Dns(%s)", strings.Join(ips, ",")), nil
	case 0x20: // NVDIMM Namespace.
		uuid, err := r.readGUID()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("NVDIMM(%s)", uuid), nil
	case 0x21: // REST Service Device Path.
		service, err := r.readU8()
		if err != nil {
			return "", err
		}

		access, err := r.readU8()
		if err != nil {
			return "", err
		}

		path := dpFormatter{name: "RestService"}
		path.addMandatory(fmt.Sprintf("0x%x", service), fmt.Sprintf("0x%x", access))

		if service == 0xFF {
			guid, err := r.readGUID()
			if err != nil {
				return "", err
			}

			path.add(guid)
			if !r.eof() {
				remaining, err := r.read(r.rem())
				if err != nil {
					return "", err
				}

				path.add(fmt.Sprintf("%x", remaining))
			}
		}

		return path.String(), nil
	case 0x22: // NVMe-oF Namespace Device Path.
		nidt, err := r.readE8(map[uint8]string{1: "eui", 2: "nvme-nguid", 3: "urn:uuid"}, true)
		if err != nil {
			return "", err
		}

		var nid string
		switch nidt {
		case "eui":
			nid, err = r.readEUI64BE()
			if err != nil {
				return "", err
			}

			err = r.skip(8)
			if err != nil {
				return "", err
			}

		case "nvme-nguid":
			b, err := r.read(16)
			if err != nil {
				return "", err
			}

			nid = fmt.Sprintf("%X-%X-%X", b[0:8], b[8:11], b[11:16])
		case "urn:uuid":
			nid, err = r.readGUIDBE()
			if err != nil {
				return "", err
			}
		}

		nqn, err := r.readZn8(r.rem())
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("NVMEoF(%s,%s:%s)", nqn, nidt, nid), nil
	}

	return "", errUnexpectedData
})

// mediaDevicePath dissects a device path node with type 0x04.
var mediaDevicePath = wrapDP(func(r *reader, subtype uint8) (string, error) {
	switch subtype {
	case 0x01: // Hard Drive.
		partition, err := r.readU32()
		if err != nil {
			return "", err
		}

		start, err := r.readU64()
		if err != nil {
			return "", err
		}

		size, err := r.readU64()
		if err != nil {
			return "", err
		}

		signature, err := r.read(16)
		if err != nil {
			return "", err
		}

		format, err := r.readE8(map[uint8]string{1: "MBR", 2: "GPT"}, true)
		if err != nil {
			return "", err
		}

		// The UEFI specification does not handle format / signature type mismatches, so we do the same.
		// Additionally, it doesn’t explain how to handle non-0x01 or 0x02 signature types.
		sigType, err := r.readE8(map[uint8]string{1: "MBR", 2: "GPT"}, true)
		if err != nil {
			return "", err
		}

		if sigType != format {
			return "", errUnexpectedData
		}

		path := dpFormatter{name: "HD"}
		path.add(fmt.Sprintf("0x%x", partition), partition == 0)
		path.add(format, format == "GPT")
		// The UEFI specification marks this parameter as mandatory, thus making the previous ones being
		// optional moot.
		if sigType == "MBR" {
			path.add(fmt.Sprintf("0x%x", signature[:4]))
		} else {
			path.add(formatGUID(signature))
		}

		if partition != 0 {
			// Most tools format those as integers, so we do the same.
			path.add(fmt.Sprintf("%d", start))
			path.add(fmt.Sprintf("%d", size))
		}

		return path.String(), nil
	case 0x02: // CD-ROM “El Torito” Format.
		entry, err := r.readU32()
		if err != nil {
			return "", err
		}

		start, err := r.readU64()
		if err != nil {
			return "", err
		}

		size, err := r.readU64()
		if err != nil {
			return "", err
		}

		// Most tools format those as integers, so we do the same.
		return fmt.Sprintf("CDROM(%d,%d,%d)", entry, start, size), nil
	case 0x03: // Vendor.
		guid, err := r.readGUID()
		if err != nil {
			return "", err
		}

		remaining, err := r.read(r.rem())
		if err != nil {
			return "", err
		}

		path := dpFormatter{name: "VenMedia"}
		path.add(guid)
		path.add(fmt.Sprintf("%x", remaining), len(remaining) == 0)
		return path.String(), nil
	case 0x04: // File Path.
		path, err := r.readZn16(r.rem())
		if err != nil {
			return "", err
		}

		return path, nil
	case 0x05: // Media Protocol.
		guid, err := r.readGUID()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Media(%s)", guid), nil
	case 0x06: // PIWG Firmware File.
		guid, err := r.readGUID()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("FvFile(%s)", guid), nil
	case 0x07: // PIWG Firmware Volume.
		guid, err := r.readGUID()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Fv(%s)", guid), nil
	case 0x08: // Relative Offset Range.
		err := r.skip(4)
		if err != nil {
			return "", err
		}

		start, err := r.readU64()
		if err != nil {
			return "", err
		}

		end, err := r.readU64()
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("Offset(0x%x,0x%x)", start, end), nil
	case 0x09: // RAM Disk Device Path.
		start, err := r.readU64()
		if err != nil {
			return "", err
		}

		end, err := r.readU64()
		if err != nil {
			return "", err
		}

		guid, err := r.readGUID()
		if err != nil {
			return "", err
		}

		instance, err := r.readU16()
		if err != nil {
			return "", err
		}

		path := dpFormatter{name: "RamDisk"}
		path.addMandatory(fmt.Sprintf("0x%x", start), fmt.Sprintf("0x%x", end))
		path.add(fmt.Sprintf("0x%x", instance), instance == 0)

		switch guid {
		case EfiVirtualDiskGuid:
			path.name = "VirtualDisk"
		case EfiVirtualCdGuid:
			path.name = "VirtualCD"
		case EfiPersistentVirtualDiskGuid:
			path.name = "PersistentVirtualDisk"
		case EfiPersistentVirtualCdGuid:
			path.name = "PersistentVirtualCD"
		default:
			path.add(guid)
		}

		return path.String(), nil
	}

	return "", errUnexpectedData
})

// bbsDevicePath dissects a device path node with type 0x05.
var bbsDevicePath = wrapDP(func(r *reader, subtype uint8) (string, error) {
	switch subtype {
	case 0x01: // BIOS Boot Specification Device Path.
		deviceType, err := r.readE16(map[uint16]string{1: "Floppy", 2: "HD", 3: "CDROM", 4: "PCMCIA", 5: "USB", 6: "Network"}, false)
		if err != nil {
			return "", err
		}

		status, err := r.readU16()
		if err != nil {
			return "", err
		}

		description, err := r.readZn8(r.rem())
		if err != nil {
			return "", err
		}

		path := dpFormatter{name: "BBS"}
		path.addMandatory(deviceType, description)
		path.add(fmt.Sprintf("0x%x", status), status == 0)
		return path.String(), nil
	}

	return "", errUnexpectedData
})

// devicePathNode dissects a device path node.
func devicePathNode(nodeType uint8, subtype uint8, b []byte) (string, error) {
	v, err := func() (string, error) {
		switch nodeType {
		case 0x01: // Hardware Device Path.
			repr, err := hardwareDevicePath(subtype, b)
			if err != nil {
				return fmt.Sprintf("HardwarePath(0x%x,%x)", subtype, b), nil
			}

			return repr, nil
		case 0x02: // ACPI Device Path.
			repr, err := acpiDevicePath(subtype, b)
			if err != nil {
				return fmt.Sprintf("AcpiPath(0x%x,%x)", subtype, b), nil
			}

			return repr, nil
		case 0x03: // Messaging Device Path.
			repr, err := messagingDevicePath(subtype, b)
			if err != nil {
				return fmt.Sprintf("Msg(0x%x,%x)", subtype, b), nil
			}

			return repr, err
		case 0x04: // Media Device Path.
			repr, err := mediaDevicePath(subtype, b)
			if err != nil {
				return fmt.Sprintf("MediaType(0x%x,%x)", subtype, b), nil
			}

			return repr, err
		case 0x05: // BIOS Boot Specification Device Path.
			repr, err := bbsDevicePath(subtype, b)
			if err != nil {
				return fmt.Sprintf("BbsPath(0x%x,%x)", subtype, b), nil
			}

			return repr, err
		}

		return "", errUnexpectedData
	}()
	if err == nil {
		return v, nil
	}

	return fmt.Sprintf("Path(0x%x,0x%x,%x)", nodeType, subtype, b), nil
}

// devicePaths dissects an array of device path structures.
func devicePaths(b []byte) ([][]string, error) {
	var paths [][]string
	var instances, nodes []string
	r := newReader(b)

	for !r.eof() {
		nodeType, err := r.readU8()
		if err != nil {
			return nil, err
		}

		subtype, err := r.readU8()
		if err != nil {
			return nil, err
		}

		n, err := r.readU16()
		if err != nil {
			return nil, err
		}

		if n < 4 {
			return nil, errUnexpectedData
		}

		node, err := r.read(int(n) - 4)
		if err != nil {
			return nil, err
		}

		// If we haven’t reached the End of Hardware Device Path marker, continue processing.
		if nodeType != 0x7f {
			summarized, err := devicePathNode(nodeType, subtype, node)
			if err != nil {
				return nil, err
			}

			nodes = append(nodes, summarized)
			continue
		}

		if n != 4 {
			return nil, errUnexpectedData
		}

		instances = append(instances, strings.Join(nodes, "/"))
		nodes = nil
		switch subtype {
		case 0x01: // End This Instance of a Device Path.
		case 0xff: // End Entire Device Path.
			paths = append(paths, instances)
			instances = nil
		default:
			return nil, errUnexpectedData
		}
	}

	if len(nodes) != 0 || len(instances) != 0 {
		return nil, errUnexpectedData
	}

	return paths, nil
}

// devicePaths dissects a device path structure.
func devicePath(b []byte) ([]string, error) {
	paths, err := devicePaths(b)
	if err != nil {
		return nil, err
	}

	if len(paths) != 1 {
		return nil, errUnexpectedData
	}

	return paths[0], nil
}
