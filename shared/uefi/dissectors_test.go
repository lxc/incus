package uefi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func testDPNode(t *testing.T, dissected string, name string, args ...string) {
	b, err := devicePathNodeFormat(name, args...)
	assert.Nil(t, err)
	s, err := devicePathNodeDissect(b[0], b[1], b[4:])
	assert.Nil(t, err)
	assert.Equal(t, dissected, s)
}

func TestDPNode(t *testing.T) {
	// 0x01.
	testDPNode(t, "Pci(0x0,0x1)", "pci", "0", "0x1")
	testDPNode(t, "PcCard(0x2a)", "pccard", "42")
	testDPNode(t, "MemoryMapped(0x1092,0x0,0x400)", "memorymapped", "4242", "0", "1024")
	testDPNode(t, "VenHw(00112233-4455-6677-8899-aabbccddeeff,1234abcd)", "venhw", "00112233-4455-6677-8899-aabbccddeeff", "1234abcd")
	testDPNode(t, "VenHw(00112233-4455-6677-8899-aabbccddeeff,1234abcd)", "venhw", "00112233-4455-6677-8899-aabbccddeeff", "0x1234abcd")
	testDPNode(t, "Ctrl(0x1092)", "ctrl", "4242")
	testDPNode(t, "BMC(0x2a,0x4242)", "bmc", "42", "0x4242")
	testDPNode(t, "HardwarePath(0x2a,4242)", "hardwarepath", "42", "4242")
	testDPNode(t, "BMC(0x2a,0x4242)", "hardwarepath", "6", "2a4242000000000000")

	// 0x02.
	testDPNode(t, "Keyboard(0x0)", "keyboard")
	testDPNode(t, "ParallelPort(0x0)", "parallelport")
	testDPNode(t, "Serial(0x0)", "serial")
	testDPNode(t, "Floppy(0x0)", "floppy")
	testDPNode(t, "PciRoot(0x0)", "pciroot")
	testDPNode(t, "PcieRoot(0x0)", "pcieroot")
	testDPNode(t, "Acpi(PNPB003)", "acpi", "PNPB003")
	testDPNode(t, "Acpi(PNPB003)", "acpi", "PNPB003", "0")
	testDPNode(t, "Acpi(PNPB003,0x1)", "acpi", "PNPB003", "1")
	testDPNode(t, "PciRoot(0x1)", "acpi", "PNP0A03", "1")
	testDPNode(t, "AcpiEx(0,0,0x0,ACPI0003)", "acpiex", "0", "0", "0", "ACPI0003")
	testDPNode(t, "AcpiEx(ABC0001,ABC0002)", "acpiex", "ABC0001", "ABC0002")
	testDPNode(t, "AcpiEx(ABC0001,ABC0002)", "acpiex", "ABC0001", "ABC0002", "0", "", "")
	testDPNode(t, "AcpiExp(PNP0C02,0,MBR0)", "acpiexp", "PNP0C02", "0", "MBR0")
	testDPNode(t, "AcpiAdr(0x0,0x1,0x2)", "acpiadr", "0", "1", "2")
	testDPNode(t, "NvdimmAcpiAdr(0x0)", "nvdimmacpiadr", "0")
	testDPNode(t, "AcpiPath(0x2a,4242)", "acpipath", "42", "4242")
	testDPNode(t, "Acpi(PNPB003)", "acpipath", "0x1", "D04103B000000000")

	// 0x03.
	testDPNode(t, "Ata(Primary,Master,0x3)", "ata", "primary", "master", "3")
	testDPNode(t, "Scsi(0x2a,0x1092)", "scsi", "42", "4242")
	testDPNode(t, "Fibre(0x2a,0x1092)", "fibre", "42", "4242")
	testDPNode(t, "I1394(0x1092)", "i1394", "4242")
	testDPNode(t, "USB(0x1,0x2)", "usb", "1", "2")
	testDPNode(t, "I2O(0x4242)", "i2o", "0x4242")
	testDPNode(t, "Infiniband(0x0,00112233-4455-6677-8899-aabbccddeeff,0x1092,0x4242,0x2a)", "infiniband", "0", "00112233-4455-6677-8899-aabbccddeeff", "4242", "0x4242", "42")
	testDPNode(t, "VenMsg(00112233-4455-6677-8899-aabbccddeeff,4242)", "venmsg", "00112233-4455-6677-8899-aabbccddeeff", "4242")
	testDPNode(t, "VenPcAnsi()", "venmsg", EfiPcAnsiGuid)
	for _, node := range []string{"VenPcAnsi", "VenVt100", "VenVt100Plus", "VenUtf8", "DebugPort"} {
		testDPNode(t, node+"()", strings.ToLower(node))
	}

	testDPNode(t, "UartFlowCtrl(XonXoff)", "uartflowctrl", "xonxoff")
	testDPNode(t, "Sas(0x42,0x2a)", "sas", "0x42", "42")
	testDPNode(t, "Sas(0x42,0x2a)", "sas", "0x42", "42", "0", "NoTopology")
	testDPNode(t, "Sas(0x42,0x2a)", "sas", "0x42", "42", "NoTopology")
	testDPNode(t, "Sas(0x42,NoTopology,0x42)", "sas", "0x42", "0", "0", "NoTopology", "0x42")
	testDPNode(t, "Sas(0x42,0x0,0x1,NoTopology,0x42)", "sas", "0x42", "0", "1", "NoTopology", "0x42")
	testDPNode(t, "Sas(0x42,NoTopology,0x42)", "sas", "0x42", "NoTopology", "0x42")
	testDPNode(t, "Sas(0x42,SATA,Internal,Expanded)", "sas", "0x42", "sata", "internal", "expanded")
	testDPNode(t, "Sas(0x42,0x0,SAS,Internal,Expanded)", "sas", "0x42", "sas", "internal", "expanded")
	testDPNode(t, "Sas(0x42,SATA,Internal,Expanded)", "sas", "0x42", "0", "0", "0x0051")
	testDPNode(t, "Sas(0x42,0xabcd)", "sas", "0x42", "0", "0", "0xabcd")
	testDPNode(t, "Sas(0x42,0x2a,NoTopology,0x1092)", "sas", "0x42", "42", "0", "notopology", "4242")
	testDPNode(t, "Sas(0x42,0x0,SAS,Internal,Expanded,1)", "sas", "0x42", "sas", "internal", "expanded", "1")
	testDPNode(t, "Sas(0x42,0x0,SAS,Internal,Expanded,0x200)", "sas", "0x42", "sas", "internal", "expanded", "512")
	testDPNode(t, "Sas(0x42,0x0,SAS,Internal,Expanded,0x1)", "sas", "0x42", "sas", "internal", "expanded", "0x1")
	testDPNode(t, "Sas(0x42,0x0,SAS,Internal,Expanded,1,0x1)", "sas", "0x42", "sas", "internal", "expanded", "1", "0x1")
	testDPNode(t, "MAC(001122334455)", "mac", "001122334455")
	testDPNode(t, "MAC(0011223344550000000000000000000000000000000000000000000000000000,0x2)", "mac", "001122334455", "2")
	testDPNode(t, "IPv4(12.34.56.78)", "ipv4", "12.34.56.78")
	testDPNode(t, "IPv4(12.34.56.78:80)", "ipv4", "12.34.56.78:80")
	testDPNode(t, "IPv4(12.34.56.78:80)", "ipv4", "12.34.56.78:80", "UDP", "DHCP")
	testDPNode(t, "IPv4(12.34.56.78:80,TCP,Static,87.65.43.21,12.34.56.1,255.255.255.0)", "ipv4", "12.34.56.78:80", "TCP", "static", "87.65.43.21", "12.34.56.1", "255.255.255.0")
	testDPNode(t, "IPv6(2001::cafe,UDP,Static)", "ipv6", "2001::cafe", "udp", "static")
	testDPNode(t, "IPv6(2001::cafe,UDP,Static)", "ipv6", "[2001::cafe]", "udp", "static")
	testDPNode(t, "IPv6([2001::cafe]:80,UDP,Static)", "ipv6", "[2001::cafe]:80", "udp", "static")
	testDPNode(t, "IPv6([2001::cafe]:80,UDP,Static,cafe::2001,2001::,48)", "ipv6", "[2001::cafe]:80", "UDP", "static", "cafe::2001", "2001::", "48")
	testDPNode(t, "Uart()", "uart", "115200", "8", "d")
	testDPNode(t, "Uart(115200,8,D,2)", "uart", "115200", "8", "d", "2")
	testDPNode(t, "Uart(115200,8,0x0,0x2a)", "uart", "115200", "8", "0", "42")
	testDPNode(t, "UsbClass()", "usbclass")
	testDPNode(t, "UsbClass()", "usbclass", "0xffff", "0xffff", "0xff")
	testDPNode(t, "UsbClass(0xffff,0xffff,0xff,0x2)", "usbclass", "0xffff", "0xffff", "0xff", "2")
	for _, node := range []string{"UsbAudio", "UsbCDCControl", "UsbHID", "UsbImage", "UsbPrinter", "UsbMassStorage", "UsbHub", "UsbCDCData", "UsbSmartCard", "UsbVideo", "UsbDiagnostic", "UsbWireless", "UsbDeviceFirmwareUpdate", "UsbIrdaBridge", "UsbTestAndMeasurement"} {
		testDPNode(t, node+"(0x2a)", strings.ToLower(node), "42")
	}

	testDPNode(t, "UsbWwid(0x2a,0x42,0x4242,foobar)", "usbwwid", "42", "0x42", "0x4242", "foobar")
	testDPNode(t, `UsbWwid(0x2a,0x42,0x4242,"foo,bar")`, "usbwwid", "42", "0x42", "0x4242", "foo,bar")
	testDPNode(t, "Unit(0x42)", "unit", "0x42")
	testDPNode(t, "Sata(0x2a)", "sata", "42", "0xffff", "0")
	testDPNode(t, "Sata(0x2a,0xffff,0x1)", "sata", "42", "0xffff", "1")
	testDPNode(t, "iSCSI(iqn.2013-09.org.linuxcontainers,0x1,0x2a)", "iscsi", "iqn.2013-09.org.linuxcontainers", "1", "42")
	testDPNode(t, "iSCSI(iqn.2013-09.org.linuxcontainers,0x1,0x2a)", "iscsi", "iqn.2013-09.org.linuxcontainers", "1", "42", "none", "none", "none")
	testDPNode(t, "iSCSI(iqn.2013-09.org.linuxcontainers,0x1,0x2a,None,None,CHAP_UNI)", "iscsi", "iqn.2013-09.org.linuxcontainers", "1", "42", "none", "none", "chap_uni")
	testDPNode(t, "iSCSI(iqn.2013-09.org.linuxcontainers,0x1,0x2a,CRC32C,None,None,0x1)", "iscsi", "iqn.2013-09.org.linuxcontainers", "1", "42", "crc32c", "none", "none", "1")
	testDPNode(t, "Vlan(42)", "vlan", "42")
	testDPNode(t, "FibreEx(0x2a,0x1092)", "fibreex", "42", "4242")
	testDPNode(t, "SasEx(0x42,0x2a)", "sasex", "0x42", "42")
	testDPNode(t, "SasEx(0x42,0x2a)", "sasex", "0x42", "42", "0", "NoTopology")
	testDPNode(t, "SasEx(0x42,0x2a)", "sasex", "0x42", "42", "NoTopology")
	testDPNode(t, "SasEx(0x42,SATA,Internal,Expanded)", "sasex", "0x42", "sata", "internal", "expanded")
	testDPNode(t, "SasEx(0x42,0x0,SAS,Internal,Expanded)", "sasex", "0x42", "sas", "internal", "expanded")
	testDPNode(t, "SasEx(0x42,SATA,Internal,Expanded)", "sasex", "0x42", "0", "0", "0x0051")
	testDPNode(t, "SasEx(0x42,0xabcd)", "sasex", "0x42", "0", "0", "0xabcd")
	testDPNode(t, "SasEx(0x42,0x0,SAS,Internal,Expanded,1)", "sasex", "0x42", "sas", "internal", "expanded", "1")
	testDPNode(t, "NVMe(0x2a,00-11-22-33-44-55-66-77)", "nvme", "42", "00-11-22-33-44-55-66-77")
	testDPNode(t, "Uri()", "uri")
	testDPNode(t, "Uri(https://linuxcontainers.org)", "uri", "https://linuxcontainers.org")
	testDPNode(t, "UFS(0x0,0x42)", "ufs", "0", "0x42")
	testDPNode(t, "SD()", "sd", "0")
	testDPNode(t, "SD(0x2a)", "sd", "42")
	testDPNode(t, "Bluetooth(001122334455)", "bluetooth", "001122334455")
	testDPNode(t, `Wi-Fi("Linux Containers")`, "wi-fi", "Linux Containers")
	testDPNode(t, `Wi-Fi(LinuxContainers)`, "wi-fi", "LinuxContainers\x00\x00")
	testDPNode(t, "eMMC()", "emmc")
	testDPNode(t, "eMMC(0x2a)", "emmc", "42")
	testDPNode(t, "BluetoothLE(001122334455,0x42)", "bluetoothle", "001122334455", "0x42")
	testDPNode(t, "Dns(10.0.0.1,10.0.0.2)", "dns", "10.0.0.1", "10.0.0.2")
	testDPNode(t, "Dns(2001::1,2001::2)", "dns", "2001::1", "2001::2")
	testDPNode(t, "Dns(2001::1,::ffff:10.0.0.2)", "dns", "2001::1", "10.0.0.2")
	testDPNode(t, "NVDIMM(00112233-4455-6677-8899-aabbccddeeff)", "nvdimm", "00112233-4455-6677-8899-aabbccddeeff")
	testDPNode(t, "RestService(0x1,0x2)", "restservice", "1", "2")
	testDPNode(t, "RestService(0xff,0x2,00112233-4455-6677-8899-aabbccddeeff)", "restservice", "255", "2", "00112233-4455-6677-8899-aabbccddeeff")
	testDPNode(t, "RestService(0xff,0x2,00112233-4455-6677-8899-aabbccddeeff,4242)", "restservice", "255", "2", "00112233-4455-6677-8899-aabbccddeeff", "4242")
	testDPNode(t, "NVMEoF(iqn.2013-09.org.linuxcontainers,eui:00-11-22-33-44-55-66-77)", "nvmeof", "iqn.2013-09.org.linuxcontainers", "eui:00-11-22-33-44-55-66-77")
	testDPNode(t, "NVMEoF(iqn.2013-09.org.linuxcontainers,nvme-nguid:0011223344556677-8899aa-bbccddeeff)", "nvmeof", "iqn.2013-09.org.linuxcontainers", "nvme-nguid:0011223344556677-8899aa-bbccddeeff")
	testDPNode(t, "NVMEoF(iqn.2013-09.org.linuxcontainers,urn:uuid:00112233-4455-6677-8899-aabbccddeeff)", "nvmeof", "iqn.2013-09.org.linuxcontainers", "urn:uuid:00112233-4455-6677-8899-aabbccddeeff")

	// 0x04.
	testDPNode(t, "HD(0x0,MBR,0x42)", "hd", "0", "mbr", "0x42")
	testDPNode(t, "HD(0x0,GPT,00112233-4455-6677-8899-aabbccddeeff)", "hd", "0", "gpt", "00112233-4455-6677-8899-aabbccddeeff")
	testDPNode(t, "HD(0x1,MBR,0x42,2048,3072000)", "hd", "1", "mbr", "0x42", "0x800", "0x2ee000")
	testDPNode(t, "HD(0x1,GPT,00112233-4455-6677-8899-aabbccddeeff,2048,3072000)", "hd", "1", "gpt", "00112233-4455-6677-8899-aabbccddeeff", "0x800", "0x2ee000")
	testDPNode(t, "CDROM(0x0,1024,102400)", "cdrom", "0", "1024", "102400")
	testDPNode(t, "VenMedia(00112233-4455-6677-8899-aabbccddeeff)", "venmedia", "00112233-4455-6677-8899-aabbccddeeff")
	testDPNode(t, "VenMedia(00112233-4455-6677-8899-aabbccddeeff,4242)", "venmedia", "00112233-4455-6677-8899-aabbccddeeff", "4242")
	testDPNode(t, `\EFI\BOOT\BOOTX64.EFI`, "", `\EFI\BOOT\BOOTX64.EFI`)
	for _, node := range []string{"Media", "FvFile", "Fv"} {
		testDPNode(t, node+"(00112233-4455-6677-8899-aabbccddeeff)", strings.ToLower(node), "00112233-4455-6677-8899-aabbccddeeff")
	}

	testDPNode(t, "Offset(1024,2048)", "offset", "0x400", "0x800")
	for _, node := range []string{"VirtualDisk", "VirtualCD", "PersistentVirtualDisk", "PersistentVirtualCD"} {
		testDPNode(t, node+"(1024,102400,0x1)", strings.ToLower(node), "1024", "102400", "1")
		testDPNode(t, node+"(1024,102400)", strings.ToLower(node), "1024", "102400")
	}

	testDPNode(t, "RamDisk(1024,102400,0x1,00112233-4455-6677-8899-aabbccddeeff)", "ramdisk", "1024", "102400", "1", "00112233-4455-6677-8899-aabbccddeeff")
	testDPNode(t, "VirtualDisk(1024,102400)", "ramdisk", "1024", "102400", "0", EfiVirtualDiskGuid)

	// 0x05.
	testDPNode(t, "BBS(HD,Test,0x42)", "bbs", "hd", "Test", "66")
	testDPNode(t, "BbsPath(0x2a,4242)", "bbspath", "42", "4242")
	testDPNode(t, "BBS(HD,Test,0x42)", "bbspath", "1", "020042005465737400")
}

