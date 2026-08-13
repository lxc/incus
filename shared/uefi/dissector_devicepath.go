package uefi

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

type dpDissector struct {
	dissect func(uint8, []byte) (string, error)
	format  func(string, ...string) (uint8, []byte, error)
}

// wrap wraps a variable dissector.
func wrapDP(f func(*reader, uint8) (string, error), g func(*writer, string, ...string) (uint8, error)) dpDissector {
	return dpDissector{
		dissect: func(subtype uint8, b []byte) (string, error) {
			r := newReader(b)
			v, err := f(r, subtype)
			if err != nil {
				return "", err
			}

			if !r.eof() {
				return "", errUnexpectedData
			}

			return v, nil
		},
		format: func(name string, args ...string) (uint8, []byte, error) {
			w := newWriter()
			subType, err := g(w, name, args...)
			if err != nil {
				return 0, nil, err
			}

			return subType, w.data, nil
		},
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
}, func(w *writer, name string, args ...string) (uint8, error) {
	switch name {
	case "pci":
		err := expectArgs(args, 2)
		if err != nil {
			return 0, err
		}

		// Some argument reordering is needed.
		return 0x01, formatDPArgs(w, []string{args[1], args[0]}, "u8", "u8")
	case "pccard":
		return 0x02, formatDPArgs(w, args, "u8")
	case "memorymapped":
		return 0x03, formatDPArgs(w, args, "u32", "u64", "u64")
	case "venhw":
		return 0x04, formatDPArgs(w, args, "guid", "*")
	case "ctrl":
		return 0x05, formatDPArgs(w, args, "u32")
	case "bmc":
		return 0x06, formatDPArgs(w, args, "u8", "u64")
	case "hardwarepath":
		return processRawDPArgs(w, args)
	}

	// This is unreachable.
	return 0, errUnexpectedData
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
}, func(w *writer, name string, args ...string) (uint8, error) {
	switch name {
	case "acpi":
		return 0x01, formatDPArgs(w, args, "eisa", "u32?")
	case "keyboard", "parallelport", "serial", "floppy", "pciroot", "pcieroot":
		err := w.writeEISA(map[string]string{"keyboard": "PNP0301", "parallelport": "PNP0401", "serial": "PNP0501", "floppy": "PNP0604", "pciroot": "PNP0A03", "pcieroot": "PNP0A08"}[name])
		if err != nil {
			return 0, err
		}

		return 0x01, formatDPArgs(w, args, "u32?")
	case "acpiexp":
		err := expectArgs(args, 3)
		if err != nil {
			return 0, err
		}

		// Some argument reordering is needed.
		return 0x02, formatDPArgs(w, []string{args[0], "0", args[1], "", args[2], ""}, "eisa", "u32", "eisa", "zn8", "zn8", "zn8")
	case "acpiex":
		err := expectArgsRange(args, 2, 6)
		if err != nil {
			return 0, err
		}

		reordered := []string{args[0], "0", args[1], "", "", ""}
		switch len(args) {
		case 6:
			reordered[4] = args[5]
			fallthrough
		case 5:
			reordered[5] = args[4]
			fallthrough
		case 4:
			reordered[3] = args[3]
			fallthrough
		case 3:
			reordered[1] = args[2]
		}

		// Some argument reordering is needed.
		return 0x02, formatDPArgs(w, reordered, "eisa", "u32", "eisa", "zn8", "zn8", "zn8")
	case "acpiadr":
		return 0x03, formatDPArgs(w, args, "u32*")
	case "nvdimmacpiadr":
		return 0x04, formatDPArgs(w, args, "u32")
	case "acpipath":
		return processRawDPArgs(w, args)
	}

	// This is unreachable.
	return 0, errUnexpectedData
})

