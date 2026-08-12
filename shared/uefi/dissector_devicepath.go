package uefi

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// wrapDP wraps a device path dissector.
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

// hardwareDevicePath dissects a device path node with type 0x01.
var hardwareDevicePath = wrapDP(func(r *reader, subtype uint8) (string, error) {
	path := dpFormatter{}
	var err error
	switch subtype {
	case 0x01: // PCI.
		path.name = "Pci"
		err = path.addDissected(r, "u8", "u8")
		// The arguments are stored swapped in their binary representation.
		slices.Reverse(path.args)
	case 0x02: // PCCARD.
		path.name = "PcCard"
		err = path.addDissected(r, "u8")
	case 0x03: // Memory Mapped.
		path.name = "MemoryMapped"
		err = path.addDissected(r, "u32", "u64", "u64")
	case 0x04: // Vendor.
		path.name = "VenHw"
		err = path.addDissected(r, "guid", "*")
	case 0x05: // Controller.
		path.name = "Ctrl"
		err = path.addDissected(r, "u32")
	case 0x06: // BMC.
		path.name = "BMC"
		err = path.addDissected(r, "u8", "u64")
	default:
		err = errUnexpectedData
	}

	if err != nil {
		return "", err
	}

	return path.String(), nil
})

// acpiDevicePath dissects a device path node with type 0x02.
var acpiDevicePath = wrapDP(func(r *reader, subtype uint8) (string, error) {
	path := dpFormatter{}
	var err error
	switch subtype {
	case 0x01: // ACPI Device Path.
		var hid string
		hid, err = r.readEISA()
		if err != nil {
			break
		}

		// The UEFI specification mandates that for the recognized EISA IDs listed below, the UID is
		// required for display.
		format := "u32"
		switch hid {
		case "PNP0301":
			path.name = "Keyboard"
		case "PNP0401":
			path.name = "ParallelPort"
		case "PNP0501":
			path.name = "Serial"
		case "PNP0604":
			path.name = "Floppy"
		case "PNP0A03":
			path.name = "PciRoot"
		case "PNP0A08":
			path.name = "PcieRoot"
		default:
			path.name = "Acpi"
			path.add(hid)
			// Here, the specification says the UID is optional.
			format = "u32?"
		}

		err = path.addDissected(r, format)
	case 0x02: // Expanded ACPI Device Path.
		var hid, cid, hidStr, uidStr, cidStr string
		var uid uint32
		hid, err = r.readEISA()
		if err != nil {
			break
		}

		uid, err = r.readU32()
		if err != nil {
			break
		}

		cid, err = r.readEISA()
		if err != nil {
			break
		}

		hidStr, err = r.readZn8()
		if err != nil {
			break
		}

		uidStr, err = r.readZn8()
		if err != nil {
			break
		}

		cidStr, err = r.readZn8()
		if err != nil {
			break
		}

		if len(hidStr) == 0 && len(cidStr) == 0 && len(uidStr) > 0 {
			path.name = "AcpiExp"
			path.addMandatory(hid, cid, uidStr)
		} else {
			path.name = "AcpiEx"
			path.addMandatory(hid, cid)
			path.add(fmt.Sprintf("0x%x", uid), uid == 0)
			path.add(hidStr, len(hidStr) == 0)
			path.add(cidStr, len(cidStr) == 0)
			path.add(uidStr, len(uidStr) == 0)
		}

	case 0x03: // _ADR Device Path.
		path.name = "AcpiAdr"
		err = path.addDissected(r, "u32*")
	case 0x04: // NVDIMM Device.
		path.name = "NvdimmAcpiAdr"
		err = path.addDissected(r, "u32")
	default:
		err = errUnexpectedData
	}

	if err != nil {
		return "", err
	}

	return path.String(), nil
})