func testDPInstanceDecomposition(t *testing.T, decomposed [][]string, instance string) {
	d, err := decomposeDPInstance(instance)
	assert.Nil(t, err)
	assert.Equal(t, decomposed, d)
}

func TestDPInstanceDecomposition(t *testing.T) {
	testDPInstanceDecomposition(t, [][]string{{"foo", "bar"}}, `Foo(bar)`)
	testDPInstanceDecomposition(t, [][]string{{"foo", "bar"}}, `Foo("bar")`)
	testDPInstanceDecomposition(t, [][]string{{"foo", "bar/baz()"}}, `Foo("bar/baz()")`)
	testDPInstanceDecomposition(t, [][]string{{"foo", "bar"}, {"baz", "qux"}}, `Foo(bar)/Baz("qux")`)
	testDPInstanceDecomposition(t, [][]string{{"", `\EFI\BOOT\BOOTX64.EFI`}}, `\EFI\BOOT\BOOTX64.EFI`)
	testDPInstanceDecomposition(t, [][]string{{"foo", "bar"}, {"", `\EFI\BOOT\BOOTX64.EFI`}}, `Foo(bar)/\EFI\BOOT\BOOTX64.EFI`)
}

func testDP(t *testing.T, paths [][]string) {
	w := newWriter()
	err := devicePathsFormat(w, paths)
	assert.Nil(t, err)
	newPaths, err := devicePathsDissect(newReader(w.data))
	assert.Nil(t, err)
	assert.Equal(t, paths, newPaths)
}