// sasDevicePathDissect dissects a SAS device path node. The `ex` parameter switches the binary
// parsing logic to big-endian.
func sasDevicePathDissect(r *reader, ex bool, reserved uint32) (string, error) {
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

// sasDevicePathFormat formats a SAS device path node. The `ex` parameter switches the binary
// dumping logic to big-endian.
func sasDevicePathFormat(ex bool, args ...string) (uint32, []byte, error) {
	err := expectArgsRange(args, 1)
	if err != nil {
		return 0, nil, err
	}

	w := newWriter()
	var reserved uint32
	reordered := []string{args[0], "0", "0", "0"}
	skipped := 0

	if len(args) >= 2 {
		// If the second argument cannot be parsed as an integer, we consider that both LUN and RTP
		// arguments are skipped.
		_, err := strconv.ParseUint(args[1], 0, 64)
		if err == nil {
			reordered[1] = args[1]
		} else {
			skipped = 2
		}
	}

	if len(args) >= 3 && skipped == 0 {
		// If the third argument cannot be parsed as an integer, we consider that the RTP argument is
		// skipped.
		_, err := strconv.ParseUint(args[2], 0, 16)
		if err == nil {
			reordered[3] = args[2]
		} else {
			skipped = 1
		}
	}

	if ex && len(args)+skipped > 7 {
		return 0, nil, tooManyArgs(args[7-skipped:])
	}

	if len(args)+skipped > 8 {
		return 0, nil, tooManyArgs(args[8-skipped:])
	}

	sasSATA := "0"
	if len(args)+skipped >= 4 {
		sasSATA = strings.ToLower(args[3-skipped])
		if sasSATA == "notopology" {
			sasSATA = "0"
		}
	}

	if len(args)+skipped == 4 {
		// According to the UEFI specification, this case can either be a 4-argument Sas(Ex) or a
		// 3-argument Sas with reserved bytes. We forbid the latter as it cannot be parsed
		// unambiguously.
		if slices.Contains([]string{"sas", "sata"}, sasSATA) {
			return 0, nil, errors.New("Missing topology information")
		}

		reordered[2] = sasSATA
	}

	if len(args)+skipped == 5 {
		// This case can only be a 4-argument Sas with reserved bytes. We provide specific error
		// messages for SasEx nodes.
		if ex {
			if slices.Contains([]string{"sas", "sata"}, sasSATA) {
				return 0, nil, errors.New("Missing connect argument")
			}

			return 0, nil, tooManyArgs(args[4-skipped:])
		}

		if slices.Contains([]string{"sas", "sata"}, sasSATA) {
			return 0, nil, errors.New("Missing topology information")
		}

		reordered[2] = sasSATA
		v, err := strconv.ParseUint(args[4-skipped], 0, 32)
		if err != nil {
			return 0, nil, fmt.Errorf("Couldn’t parse reserved bytes %s: %w", args[4-skipped], err)
		}

		reserved = uint32(v)
	}

	if len(args)+skipped >= 6 {
		if !slices.Contains([]string{"sas", "sata"}, sasSATA) {
			return 0, nil, errors.New("Unexpected topology information")
		}

		topology := uint16(0x0001)
		if sasSATA == "sata" {
			topology |= 0x0010
		}

		location, ok := map[string]uint64{"internal": 0, "external": 1}[strings.ToLower(args[4-skipped])]
		if !ok {
			var err error
			location, err = strconv.ParseUint(args[4-skipped], 0, 64)
			if err != nil || location > 1 {
				return 0, nil, fmt.Errorf("Couldn’t parse topology location %s: Unknown value", args[4-skipped])
			}
		}

		if location == 1 {
			topology |= 0x0020
		}

		connect, ok := map[string]uint64{"direct": 0, "expanded": 1}[strings.ToLower(args[5-skipped])]
		if !ok {
			var err error
			connect, err = strconv.ParseUint(args[5-skipped], 0, 64)
			if err != nil || connect > 3 {
				return 0, nil, fmt.Errorf("Couldn’t parse topology connect %s: Unknown value", args[5-skipped])
			}
		}

		topology |= uint16(connect << 6)

		// We now have to disambiguate whether the next argument, if it exists, is a drive bay or
		// reserved bytes. For the sake of sanity, we don’t allow specifying the drive bay parameter as
		// anything other a plain integer. The reason is that the `0xXX` syntax suggests some kind of
		// proximity with the actual stored byte; however, bays are 1-indexed, leading to a rather
		// confusing ambiguity.
		if len(args)+skipped >= 7 {
			// We need to parse a uint16 so that 256 can fit.
			v, err := strconv.ParseUint(args[6-skipped], 10, 16)
			if err == nil && v > 0 && v <= 256 {
				topology |= uint16(v-1) << 8
				// We also need to swap bits 1 and 2 to indicate that this new byte must be parsed.
				topology ^= 0x0003
			} else if ex || len(args)+skipped == 8 {
				if err != nil {
					return 0, nil, fmt.Errorf("Couldn’t parse drive bay %s: %w", args[6-skipped], err)
				}

				return 0, nil, fmt.Errorf("Couldn’t parse drive bay %s: Expected 1 ≤ n ≤ 256", args[6-skipped])
			} else {
				skipped = skipped + 1
			}
		}

		reordered[2] = fmt.Sprintf("%d", topology)
		if len(args)+skipped == 8 {
			// This case can only be a Sas node with reserved bytes. We provide a specific error message
			// for SasEx nodes.
			if ex {
				return 0, nil, fmt.Errorf("Unexpected reserved bytes %s", args[7-skipped])
			}

			v, err := strconv.ParseUint(args[7-skipped], 0, 32)
			if err != nil {
				return 0, nil, fmt.Errorf("Couldn’t parse reserved bytes %s: %w", args[6-skipped], err)
			}

			reserved = uint32(v)
		}
	}

	if ex {
		err = formatDPArgs(w, reordered, "u64be", "u64be", "u16", "u16")
	} else {
		err = formatDPArgs(w, reordered, "u64", "u64", "u16", "u16")
	}

	return reserved, w.data, err
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

			return sasDevicePathDissect(r, false, reserved)
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
		return sasDevicePathDissect(r, true, 0)
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
}, func(w *writer, name string, args ...string) (uint8, error) {
	switch name {
	case "ata":
		err := expectArgs(args, 3)
		if err != nil {
			return 0, err
		}

		controller, ok := map[string]string{"primary": "0", "secondary": "1"}[strings.ToLower(args[0])]
		if !ok {
			controller = args[0]
		}

		drive, ok := map[string]string{"master": "0", "slave": "1"}[strings.ToLower(args[1])]
		if !ok {
			drive = args[1]
		}

		return 0x01, formatDPArgs(w, []string{controller, drive, args[2]}, "u8", "u8", "u16")
	case "scsi":
		return 0x02, formatDPArgs(w, args, "u16", "u16")
	case "fibre":
		return 0x03, formatDPArgs(w, args, "s4", "u64", "u64")
	case "i1394":
		return 0x04, formatDPArgs(w, args, "s4", "u64")
	case "usb":
		return 0x05, formatDPArgs(w, args, "u8", "u8")
	case "i2o":
		return 0x06, formatDPArgs(w, args, "u32")
	case "infiniband":
		return 0x09, formatDPArgs(w, args, "u32", "guid", "u64", "u64", "u64")
	case "venmsg":
		return 0x0a, formatDPArgs(w, args, "guid", "*")
	case "venpcansi", "venvt100", "venvt100plus", "venutf8", "debugport":
		guid := map[string]string{"venpcansi": EfiPcAnsiGuid, "venvt100": EfiVT100Guid, "venvt100plus": EfiVT100PlusGuid, "venutf8": EfiVTUTF8Guid, "debugport": EfiDebugPortProtocolGuid}[name]
		return 0x0a, formatDPArgs(w, []string{guid}, "guid")
	case "uartflowctrl":
		err := expectArgs(args, 1)
		if err != nil {
			return 0, err
		}

		flow, ok := map[string]string{"none": "0", "hardware": "1", "xonxoff": "2"}[strings.ToLower(args[0])]
		if !ok {
			flow = args[0]
		}

		return 0x0a, formatDPArgs(w, []string{EfiUartDevicePathGuid, flow}, "guid", "u32")
	case "sas":
		reserved, b, err := sasDevicePathFormat(false, args...)
		if err != nil {
			return 0, err
		}

		err = w.writeGUID(EfiSasDevicePathGuid)
		if err != nil {
			return 0, err
		}

		err = w.writeU32(reserved)
		if err != nil {
			return 0, err
		}

		return 0x0a, w.write(b)
	case "mac":
		err := expectArgsRange(args, 1, 2)
		if err != nil {
			return 0, err
		}

		v, err := hex.DecodeString(args[0])
		if err != nil {
			return 0, fmt.Errorf("Couldn’t parse address %s: %w", args[0], err)
		}

		mac := make([]byte, 32)
		copy(mac, v)
		ifType := uint64(0)
		if len(args) == 2 {
			ifType, err = strconv.ParseUint(args[1], 0, 8)
			if err != nil {
				return 0, fmt.Errorf("Couldn’t parse interface type %s: %w", args[1], err)
			}
		}

		return 0x0b, formatDPArgs(w, []string{fmt.Sprintf("%x", mac), fmt.Sprintf("%d", ifType)}, "*", "u8")
	case "ipv4":
		err := expectArgsRange(args, 1, 6)
		if err != nil {
			return 0, err
		}

		remoteRaw, remotePort, err := parseIP(args[0])
		if err != nil {
			return 0, fmt.Errorf("Couldn’t parse remote IP %s: %w", args[0], err)
		}

		reordered := []string{"00000000", fmt.Sprintf("%x", remoteRaw), "0", fmt.Sprintf("%d", remotePort), "17", "0", "00000000", "00000000"}
		var ok bool
		if len(args) >= 2 {
			reordered[4], ok = map[string]string{"tcp": "6", "udp": "17"}[strings.ToLower(args[1])]
			if !ok {
				reordered[4] = args[1]
			}
		}

		if len(args) >= 3 {
			reordered[5], ok = map[string]string{"dhcp": "0", "static": "1"}[strings.ToLower(args[2])]
			if !ok {
				reordered[5] = args[2]
			}

			if !slices.Contains([]string{"0", "1"}, reordered[5]) {
				return 0, fmt.Errorf("Unknown IP type %s", args[2])
			}
		}

		if len(args) >= 4 {
			localRaw, localPort, err := parseIP(args[3])
			if err != nil {
				return 0, fmt.Errorf("Couldn’t parse local IP %s: %w", args[3], err)
			}

			reordered[0] = fmt.Sprintf("%x", localRaw)
			reordered[2] = fmt.Sprintf("%d", localPort)
		}

		if len(args) >= 5 {
			gatewayRaw, _, err := parseIP(args[4], false)
			if err != nil {
				return 0, fmt.Errorf("Couldn’t parse gateway IP %s: %w", args[4], err)
			}

			reordered[6] = fmt.Sprintf("%x", gatewayRaw)
		}

		if len(args) == 6 {
			maskRaw, _, err := parseIP(args[5], false)
			if err != nil {
				return 0, fmt.Errorf("Couldn’t parse subnet mask %s: %w", args[5], err)
			}

			reordered[7] = fmt.Sprintf("%x", maskRaw)
		}

		return 0x0c, formatDPArgs(w, reordered, "*", "*", "u16", "u16", "u16", "u8", "*", "*")
	case "ipv6":
		err := expectArgsRange(args, 3, 6)
		if err != nil {
			return 0, err
		}

		remoteRaw, remotePort, err := parseIP6(args[0])
		if err != nil {
			return 0, fmt.Errorf("Couldn’t parse remote IP %s: %w", args[0], err)
		}

		protocol, ok := map[string]string{"tcp": "6", "udp": "17"}[strings.ToLower(args[1])]
		if !ok {
			protocol = args[1]
		}

		origin, ok := map[string]string{"static": "0", "statelessautoconfigure": "1", "statefulautoconfigure": "2"}[strings.ToLower(args[2])]
		if !ok {
			origin = args[2]
		}

		if !slices.Contains([]string{"0", "1", "2"}, origin) {
			return 0, fmt.Errorf("Unknown IP origin %s", args[2])
		}

		reordered := []string{"00000000000000000000000000000000", fmt.Sprintf("%x", remoteRaw), "0", fmt.Sprintf("%d", remotePort), protocol, origin, "64", "00000000000000000000000000000000"}
		if len(args) >= 4 {
			localRaw, localPort, err := parseIP6(args[3])
			if err != nil {
				return 0, fmt.Errorf("Couldn’t parse local IP %s: %w", args[3], err)
			}

			reordered[0] = fmt.Sprintf("%x", localRaw)
			reordered[2] = fmt.Sprintf("%d", localPort)
		}

		if len(args) >= 5 {
			gatewayRaw, _, err := parseIP6(args[4], false)
			if err != nil {
				return 0, fmt.Errorf("Couldn’t parse gateway IP %s: %w", args[4], err)
			}

			reordered[7] = fmt.Sprintf("%x", gatewayRaw)
		}

		if len(args) == 6 {
			reordered[6] = args[5]
		}

		return 0x0d, formatDPArgs(w, reordered, "*", "*", "u16", "u16", "u16", "u8", "u8", "*")
	case "uart":
		err := expectArgsRange(args, 0, 4)
		if err != nil {
			return 0, err
		}

		reordered := []string{"115200", "8", "0", "0"}
		if len(args) >= 1 {
			reordered[0] = args[0]
		}

		if len(args) >= 2 {
			reordered[1] = args[1]
		}

		var parityOK bool
		if len(args) >= 3 {
			reordered[2], parityOK = map[string]string{"d": "0", "n": "1", "e": "2", "o": "3", "m": "4", "s": "5"}[strings.ToLower(args[2])]
			if !parityOK {
				reordered[2] = args[2]
			}
		}

		if len(args) == 4 {
			// The specification tells us that if the parity has been given with a keyword, then so must
			// the stop bits be. This is a way to disambiguate the value.
			if parityOK {
				var ok bool
				reordered[3], ok = map[string]string{"d": "0", "1": "1", "1.5": "2", "2": "3"}[strings.ToLower(args[3])]
				if !ok {
					return 0, fmt.Errorf("Couldn’t parse stop bits %s: Unknown value", args[3])
				}
			} else {
				reordered[3] = args[3]
			}
		}

		return 0x0e, formatDPArgs(w, reordered, "s4", "u64", "u8", "u8", "u8")
	case "usbclass":
		return 0x0f, formatDPArgs(w, args, "u16?65535", "u16?65535", "u8?255", "u8?255", "u8?255")
	case "usbaudio", "usbcdccontrol", "usbhid", "usbimage", "usbprinter", "usbmassstorage", "usbhub", "usbcdcdata", "usbsmartcard", "usbvideo", "usbdiagnostic", "usbwireless":
		usbClass := map[string]string{"usbaudio": "1", "usbcdccontrol": "2", "usbhid": "3", "usbimage": "6", "usbprinter": "7", "usbmassstorage": "8", "usbhub": "9", "usbcdcdata": "10", "usbsmartcard": "11", "usbvideo": "14", "usbdiagnostic": "220", "usbwireless": "224"}[name]
		reordered := []string{"65535", "65535", usbClass, "255", "255"}
		switch len(args) {
		case 4:
			reordered[4] = args[3]
			fallthrough
		case 3:
			reordered[3] = args[2]
			fallthrough
		case 2:
			reordered[1] = args[1]
			fallthrough
		case 1:
			reordered[0] = args[0]
		case 0:
		default:
			return 0, expectArgsRange(args, 0, 4)
		}

		return 0x0f, formatDPArgs(w, reordered, "u16", "u16", "u8", "u8", "u8")
	case "usbdevicefirmwareupdate", "usbirdabridge", "usbtestandmeasurement":
		usbSubclass := map[string]string{"usbdevicefirmwareupdate": "1", "usbirdabridge": "2", "usbtestandmeasurement": "3"}[name]
		reordered := []string{"65535", "65535", "254", usbSubclass, "255"}
		switch len(args) {
		case 3:
			reordered[4] = args[2]
			fallthrough
		case 2:
			reordered[1] = args[1]
			fallthrough
		case 1:
			reordered[0] = args[0]
		case 0:
		default:
			return 0, expectArgsRange(args, 0, 3)
		}

		return 0x0f, formatDPArgs(w, reordered, "u16", "u16", "u8", "u8", "u8")
	case "usbwwid":
		err := expectArgs(args, 4)
		if err != nil {
			return 0, err
		}

		reordered := []string{args[2], args[0], args[1], args[3]}
		return 0x10, formatDPArgs(w, reordered, "u16", "u16", "u16", "z16")
	case "unit":
		return 0x11, formatDPArgs(w, args, "u8")
	case "sata":
		return 0x12, formatDPArgs(w, args, "u16", "u16?65535", "u16?")
	case "iscsi":
		err := expectArgsRange(args, 3, 7)
		if err != nil {
			return 0, err
		}

		reordered := []string{"0", "", args[2], args[1], args[0]}
		var options uint16
		if len(args) >= 4 {
			headerDigest, ok := map[string]uint16{"none": 0x0000, "crc32c": 0x0002}[strings.ToLower(args[3])]
			if !ok {
				return 0, fmt.Errorf("Couldn’t parse header digest type %s: Unknown value", args[3])
			}

			options |= headerDigest
		}

		if len(args) >= 5 {
			dataDigest, ok := map[string]uint16{"none": 0x0000, "crc32c": 0x0008}[strings.ToLower(args[4])]
			if !ok {
				return 0, fmt.Errorf("Couldn’t parse data digest type %s: Unknown value", args[4])
			}

			options |= dataDigest
		}

		if len(args) >= 6 {
			authentication, ok := map[string]uint16{"chap_bi": 0x0000, "none": 0x0800, "chap_uni": 0x1000}[strings.ToLower(args[5])]
			if !ok {
				return 0, fmt.Errorf("Couldn’t parse authentication type %s: Unknown value", args[5])
			}

			options |= authentication
		} else {
			options |= 0x0800
		}

		reordered[1] = fmt.Sprintf("%d", options)

		if len(args) == 7 {
			protocol := args[6]
			if strings.ToLower(protocol) == "tcp" {
				protocol = "0"
			}

			reordered[0] = protocol
		}

		return 0x13, formatDPArgs(w, reordered, "u16", "u16", "u64be", "u16", "z16")
	case "vlan":
		return 0x14, formatDPArgs(w, args, "u16")
	case "fibreex":
		return 0x15, formatDPArgs(w, args, "s4", "u64be", "u64be")
	case "sasex":
		_, b, err := sasDevicePathFormat(true, args...)
		if err != nil {
			return 0, err
		}

		return 0x16, w.write(b)
	case "nvme":
		return 0x17, formatDPArgs(w, args, "u32", "eui64")
	case "uri":
		return 0x18, formatDPArgs(w, args, "z8?")
	case "ufs":
		return 0x19, formatDPArgs(w, args, "u8", "u8")
	case "sd":
		return 0x1a, formatDPArgs(w, args, "u8?")
	case "bluetooth":
		return 0x1b, formatDPArgs(w, args, "6")
	case "wi-fi":
		err := expectArgs(args, 1)
		if err != nil {
			return 0, err
		}

		ssid := []byte(args[0])
		if len(ssid) > 32 {
			return 0, fmt.Errorf("Couldn’t parse SSID %s: Expected at most 32 bytes, got %d", ssid, len(ssid))
		}

		data := make([]byte, 32)
		copy(data, ssid)
		return 0x1c, w.write(data)
	case "emmc":
		return 0x1d, formatDPArgs(w, args, "u8?")
	case "bluetoothle":
		return 0x1e, formatDPArgs(w, args, "6", "u8")
	case "dns":
		// The standard doesn’t explicitly allow mixing IPv4 and IPv6 addresses, but it doesn’t cost
		// much to handle.
		v6 := false
		for _, ip := range args {
			addr, err := netip.ParseAddr(ip)
			if err != nil {
				return 0, fmt.Errorf("Couldn’t parse IP address %s: %w", ip, err)
			}

			if addr.Is6() {
				v6 = true
			}
		}

		err := w.writeB8(v6)
		if err != nil {
			return 0, err
		}

		for _, ip := range args {
			var b []byte
			if v6 {
				b, _, err = parseIP6(ip, false)
			} else {
				b, _, err = parseIP(ip, false)
			}

			if err != nil {
				return 0, err
			}

			data := make([]byte, 16)
			copy(data, b)
			err = w.write(data)
			if err != nil {
				return 0, err
			}
		}

		return 0x1f, nil
	case "nvdimm":
		return 0x20, formatDPArgs(w, args, "guid")
	case "restservice":
		err := expectArgsRange(args, 2, 4)
		if err != nil {
			return 0, err
		}

		service, err := strconv.ParseUint(args[0], 0, 8)
		if err != nil {
			return 0, fmt.Errorf("Couldn’t parse service type %s: %w", args[0], err)
		}

		if service == 0xFF {
			err = expectArgsRange(args, 3, 4)
			if err != nil {
				return 0, err
			}

			return 0x21, formatDPArgs(w, args, "u8", "u8", "guid", "*")
		}

		err = expectArgs(args, 2)
		if err != nil {
			return 0, err
		}

		return 0x21, formatDPArgs(w, args, "u8", "u8")
	case "nvmeof":
		err := expectArgs(args, 2)
		if err != nil {
			return 0, err
		}

		nid := args[1]
		var nidt string
		var ok bool
		types := []string{"u8"}
		nid, ok = strings.CutPrefix(nid, "eui:")
		if ok {
			nidt = "1"
			types = append(types, "eui64be", "s8")
		} else {
			nid, ok = strings.CutPrefix(nid, "nvme-nguid:")
			if ok {
				nidt = "2"
				nid = strings.ReplaceAll(nid, "-", "")
				types = append(types, "16")
			} else {
				nid, ok = strings.CutPrefix(nid, "urn:uuid:")
				if !ok {
					return 0, fmt.Errorf("Couldn’t parse namespace identifier %s: Unknown identifier type", nid)
				}

				nidt = "3"
				types = append(types, "guidbe")
			}
		}

		return 0x22, formatDPArgs(w, []string{nidt, nid, args[0]}, append(types, "zn8")...)
	}

	// This is unreachable.
	return 0, errUnexpectedData
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
}, func(w *writer, name string, args ...string) (uint8, error) {
	switch name {
	case "hd":
		err := expectArgs(args, 3, 5)
		if err != nil {
			return 0, err
		}

		partition, err := strconv.ParseUint(args[0], 0, 8)
		if err != nil {
			return 0, fmt.Errorf("Couldn’t parse partition number %s: %w", args[0], err)
		}

		format, ok := map[string]uint64{"mbr": 1, "gpt": 2}[strings.ToLower(args[1])]
		if !ok {
			// Because the specification is incomplete, we reject anything other than 0x01 and 0x02.
			format, err := strconv.ParseUint(args[1], 0, 8)
			if err != nil {
				return 0, fmt.Errorf("Couldn’t parse partition format %s: %w", args[1], err)
			}

			if format != 0x01 && format != 0x02 {
				return 0, fmt.Errorf("Couldn’t parse partition format %s: Unknown value", args[1])
			}
		}

		types := []string{"u32", "u64", "u64"}
		if format == 0x01 {
			if partition > 4 {
				return 0, fmt.Errorf("Couldn’t parse partition number %s: Expected n ≤ 4", args[0])
			}

			types = append(types, "u32", "s12")
		} else {
			types = append(types, "guid")
		}

		reordered := []string{args[0], "0", "0", args[2], fmt.Sprintf("%d", format), fmt.Sprintf("%d", format)}
		if partition == 0 {
			err = expectArgs(args, 3)
			if err != nil {
				return 0, err
			}
		} else {
			err = expectArgs(args, 5)
			if err != nil {
				return 0, nil
			}

			reordered[1] = args[3]
			reordered[2] = args[4]
		}

		return 0x01, formatDPArgs(w, reordered, append(types, "u8", "u8")...)
	case "cdrom":
		return 0x02, formatDPArgs(w, args, "u32", "u64", "u64")
	case "venmedia":
		return 0x03, formatDPArgs(w, args, "guid", "*")
	case "":
		return 0x04, formatDPArgs(w, args, "zn16")
	case "media", "fvfile", "fv":
		return map[string]uint8{"media": 0x05, "fvfile": 0x06, "fv": 0x07}[name], formatDPArgs(w, args, "guid")
	case "offset":
		return 0x08, formatDPArgs(w, args, "s4", "u64", "u64")
	case "virtualdisk", "virtualcd", "persistentvirtualdisk", "persistentvirtualcd":
		err := expectArgsRange(args, 2, 3)
		if err != nil {
			return 0, err
		}

		guid := map[string]string{"virtualdisk": EfiVirtualDiskGuid, "virtualcd": EfiVirtualCdGuid, "persistentvirtualdisk": EfiPersistentVirtualDiskGuid, "persistentvirtualcd": EfiPersistentVirtualCdGuid}[name]
		reordered := []string{args[0], args[1], guid}
		if len(args) == 3 {
			reordered = append(reordered, args[2])
		}

		return 0x09, formatDPArgs(w, reordered, "u64", "u64", "guid", "u16?")
	case "ramdisk":
		err := expectArgs(args, 4)
		if err != nil {
			return 0, err
		}

		return 0x09, formatDPArgs(w, []string{args[0], args[1], args[3], args[2]}, "u64", "u64", "guid", "u16")
	}

	// This is unreachable.
	return 0, errUnexpectedData
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
}, func(w *writer, name string, args ...string) (uint8, error) {
	switch name {
	case "bbs":
		err := expectArgsRange(args, 2, 3)
		if err != nil {
			return 0, err
		}

		deviceType, ok := map[string]string{"floppy": "1", "hd": "2", "cdrom": "3", "pcmcia": "4", "usb": "5", "network": "6"}[strings.ToLower(args[0])]
		if !ok {
			deviceType = args[0]
		}

		reordered := []string{deviceType, "0", args[1]}
		if len(args) == 3 {
			reordered[1] = args[2]
		}

		// Some argument reordering is needed.
		return 0x01, formatDPArgs(w, reordered, "u16", "u16", "zn8")
	case "bbspath":
		return processRawDPArgs(w, args)
	}

	// This is unreachable.
	return 0, errUnexpectedData
})