// sasDevicePath dissects a SAS device path node. The `ex` parameter switches the binary parsing
// logic to big-endian.
func sasDevicePath(r *reader, ex bool, reserved uint32) (string, error) {
	var address, lun uint64
	var err error
	path := dpFormatter{name: "Sas"}
	if ex {
		path.name = "SasEx"
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

	path.add(fmt.Sprintf("0x%x", address))
	topology, err := r.readU16()
	if err != nil {
		return "", err
	}

	moreInfo := false
	moreInfoNext := false
	sasSATA := "NoTopology"
	switch topology & 0x000F {
	case 0x0002:
		moreInfoNext = true
		fallthrough
	case 0x0001:
		moreInfo = true
	case 0x0000:
	default:
		sasSATA = fmt.Sprintf("0x%x", topology)
	}

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

		connect := uint8(topology) >> 6
		var ok bool
		connectStr, ok = map[uint8]string{0: "Direct", 1: "Expanded"}[connect]
		if !ok {
			connectStr = fmt.Sprintf("0x%x", connect)
		}
	}

	rtp, err := r.readU16()
	if err != nil {
		return "", err
	}

	// We need to print the LUN if a RTP is specified in order to disambiguate. Even though the
	// specification does not explicitly say it, it looks like SAS nodes are expected to always show
	// it.
	if lun != 0 || rtp != 0 || sasSATA == "SAS" {
		path.add(fmt.Sprintf("0x%x", lun))
	}

	if rtp != 0 {
		path.add(fmt.Sprintf("0x%x", rtp))
	}

	// We need to print the topology if reserved data are specified in order to disambiguate.
	if sasSATA != "NoTopology" || reserved != 0 {
		path.add(sasSATA)
	}

	if moreInfo {
		if external {
			path.add("External")
		} else {
			path.add("Internal")
		}

		path.add(connectStr)
	}

	if moreInfoNext {
		// We need to print the bay as a plain integer in order to disambiguate, and because of the
		// strange 1-offset.
		path.add(fmt.Sprintf("%d", (topology>>8)+1))
	}

	if reserved != 0 {
		path.add(fmt.Sprintf("0x%x", reserved))
	}

	return path.String(), nil
}