func TestDP(t *testing.T) {
	testDP(t, [][]string{{`HD(0x1,GPT,89264b7c-8900-43a2-9167-e503d414dce3,2048,204800)/\EFI\ubuntu\shimx64.efi`}})
	testDP(t, [][]string{{`PciRoot(0x0)/Pci(0x1f,0x0)/Acpi(PNP0303)`, `PciRoot(0x0)/Pci(0x1f,0x0)/Serial(0x0)/Uart(115200,8,N,1)/VenUtf8()`, `UsbHID(0xffff,0xffff,0x1,0x1)`, `PciRoot(0x0)/Pci(0x1,0x0)/Pci(0x0,0x2)`, `PciRoot(0x0)/Pci(0x1,0x0)/Pci(0x0,0x3)`}})
	testDP(t, [][]string{{`PciRoot(0x0)/Pci(0x1f,0x0)/Acpi(PNP0303)`}, {`PciRoot(0x0)/Pci(0x1f,0x0)/Serial(0x0)/Uart(115200,8,N,1)/VenUtf8()`}, {`UsbHID(0xffff,0xffff,0x1,0x1)`}, {`PciRoot(0x0)/Pci(0x1,0x0)/Pci(0x0,0x2)`}, {`PciRoot(0x0)/Pci(0x1,0x0)/Pci(0x0,0x3)`}})
}