// devicePathNodeDissect dissects a device path node.
func devicePathNodeDissect(nodeType uint8, subtype uint8, b []byte) (string, error) {
	v, err := func() (string, error) {
		switch nodeType {
		case 0x01: // Hardware Device Path.
			repr, err := hardwareDevicePath.dissect(subtype, b)
			if err != nil {
				return fmt.Sprintf("HardwarePath(0x%x,%x)", subtype, b), nil
			}

			return repr, nil
		case 0x02: // ACPI Device Path.
			repr, err := acpiDevicePath.dissect(subtype, b)
			if err != nil {
				return fmt.Sprintf("AcpiPath(0x%x,%x)", subtype, b), nil
			}

			return repr, nil
		case 0x03: // Messaging Device Path.
			repr, err := messagingDevicePath.dissect(subtype, b)
			if err != nil {
				return fmt.Sprintf("Msg(0x%x,%x)", subtype, b), nil
			}

			return repr, err
		case 0x04: // Media Device Path.
			repr, err := mediaDevicePath.dissect(subtype, b)
			if err != nil {
				return fmt.Sprintf("MediaPath(0x%x,%x)", subtype, b), nil
			}

			return repr, err
		case 0x05: // BIOS Boot Specification Device Path.
			repr, err := bbsDevicePath.dissect(subtype, b)
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

// devicePathNodeFormat formats a device path node.
func devicePathNodeFormat(name string, args ...string) ([]byte, error) {
	var nodeType, subType uint8
	var b []byte
	var err error
	switch name {
	case "pci", "pccard", "memorymapped", "venhw", "ctrl", "bmc", "hardwarepath":
		nodeType = 0x01
		subType, b, err = hardwareDevicePath.format(name, args...)
	case "acpi", "keyboard", "parallelport", "serial", "floppy", "pciroot", "pcieroot", "acpiexp", "acpiex", "acpiadr", "nvdimmacpiadr", "acpipath":
		nodeType = 0x02
		subType, b, err = acpiDevicePath.format(name, args...)
	case "ata", "scsi", "fibre", "i1394", "usb", "i2o", "infiniband", "venmsg", "venpcansi", "venvt100", "venvt100plus", "venutf8", "uartflowctrl", "sas", "debugport", "mac", "ipv4", "ipv6", "uart", "usbclass", "usbaudio", "usbcdccontrol", "usbhid", "usbimage", "usbprinter", "usbmassstorage", "usbhub", "usbcdcdata", "usbsmartcard", "usbvideo", "usbdiagnostic", "usbwireless", "usbdevicefirmwareupdate", "usbirdabridge", "usbtestandmeasurement", "usbwwid", "unit", "sata", "iscsi", "vlan", "fibreex", "sasex", "nvme", "uri", "ufs", "sd", "bluetooth", "wi-fi", "emmc", "bluetoothle", "dns", "nvdimm", "restservice", "nvmeof":
		nodeType = 0x03
		subType, b, err = messagingDevicePath.format(name, args...)
	case "hd", "cdrom", "venmedia", "", "media", "fvfile", "fv", "offset", "ramdisk", "virtualdisk", "virtualcd", "persistentvirtualdisk", "persistentvirtualcd":
		nodeType = 0x04
		subType, b, err = mediaDevicePath.format(name, args...)
	case "bbs", "bbspath":
		nodeType = 0x05
		subType, b, err = bbsDevicePath.format(name, args...)
	default:
		err = fmt.Errorf("Unknown node %s", name)
	}

	if err != nil {
		return nil, fmt.Errorf("Failed to parse node %s with arguments %v: %w", strings.ToUpper(name), args, err)
	}

	w := newWriter()
	err = w.writeU8(nodeType)
	if err != nil {
		return nil, err
	}

	err = w.writeU8(subType)
	if err != nil {
		return nil, err
	}

	err = w.writeU16(uint16(4 + len(b)))
	if err != nil {
		return nil, err
	}

	err = w.write(b)
	if err != nil {
		return nil, err
	}

	return w.data, nil
}

// devicePathsDissect dissects an array of device path structures.
func devicePathsDissect(r *reader) ([][]string, error) {
	var paths [][]string
	var instances, nodes []string

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
			summarized, err := devicePathNodeDissect(nodeType, subtype, node)
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

// devicePathsFormat formats an array of device path structures.
func devicePathsFormat(w *writer, paths [][]string) error {
	for _, path := range paths {
		for i, instance := range path {
			nodes, err := decomposeDPInstance(instance)
			if err != nil {
				return fmt.Errorf("Failed to parse device path instance %s: %w", instance, err)
			}

			for _, node := range nodes {
				b, err := devicePathNodeFormat(node[0], node[1:]...)
				if err != nil {
					return fmt.Errorf("Failed to parse device path instance %s: %w", instance, err)
				}

				err = w.write(b)
				if err != nil {
					return err
				}
			}

			if i == len(path)-1 {
				err = w.write([]byte{0x7f, 0xff, 0x04, 0x00})
			} else {
				err = w.write([]byte{0x7f, 0x01, 0x04, 0x00})
			}

			if err != nil {
				return err
			}
		}
	}

	return nil
}

// devicePath dissects a device path structure.
var devicePath = wrap(func(r *reader) ([]string, error) {
	paths, err := devicePathsDissect(r)
	if err != nil {
		return nil, err
	}

	if len(paths) != 1 {
		return nil, errUnexpectedData
	}

	return paths[0], nil
}, func(w *writer, v []string) error {
	return devicePathsFormat(w, [][]string{v})
})