// messagingDevicePath dissects a device path node with type 0x03.
var messagingDevicePath = wrapDP(func(r *reader, subtype uint8) (string, error) {
	path := dpFormatter{}
	var err error
out:
	switch subtype {
	case 0x01: // ATAPI.
		path.name = "Ata"
		var controller, drive string
		controller, err = r.readE8(map[uint8]string{0: "Primary", 1: "Secondary"}, true)
		if err != nil {
			break
		}

		drive, err = r.readE8(map[uint8]string{0: "Master", 1: "Slave"}, true)
		if err != nil {
			break
		}

		path.addMandatory(controller, drive)
		err = path.addDissected(r, "u16")
	case 0x02: // SCSI.
		path.name = "Scsi"
		err = path.addDissected(r, "u16", "u16")
	case 0x03: // Fibre Channel.
		path.name = "Fibre"
		err = path.addDissected(r, "s4", "u64", "u64")
	case 0x04: // 1394.
		path.name = "I1394"
		err = path.addDissected(r, "s4", "u64")
	case 0x05: // USB.
		path.name = "USB"
		err = path.addDissected(r, "u8", "u8")
	case 0x06: // I2O Random Block Storage Class.
		path.name = "I2O"
		err = path.addDissected(r, "u32")
	case 0x09: // InfiniBand.
		path.name = "Infiniband"
		err = path.addDissected(r, "u32", "guid", "u64", "u64", "u64")
	case 0x0a: // Vendor.
		var guid string
		guid, err = r.readGUID()
		if err != nil {
			break
		}

		path.name = map[string]string{EfiPcAnsiGuid: "VenPcAnsi", EfiVT100Guid: "VenVt100", EfiVT100PlusGuid: "VenVt100Plus", EfiVTUTF8Guid: "VenUtf8", EfiUartDevicePathGuid: "UartFlowCtrl", EfiDebugPortProtocolGuid: "DebugPort"}[guid]
		switch guid {
		case EfiPcAnsiGuid, EfiVT100Guid, EfiVT100PlusGuid, EfiVTUTF8Guid, EfiDebugPortProtocolGuid:
			break out
		case EfiUartDevicePathGuid:
			var flow string
			flow, err = r.readE32(map[uint32]string{0: "None", 1: "Hardware", 2: "XonXoff"}, true)
			path.add(flow)
			break out
		case EfiSasDevicePathGuid:
			var reserved uint32
			reserved, err = r.readU32()
			if err != nil {
				break out
			}

			return sasDevicePath(r, false, reserved)
		}

		path.name = "VenMsg"
		path.add(guid)
		err = path.addDissected(r, "*")
	case 0x0b: // MAC Address for a network interface.
		var mac []byte
		var ifType uint8
		mac, err = r.read(32)
		if err != nil {
			break
		}

		ifType, err = r.readU8()
		if err != nil {
			break
		}

		if ifType == 0x00 || ifType == 0x01 {
			mac = mac[:6]
		}

		path.name = "MAC"
		path.add(fmt.Sprintf("%x", mac))
		path.add(fmt.Sprintf("0x%x", ifType), ifType == 0x00)
	case 0x0c: // IPv4.
		var localRaw, remoteRaw, gatewayRaw, maskRaw []byte
		var localPort, remotePort uint16
		var protocol, ipType string
		localRaw, err = r.read(4)
		if err != nil {
			break
		}

		remoteRaw, err = r.read(4)
		if err != nil {
			break
		}

		localPort, err = r.readU16()
		if err != nil {
			break
		}

		remotePort, err = r.readU16()
		if err != nil {
			break
		}

		protocol, err = r.readE16(map[uint16]string{6: "TCP", 17: "UDP"}, false)
		if err != nil {
			break
		}

		ipType, err = r.readE8(map[uint8]string{0: "DHCP", 1: "Static"}, true)
		if err != nil {
			break
		}

		gatewayRaw, err = r.read(4)
		if err != nil {
			break
		}

		maskRaw, err = r.read(4)
		if err != nil {
			break
		}

		localIP := formatIP(localRaw, localPort)
		remoteIP := formatIP(remoteRaw, remotePort)
		gatewayIP := formatIP(gatewayRaw)
		maskIP := formatIP(maskRaw)

		path.name = "IPv4"
		path.add(remoteIP)
		path.add(protocol, protocol == "UDP")
		path.add(ipType, ipType == "DHCP")
		path.add(localIP, localIP == "0.0.0.0")
		path.add(gatewayIP, gatewayIP == "0.0.0.0")
		path.add(maskIP, maskIP == "0.0.0.0")
	case 0x0d: // IPv6.
		var localRaw, remoteRaw, gatewayRaw []byte
		var localPort, remotePort uint16
		var protocol, origin string
		var prefix uint8
		localRaw, err = r.read(16)
		if err != nil {
			break
		}

		remoteRaw, err = r.read(16)
		if err != nil {
			break
		}

		localPort, err = r.readU16()
		if err != nil {
			break
		}

		remotePort, err = r.readU16()
		if err != nil {
			break
		}

		protocol, err = r.readE16(map[uint16]string{6: "TCP", 17: "UDP"}, false)
		if err != nil {
			break
		}

		origin, err = r.readE8(map[uint8]string{0: "Static", 1: "StatelessAutoConfigure", 2: "StatefulAutoConfigure"}, true)
		if err != nil {
			break
		}

		prefix, err = r.readU8()
		if err != nil {
			break
		}

		gatewayRaw, err = r.read(16)
		if err != nil {
			break
		}

		localIP := formatIP6(localRaw, localPort)
		remoteIP := formatIP6(remoteRaw, remotePort)
		gatewayIP := formatIP6(gatewayRaw)

		path.name = "IPv6"
		path.add(remoteIP)
		// The specification states that there is a default value even if the next field doesn’t.
		path.add(protocol, protocol == "UDP")
		path.add(origin)
		path.add(localIP, localIP == "::")
		path.add(gatewayIP, gatewayIP == "::")
		// The specification doesn’t give any hint on how to display the prefix length. We are choosing
		// to display is as an integer defaulting to 64.
		path.add(strconv.Itoa(int(prefix)), prefix == 64)
	case 0x0e: // UART.
		var baudRate uint64
		var dataBits, parity, stopBits uint8
		err = r.skip(4)
		if err != nil {
			break
		}

		baudRate, err = r.readU64()
		if err != nil {
			break
		}

		dataBits, err = r.readU8()
		if err != nil {
			break
		}

		// The UEFI specification requires that both the parity and stop bits be formatted in a similar
		// way. If either can’t be mapped to a keyword, then the other must be displayed as an integer.
		parity, err = r.readU8()
		if err != nil {
			break
		}

		stopBits, err = r.readU8()
		if err != nil {
			break
		}

		path.name = "Uart"
		path.add(fmt.Sprintf("%d", baudRate), baudRate == 115200)
		path.add(fmt.Sprintf("%d", dataBits), dataBits == 8)
		if parity > 5 || stopBits > 3 {
			path.add(fmt.Sprintf("0x%x", parity), parity == 0)
			path.add(fmt.Sprintf("0x%x", stopBits), stopBits == 0)
		} else {
			path.add(map[uint8]string{0: "D", 1: "N", 2: "E", 3: "O", 4: "M", 5: "S"}[parity], parity == 0)
			path.add(map[byte]string{0: "D", 1: "1", 2: "1.5", 3: "2"}[stopBits], stopBits == 0)
		}

	case 0x0f: // USB Class.
		var vid, pid uint16
		var usbClass, usbSubclass, protocol uint8
		vid, err = r.readU16()
		if err != nil {
			break
		}

		pid, err = r.readU16()
		if err != nil {
			break
		}

		usbClass, err = r.readU8()
		if err != nil {
			break
		}

		usbSubclass, err = r.readU8()
		if err != nil {
			break
		}

		protocol, err = r.readU8()
		if err != nil {
			break
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

		path.name = name
		path.add(fmt.Sprintf("0x%x", vid), vid == 0xFFFF)
		path.add(fmt.Sprintf("0x%x", pid), pid == 0xFFFF)
		if !knownClass {
			path.add(fmt.Sprintf("0x%x", usbClass), usbClass == 0xFF)
		}

		if !knownSubclass {
			path.add(fmt.Sprintf("0x%x", usbSubclass), usbSubclass == 0xFF)
		}

		path.add(fmt.Sprintf("0x%x", protocol), protocol == 0xFF)
	case 0x10: // USB WWID.
		var usbInterface, vid, pid uint16
		var sn string
		usbInterface, err = r.readU16()
		if err != nil {
			break
		}

		vid, err = r.readU16()
		if err != nil {
			break
		}

		pid, err = r.readU16()
		if err != nil {
			break
		}

		sn, err = r.readZ16(r.rem() / 2)
		if err != nil {
			break
		}

		path.name = "UsbWwid"
		path.addMandatory(fmt.Sprintf("0x%x", vid), fmt.Sprintf("0x%x", pid), fmt.Sprintf("0x%x", usbInterface), sn)
	case 0x11: // Device Logical unit.
		path.name = "Unit"
		err = path.addDissected(r, "u8")
	case 0x12: // SATA.
		path.name = "Sata"
		// The UEFI specification marks the last parameter (LUN) as mandatory, but we don’t.
		err = path.addDissected(r, "u16", "u16?65535", "u16?")
	case 0x13: // iSCSI.
		var protocol, targetName string
		var options, portalGroup uint16
		var lun uint64
		protocol, err = r.readE16(map[uint16]string{0: "TCP"}, false)
		if err != nil {
			break
		}

		options, err = r.readU16()
		if err != nil {
			break
		}

		headerDigest, ok := map[uint16]string{0x0000: "None", 0x0002: "CRC32C"}[options&0x0003]
		if !ok {
			err = errUnexpectedData
			break
		}

		dataDigest, ok := map[uint16]string{0x0000: "None", 0x0008: "CRC32C"}[options&0x000c]
		if !ok {
			err = errUnexpectedData
			break
		}

		authentication, ok := map[uint16]string{0x0000: "CHAP_BI", 0x0800: "None", 0x1000: "CHAP_UNI"}[options&0x1c00]
		if !ok {
			err = errUnexpectedData
			break
		}

		lun, err = r.readU64BE()
		if err != nil {
			break
		}

		portalGroup, err = r.readU16()
		if err != nil {
			break
		}

		targetName, err = r.readZ16(r.rem() / 2)
		if err != nil {
			break
		}

		path.name = "iSCSI"
		path.addMandatory(targetName, fmt.Sprintf("0x%x", portalGroup), fmt.Sprintf("0x%x", lun))
		path.add(headerDigest, headerDigest == "None")
		path.add(dataDigest, dataDigest == "None")
		path.add(authentication, authentication == "None")
		path.add(protocol, protocol == "TCP")
	case 0x14: // Vlan (802.1q).
		// We display the VLAN as a plain base-10 integer.
		var vlan uint16
		vlan, err = r.readU16()
		if err != nil {
			break
		}

		path.name = "Vlan"
		path.add(fmt.Sprintf("%d", vlan))
	case 0x15: // Fibre Channel Ex.
		path.name = "FibreEx"
		err = path.addDissected(r, "s4", "u64be", "u64be")
	case 0x16: // SAS Ex.
		return sasDevicePath(r, true, 0)
	case 0x17: // NVM Express Namespace.
		path.name = "NVMe"
		err = path.addDissected(r, "u32", "eui64")
	case 0x18: // Universal Resource Identifier (URI) Device Path.
		path.name = "Uri"
		err = path.addDissected(r, "z8?")
	case 0x19: // UFS.
		path.name = "UFS"
		err = path.addDissected(r, "u8", "u8")
	case 0x1a: // SD.
		path.name = "SD"
		err = path.addDissected(r, "u8?")
	case 0x1b: // Bluetooth.
		path.name = "Bluetooth"
		err = path.addDissected(r, "6")
	case 0x1c: // Wi-Fi Device Path.
		// Sane string parsing strategies unfortunately don’t apply to SSIDs, which can contain null
		// bytes.
		var ssid []byte
		ssid, err = r.read(32)
		if err != nil {
			break
		}

		path.name = "Wi-Fi"
		path.add(strings.TrimRight(string(ssid), "\x00"))
	case 0x1d: // eMMC.
		path.name = "eMMC"
		err = path.addDissected(r, "u8?")
	case 0x1e: // BluetoothLE.
		path.name = "BluetoothLE"
		err = path.addDissected(r, "6", "u8")
	case 0x1f: // DNS Device Path.
		var v6 bool
		var ip []byte
		v6, err = r.readB8()
		if err != nil {
			break
		}

		path.name = "Dns"
		for !r.eof() {
			ip, err = r.read(16)
			if err != nil {
				break out
			}

			if v6 {
				path.add(formatIP6(ip))
			} else {
				path.add(formatIP(ip))
			}
		}

	case 0x20: // NVDIMM Namespace.
		path.name = "NVDIMM"
		err = path.addDissected(r, "guid")
	case 0x21: // REST Service Device Path.
		path.name = "RestService"
		var service, access uint8
		service, err = r.readU8()
		if err != nil {
			break
		}

		access, err = r.readU8()
		if err != nil {
			break
		}

		path.addMandatory(fmt.Sprintf("0x%x", service), fmt.Sprintf("0x%x", access))

		if service == 0xFF {
			// The UEFI specification doesn’t mark the last field as optional, but we do.
			err = path.addDissected(r, "guid", "*")
		}

	case 0x22: // NVMe-oF Namespace Device Path.
		var nidt string
		nidt, err = r.readE8(map[uint8]string{1: "eui", 2: "nvme-nguid", 3: "urn:uuid"}, true)
		if err != nil {
			break
		}

		var nid string
		switch nidt {
		case "eui":
			nid, err = r.readEUI64BE()
			if err != nil {
				break out
			}

			err = r.skip(8)
			if err != nil {
				break out
			}

		case "nvme-nguid":
			var b []byte
			b, err = r.read(16)
			if err != nil {
				break out
			}

			nid = fmt.Sprintf("%x-%x-%x", b[0:8], b[8:11], b[11:16])
		case "urn:uuid":
			nid, err = r.readGUIDBE()
			if err != nil {
				break out
			}
		}

		var nqn string
		nqn, err = r.readZn8(r.rem())
		if err != nil {
			break out
		}

		path.name = "NVMEoF"
		path.addMandatory(nqn, nidt+":"+nid)
	default:
		err = errUnexpectedData
	}

	if err != nil {
		return "", err
	}

	return path.String(), nil
})

// mediaDevicePath dissects a device path node with type 0x04.
var mediaDevicePath = wrapDP(func(r *reader, subtype uint8) (string, error) {
	path := dpFormatter{}
	var err error
	switch subtype {
	case 0x01: // Hard Drive.
		var partition uint32
		var start, size uint64
		var signature []byte
		var format, sigType string
		partition, err = r.readU32()
		if err != nil {
			break
		}

		start, err = r.readU64()
		if err != nil {
			break
		}

		size, err = r.readU64()
		if err != nil {
			break
		}

		signature, err = r.read(16)
		if err != nil {
			break
		}

		format, err = r.readE8(map[uint8]string{1: "MBR", 2: "GPT"}, true)
		if err != nil {
			break
		}

		// The UEFI specification does not handle format / signature type mismatches, so we do the same.
		// Additionally, it doesn’t explain how to handle non-0x01 or 0x02 signature types. Even though
		// the 0x00 type is allowed, it is not a valid format; we decide to reject all those
		// under-specified cases.
		sigType, err = r.readE8(map[uint8]string{1: "MBR", 2: "GPT"}, true)
		if err != nil {
			break
		}

		if sigType != format {
			err = errUnexpectedData
			break
		}

		path.name = "HD"
		path.add(fmt.Sprintf("0x%x", partition), partition == 0)
		path.add(format, format == "GPT")
		// The UEFI specification marks this parameter as mandatory, thus making the previous ones being
		// optional moot.
		if sigType == "MBR" {
			path.add(fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(signature[:4])))
		} else {
			path.add(formatGUID(signature))
		}

		if partition != 0 {
			// Most tools format those as integers, so we do the same.
			path.addMandatory(fmt.Sprintf("%d", start), fmt.Sprintf("%d", size))
		}

	case 0x02: // CD-ROM “El Torito” Format.
		var entry uint32
		var start, size uint64
		entry, err = r.readU32()
		if err != nil {
			break
		}

		start, err = r.readU64()
		if err != nil {
			break
		}

		size, err = r.readU64()
		if err != nil {
			break
		}

		path.name = "CDROM"
		// Most tools format sizes as integers, so we do the same.
		path.addMandatory(fmt.Sprintf("0x%x", entry), fmt.Sprintf("%d", start), fmt.Sprintf("%d", size))
	case 0x03: // Vendor.
		path.name = "VenMedia"
		err = path.addDissected(r, "guid", "*")
	case 0x04: // File Path.
		var file string
		file, err = r.readZn16(r.rem() / 2)
		if err != nil {
			break
		}

		return file, nil
	case 0x05: // Media Protocol.
		path.name = "Media"
		err = path.addDissected(r, "guid")
	case 0x06: // PIWG Firmware File.
		path.name = "FvFile"
		err = path.addDissected(r, "guid")
	case 0x07: // PIWG Firmware Volume.
		path.name = "Fv"
		err = path.addDissected(r, "guid")
	case 0x08: // Relative Offset Range.
		var start, end uint64
		err = r.skip(4)
		if err != nil {
			break
		}

		start, err = r.readU64()
		if err != nil {
			break
		}

		end, err = r.readU64()
		if err != nil {
			break
		}

		path.name = "Offset"
		path.addMandatory(fmt.Sprintf("%d", start), fmt.Sprintf("%d", end))
	case 0x09: // RAM Disk Device Path.
		var start, end uint64
		var guid string
		var instance uint16
		start, err = r.readU64()
		if err != nil {
			break
		}

		end, err = r.readU64()
		if err != nil {
			break
		}

		guid, err = r.readGUID()
		if err != nil {
			break
		}

		instance, err = r.readU16()
		if err != nil {
			break
		}

		path.addMandatory(fmt.Sprintf("%d", start), fmt.Sprintf("%d", end))
		path.add(fmt.Sprintf("0x%x", instance), instance == 0)
		var ok bool
		path.name, ok = map[string]string{EfiVirtualDiskGuid: "VirtualDisk", EfiVirtualCdGuid: "VirtualCD", EfiPersistentVirtualDiskGuid: "PersistentVirtualDisk", EfiPersistentVirtualCdGuid: "PersistentVirtualCD"}[guid]

		if !ok {
			path.add(guid)
			path.name = "RamDisk"
		}

	default:
		err = errUnexpectedData
	}

	if err != nil {
		return "", err
	}

	return path.String(), nil
})

// bbsDevicePath dissects a device path node with type 0x05.
var bbsDevicePath = wrapDP(func(r *reader, subtype uint8) (string, error) {
	path := dpFormatter{}
	var err error
	switch subtype {
	case 0x01: // BIOS Boot Specification Device Path.
		var deviceType, description string
		var status uint16
		deviceType, err = r.readE16(map[uint16]string{1: "Floppy", 2: "HD", 3: "CDROM", 4: "PCMCIA", 5: "USB", 6: "Network"}, false)
		if err != nil {
			break
		}

		status, err = r.readU16()
		if err != nil {
			break
		}

		description, err = r.readZn8(r.rem())
		if err != nil {
			break
		}

		path.name = "BBS"
		path.addMandatory(deviceType, description)
		path.add(fmt.Sprintf("0x%x", status), status == 0)
	default:
		err = errUnexpectedData
	}

	if err != nil {
		return "", err
	}

	return path.String(), nil
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
				return fmt.Sprintf("MediaPath(0x%x,%x)", subtype, b), nil
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

// devicePath dissects a device path structure.
// TODO: Implement variable formatting.
var devicePath = dissector{
	dissect: func(b []byte) (any, error) {
		paths, err := devicePaths(b)
		if err != nil {
			return nil, err
		}

		if len(paths) != 1 {
			return nil, errUnexpectedData
		}

		return paths[0], nil
	},
	format: func(json.RawMessage) ([]byte, error) {
		return nil, errNotImplemented
	},
}
