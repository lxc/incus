package drivers

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lxc/incus/v6/internal/server/instance/drivers/cfg"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/resources"
)

func writeHeader(sb *strings.Builder, comment string, name string) {
	if comment != "" {
		fmt.Fprintf(sb, "# %s\n", comment)
	}

	fmt.Fprintf(sb, "[%s]\n", name)
}

func writeEntry(sb *strings.Builder, key string, value string) {
	if value != "" {
		fmt.Fprintf(sb, "%s = \"%s\"\n", key, value)
	}
}

func qemuStringifyCfg(conf ...cfg.Section) *strings.Builder {
	sb := &strings.Builder{}

	for _, section := range conf {
		writeHeader(sb, section.Comment, section.Name)

		for key, value := range section.Entries {
			writeEntry(sb, key, value)
		}

		sb.WriteString("\n")
	}

	return sb
}

// qemuStringifyCfgPredictably is only there to ensure tests reproducibility.
func qemuStringifyCfgPredictably(conf ...cfg.Section) *strings.Builder {
	sb := &strings.Builder{}

	for _, section := range conf {
		writeHeader(sb, section.Comment, section.Name)

		keys := make([]string, 0, len(section.Entries))
		for key := range section.Entries {
			keys = append(keys, key)
		}

		sort.Strings(keys)
		for _, key := range keys {
			writeEntry(sb, key, section.Entries[key])
		}

		sb.WriteString("\n")
	}

	return sb
}

func qemuMachineType(architecture int) string {
	var machineType string

	switch architecture {
	case osarch.ARCH_64BIT_INTEL_X86:
		machineType = "q35"
	case osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN:
		machineType = "virt"
	case osarch.ARCH_64BIT_POWERPC_LITTLE_ENDIAN:
		machineType = "pseries"
	case osarch.ARCH_64BIT_S390_BIG_ENDIAN:
		machineType = "s390-ccw-virtio"
	}

	return machineType
}

type qemuBaseOpts struct {
	architecture int
	iommu        bool
	definition   string
}

func qemuBase(opts *qemuBaseOpts) []cfg.Section {
	machineType := qemuMachineType(opts.architecture)
	gicVersion := ""
	capLargeDecr := ""

	switch opts.architecture {
	case osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN:
		gicVersion = "max"
	case osarch.ARCH_64BIT_POWERPC_LITTLE_ENDIAN:
		capLargeDecr = "off"
	}

	if opts.definition != "" {
		machineType = opts.definition
	}

	sections := []cfg.Section{{
		Name:    "machine",
		Comment: "Machine",
		Entries: map[string]string{
			"graphics":       "off",
			"type":           machineType,
			"gic-version":    gicVersion,
			"cap-large-decr": capLargeDecr,
			"accel":          "kvm",
			"usb":            "off",
		},
	}}

	if opts.iommu {
		sections[0].Entries["kernel-irqchip"] = "split"
	}

	if opts.architecture == osarch.ARCH_64BIT_INTEL_X86 {
		sections = append(sections, []cfg.Section{{
			Name: "global",
			Entries: map[string]string{
				"driver":   "ICH9-LPC",
				"property": "disable_s3",
				"value":    "1",
			},
		}, {
			Name: "global",
			Entries: map[string]string{
				"driver":   "ICH9-LPC",
				"property": "disable_s4",
				"value":    "0",
			},
		}}...)
	}

	return append(
		sections,
		cfg.Section{
			Name:    "boot-opts",
			Entries: map[string]string{"strict": "on"},
		})
}

type qemuMemoryOpts struct {
	memSizeMB int64
	maxSizeMB int64
}

func qemuMemory(opts *qemuMemoryOpts) []cfg.Section {
	// Sets fixed values for slots and maxmem to support memory hotplug.
	section := cfg.Section{
		Name:    "memory",
		Comment: "Memory",
		Entries: map[string]string{
			"size":   fmt.Sprintf("%dM", opts.memSizeMB),
			"maxmem": fmt.Sprintf("%dM", opts.maxSizeMB),
			// Some systems hit odd errors when using more than 8 hotplug slots.
			// That's even with maxmem capped at the total system memory.
			"slots": "8",
		},
	}

	// Disable hotplug when already at maximum.
	if section.Entries["size"] == section.Entries["maxmem"] {
		delete(section.Entries, "slots")
	}

	return []cfg.Section{section}
}

type qemuDevOpts struct {
	busName       string
	devBus        string
	devAddr       string
	multifunction bool
}

type qemuDevEntriesOpts struct {
	dev     qemuDevOpts
	pciName string
	ccwName string
}

func qemuDeviceEntries(opts *qemuDevEntriesOpts) map[string]string {
	entries := make(map[string]string)

	if opts.dev.busName == "pci" || opts.dev.busName == "pcie" {
		entries["driver"] = opts.pciName
		entries["bus"] = opts.dev.devBus
		entries["addr"] = opts.dev.devAddr
	} else if opts.dev.busName == "ccw" {
		entries["driver"] = opts.ccwName
	}

	if opts.dev.multifunction {
		entries["multifunction"] = "on"
	}

	return entries
}

type qemuSerialOpts struct {
	dev              qemuDevOpts
	charDevName      string
	ringbufSizeBytes int
}

func qemuSerial(opts *qemuSerialOpts) []cfg.Section {
	entriesOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "virtio-serial-pci",
		ccwName: "virtio-serial-ccw",
	}

	return []cfg.Section{{
		Name:    `device "dev-qemu_serial"`,
		Comment: "Virtual serial bus",
		Entries: qemuDeviceEntries(&entriesOpts),
	}, {
		// Ring buffer used by the incus agent to report (write) its status to. Incus server will read
		// its content via QMP using "ringbuf-read" command.
		Name:    fmt.Sprintf(`chardev "%s"`, opts.charDevName),
		Comment: "Serial identifier",
		Entries: map[string]string{
			"backend": "ringbuf",
			"size":    fmt.Sprintf("%dB", opts.ringbufSizeBytes),
		},
	}, {
		// QEMU serial device connected to the above ring buffer.
		Name: `device "qemu_serial"`,
		Entries: map[string]string{
			"driver":  "virtserialport",
			"name":    "org.linuxcontainers.incus",
			"chardev": opts.charDevName,
			"bus":     "dev-qemu_serial.0",
		},
	}, {
		// Legacy QEMU serial device, not connected to any ring buffer. Its purpose is to
		// create a symlink in /dev/virtio-ports/, triggering a udev rule to start incus-agent.
		// This is necessary for backward compatibility with virtual machines lacking the
		// updated incus-agent-loader package, which includes updated udev rules and a systemd unit.
		Name: `device "qemu_serial_legacy"`,
		Entries: map[string]string{
			"driver": "virtserialport",
			"name":   "org.linuxcontainers.lxd",
			"bus":    "dev-qemu_serial.0",
		},
	}, {
		Name:    `chardev "qemu_spice-chardev"`,
		Comment: "Spice agent",
		Entries: map[string]string{
			"backend": "spicevmc",
			"name":    "vdagent",
		},
	}, {
		Name: `device "qemu_spice"`,
		Entries: map[string]string{
			"driver":  "virtserialport",
			"name":    "com.redhat.spice.0",
			"chardev": "qemu_spice-chardev",
			"bus":     "dev-qemu_serial.0",
		},
	}, {
		Name:    `chardev "qemu_spicedir-chardev"`,
		Comment: "Spice folder",
		Entries: map[string]string{
			"backend": "spiceport",
			"name":    "org.spice-space.webdav.0",
		},
	}, {
		Name: `device "qemu_spicedir"`,
		Entries: map[string]string{
			"driver":  "virtserialport",
			"name":    "org.spice-space.webdav.0",
			"chardev": "qemu_spicedir-chardev",
			"bus":     "dev-qemu_serial.0",
		},
	}}
}

type qemuPCIeOpts struct {
	portName      string
	index         int
	devAddr       string
	multifunction bool
}

func qemuPCIe(opts *qemuPCIeOpts) []cfg.Section {
	entries := map[string]string{
		"driver":  "pcie-root-port",
		"bus":     "pcie.0",
		"addr":    opts.devAddr,
		"chassis": fmt.Sprintf("%d", opts.index),
	}

	if opts.multifunction {
		entries["multifunction"] = "on"
	}

	return []cfg.Section{{
		Name:    fmt.Sprintf(`device "%s"`, opts.portName),
		Entries: entries,
	}}
}

func qemuSCSI(opts *qemuDevOpts) []cfg.Section {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-scsi-pci",
		ccwName: "virtio-scsi-ccw",
	}

	return []cfg.Section{{
		Name:    `device "qemu_scsi"`,
		Comment: "SCSI controller",
		Entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuBalloon(opts *qemuDevOpts) []cfg.Section {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-balloon-pci",
		ccwName: "virtio-balloon-ccw",
	}

	return []cfg.Section{{
		Name:    `device "qemu_balloon"`,
		Comment: "Balloon driver",
		Entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuRNG(opts *qemuDevOpts) []cfg.Section {
	entries := qemuDeviceEntries(&qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-rng-pci",
		ccwName: "virtio-rng-ccw",
	})
	entries["rng"] = "qemu_rng"

	return []cfg.Section{{
		Name:    `object "qemu_rng"`,
		Comment: "Random number generator",
		Entries: map[string]string{
			"qom-type": "rng-random",
			"filename": "/dev/urandom",
		},
	}, {
		Name:    `device "dev-qemu_rng"`,
		Entries: entries,
	}}
}

func qemuCoreInfo() []cfg.Section {
	return []cfg.Section{{
		Name:    `device "qemu_vmcoreinfo"`,
		Comment: "VM core info driver",
		Entries: map[string]string{"driver": "vmcoreinfo"},
	}}
}

func qemuIOMMU(opts *qemuDevOpts, isWindows bool) []cfg.Section {
	if isWindows {
		return []cfg.Section{{
			Name:    `device "intel-iommu"`,
			Comment: "IOMMU driver",
			Entries: map[string]string{
				"driver":       "intel-iommu",
				"intremap":     "on",
				"caching-mode": "on",
			},
		}}
	}

	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-iommu-pci",
	}

	return []cfg.Section{{
		Name:    `device "qemu_iommu"`,
		Comment: "IOMMU driver",
		Entries: qemuDeviceEntries(&entriesOpts),
	}}
}

type qemuSevOpts struct {
	cbitpos         int
	reducedPhysBits int
	policy          string
	dhCertFD        string
	sessionDataFD   string
}

func qemuSEV(opts *qemuSevOpts) []cfg.Section {
	entries := map[string]string{
		"qom-type":          "sev-guest",
		"cbitpos":           fmt.Sprintf("%d", opts.cbitpos),
		"reduced-phys-bits": fmt.Sprintf("%d", opts.reducedPhysBits),
		"policy":            opts.policy,
	}

	if opts.dhCertFD != "" && opts.sessionDataFD != "" {
		entries["dh-cert-file"] = opts.dhCertFD
		entries["session-file"] = opts.sessionDataFD
	}

	return []cfg.Section{{
		Name:    `object "sev0"`,
		Comment: "Secure Encrypted Virtualization",
		Entries: entries,
	}}
}

type qemuVsockOpts struct {
	dev     qemuDevOpts
	vsockFD int
	vsockID uint32
}

func qemuVsock(opts *qemuVsockOpts) []cfg.Section {
	entries := qemuDeviceEntries(&qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "vhost-vsock-pci",
		ccwName: "vhost-vsock-ccw",
	})
	entries["guest-cid"] = fmt.Sprintf("%d", opts.vsockID)
	entries["vhostfd"] = fmt.Sprintf("%d", opts.vsockFD)

	return []cfg.Section{{
		Name:    `device "qemu_vsock"`,
		Comment: "Vsock",
		Entries: entries,
	}}
}

type qemuGpuOpts struct {
	dev          qemuDevOpts
	architecture int
}

func qemuGPU(opts *qemuGpuOpts) []cfg.Section {
	var pciName string

	if opts.architecture == osarch.ARCH_64BIT_INTEL_X86 {
		pciName = "virtio-vga"
	} else {
		pciName = "virtio-gpu-pci"
	}

	entriesOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: pciName,
		ccwName: "virtio-gpu-ccw",
	}

	return []cfg.Section{{
		Name:    `device "qemu_gpu"`,
		Comment: "GPU",
		Entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuKeyboard(opts *qemuDevOpts) []cfg.Section {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-keyboard-pci",
		ccwName: "virtio-keyboard-ccw",
	}

	return []cfg.Section{{
		Name:    `device "qemu_keyboard"`,
		Comment: "Input",
		Entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuTablet(opts *qemuDevOpts) []cfg.Section {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-tablet-pci",
		ccwName: "virtio-tablet-ccw",
	}

	return []cfg.Section{{
		Name:    `device "qemu_tablet"`,
		Comment: "Input",
		Entries: qemuDeviceEntries(&entriesOpts),
	}}
}

type qemuNumaEntry struct {
	node   uint64
	socket uint64
	core   uint64
	thread uint64
}

type qemuCPUOpts struct {
	architecture        string
	cpuCount            int
	cpuRequested        int
	cpuSockets          int
	cpuCores            int
	cpuThreads          int
	cpuNumaNodes        []uint64
	cpuNumaMapping      []qemuNumaEntry
	cpuNumaHostNodes    []uint64
	hugepages           string
	memory              int64
	memoryHostNodes     []int64
	qemuMemObjectFormat string
}

func qemuCPUNumaHostNode(opts *qemuCPUOpts, index int) []cfg.Section {
	entries := make(map[string]string)

	if opts.hugepages != "" {
		entries["qom-type"] = "memory-backend-file"
		entries["mem-path"] = opts.hugepages
		entries["prealloc"] = "on"
		entries["discard-data"] = "on"
	} else {
		entries["qom-type"] = "memory-backend-memfd"
	}

	entries["size"] = fmt.Sprintf("%dM", opts.memory)

	return []cfg.Section{{
		Name:    fmt.Sprintf("object \"mem%d\"", index),
		Entries: entries,
	}, {
		Name: "numa",
		Entries: map[string]string{
			"type":   "node",
			"nodeid": fmt.Sprintf("%d", index),
			"memdev": fmt.Sprintf("mem%d", index),
		},
	}}
}

func qemuCPU(opts *qemuCPUOpts, pinning bool) []cfg.Section {
	entries := map[string]string{"cpus": fmt.Sprintf("%d", opts.cpuCount)}

	if pinning {
		entries["sockets"] = fmt.Sprintf("%d", opts.cpuSockets)
		entries["cores"] = fmt.Sprintf("%d", opts.cpuCores)
		entries["threads"] = fmt.Sprintf("%d", opts.cpuThreads)
	} else {
		cpu, err := resources.GetCPU()
		if err != nil {
			return nil
		}

		// Cap the max number of CPUs to 64 unless directly assigned more.
		maxCpus := 64
		if int(cpu.Total) < maxCpus {
			maxCpus = int(cpu.Total)
		} else if opts.cpuRequested > maxCpus {
			maxCpus = opts.cpuRequested
		} else if opts.cpuCount > maxCpus {
			maxCpus = opts.cpuCount
		}

		entries["maxcpus"] = fmt.Sprintf("%d", maxCpus)
	}

	sections := []cfg.Section{{
		Name:    "smp-opts",
		Comment: "CPU",
		Entries: entries,
	}}

	if opts.architecture != "x86_64" {
		return sections
	}

	if len(opts.cpuNumaHostNodes) == 0 {
		// Add one mem and one numa sections with index 0.
		numaHostNode := qemuCPUNumaHostNode(opts, 0)

		// Unconditionally append "share = "on" to the [object "mem0"] section
		numaHostNode[0].Entries["share"] = "on"

		// If NUMA memory restrictions are set, apply them.
		if len(opts.memoryHostNodes) > 0 {
			numaHostNode[0].Entries["policy"] = "bind"

			for index, element := range opts.memoryHostNodes {
				var hostNodesKey string
				if opts.qemuMemObjectFormat == "indexed" {
					hostNodesKey = fmt.Sprintf("host-nodes.%d", index)
				} else {
					hostNodesKey = "host-nodes"
				}

				numaHostNode[0].Entries[hostNodesKey] = fmt.Sprintf("%d", element)
			}
		}

		return append(sections, numaHostNode...)
	}

	for index, element := range opts.cpuNumaHostNodes {
		numaHostNode := qemuCPUNumaHostNode(opts, index)

		numaHostNode[0].Entries["policy"] = "bind"

		if opts.hugepages != "" {
			// append share = "on" only if hugepages is set
			numaHostNode[0].Entries["share"] = "on"
		}

		var hostNodesKey string
		if opts.qemuMemObjectFormat == "indexed" {
			hostNodesKey = "host-nodes.0"
		} else {
			hostNodesKey = "host-nodes"
		}

		numaHostNode[0].Entries[hostNodesKey] = fmt.Sprintf("%d", element)
		sections = append(sections, numaHostNode...)
	}

	for _, numa := range opts.cpuNumaMapping {
		sections = append(sections, cfg.Section{
			Name: "numa",
			Entries: map[string]string{
				"type":      "cpu",
				"node-id":   fmt.Sprintf("%d", numa.node),
				"socket-id": fmt.Sprintf("%d", numa.socket),
				"core-id":   fmt.Sprintf("%d", numa.core),
				"thread-id": fmt.Sprintf("%d", numa.thread),
			},
		})
	}

	return sections
}

type qemuControlSocketOpts struct {
	path string
}

func qemuControlSocket(opts *qemuControlSocketOpts) []cfg.Section {
	return []cfg.Section{{
		Name:    `chardev "monitor"`,
		Comment: "Qemu control",
		Entries: map[string]string{
			"backend": "socket",
			"path":    opts.path,
			"server":  "on",
			"wait":    "off",
		},
	}, {
		Name: "mon",
		Entries: map[string]string{
			"chardev": "monitor",
			"mode":    "control",
		},
	}}
}

func qemuConsole() []cfg.Section {
	return []cfg.Section{{
		Name:    `chardev "console"`,
		Comment: "Console",
		Entries: map[string]string{
			"backend": "ringbuf",
			"size":    "1048576",
		},
	}}
}

type qemuDriveFirmwareOpts struct {
	roPath    string
	nvramPath string
}

func qemuDriveFirmware(opts *qemuDriveFirmwareOpts) []cfg.Section {
	return []cfg.Section{{
		Name:    "drive",
		Comment: "Firmware (read only)",
		Entries: map[string]string{
			"file":     opts.roPath,
			"if":       "pflash",
			"format":   "raw",
			"unit":     "0",
			"readonly": "on",
		},
	}, {
		Name:    "drive",
		Comment: "Firmware settings (writable)",
		Entries: map[string]string{
			"file":   opts.nvramPath,
			"if":     "pflash",
			"format": "raw",
			"unit":   "1",
		},
	}}
}

type qemuHostDriveOpts struct {
	dev           qemuDevOpts
	name          string
	nameSuffix    string
	comment       string
	fsdriver      string
	mountTag      string
	securityModel string
	path          string
	sockFd        string
	readonly      bool
	protocol      string
}

func qemuHostDrive(opts *qemuHostDriveOpts) []cfg.Section {
	var driveSection cfg.Section
	var entries map[string]string
	deviceOpts := qemuDevEntriesOpts{dev: opts.dev}

	if opts.protocol == "9p" {
		var readonly string
		if opts.readonly {
			readonly = "on"
		} else {
			readonly = "off"
		}

		driveSection = cfg.Section{
			Name:    fmt.Sprintf(`fsdev "%s"`, opts.name),
			Comment: opts.comment,
			Entries: map[string]string{
				"fsdriver":       opts.fsdriver,
				"sock_fd":        opts.sockFd,
				"security_model": opts.securityModel,
				"readonly":       readonly,
				"path":           opts.path,
			},
		}

		deviceOpts.pciName = "virtio-9p-pci"
		deviceOpts.ccwName = "virtio-9p-ccw"
		entries = qemuDeviceEntries(&deviceOpts)
		entries["mount_tag"] = opts.mountTag
		entries["fsdev"] = opts.name
	} else if opts.protocol == "virtio-fs" {
		driveSection = cfg.Section{
			Name:    fmt.Sprintf(`chardev "%s"`, opts.name),
			Comment: opts.comment,
			Entries: map[string]string{
				"backend": "socket",
				"path":    opts.path,
			},
		}

		deviceOpts.pciName = "vhost-user-fs-pci"
		deviceOpts.ccwName = "vhost-user-fs-ccw"
		entries = qemuDeviceEntries(&deviceOpts)
		entries["tag"] = opts.mountTag
		entries["chardev"] = opts.name
	} else {
		return []cfg.Section{}
	}

	return []cfg.Section{
		driveSection,
		{
			Name:    fmt.Sprintf(`device "dev-%s%s-%s"`, opts.name, opts.nameSuffix, opts.protocol),
			Entries: entries,
		},
	}
}

type qemuDriveConfigOpts struct {
	name     string
	dev      qemuDevOpts
	protocol string
	path     string
}

func qemuDriveConfig(opts *qemuDriveConfigOpts) []cfg.Section {
	return qemuHostDrive(&qemuHostDriveOpts{
		dev: opts.dev,
		// Devices use "qemu_" prefix indicating that this is a internally named device.
		name:          fmt.Sprintf("qemu_%s", opts.name),
		nameSuffix:    "-drive",
		comment:       fmt.Sprintf("Shared %s drive (%s)", opts.name, opts.protocol),
		mountTag:      opts.name,
		protocol:      opts.protocol,
		fsdriver:      "local",
		readonly:      true,
		securityModel: "none",
		path:          opts.path,
	})
}

type qemuDriveDirOpts struct {
	dev      qemuDevOpts
	devName  string
	mountTag string
	path     string
	protocol string
	readonly bool
}

func qemuDriveDir(opts *qemuDriveDirOpts) []cfg.Section {
	return qemuHostDrive(&qemuHostDriveOpts{
		dev:           opts.dev,
		name:          fmt.Sprintf("incus_%s", opts.devName),
		comment:       fmt.Sprintf("%s drive (%s)", opts.devName, opts.protocol),
		mountTag:      opts.mountTag,
		protocol:      opts.protocol,
		fsdriver:      "local",
		readonly:      opts.readonly,
		path:          opts.path,
		securityModel: "passthrough",
	})
}

type qemuPCIPhysicalOpts struct {
	dev         qemuDevOpts
	devName     string
	pciSlotName string
}

func qemuPCIPhysical(opts *qemuPCIPhysicalOpts) []cfg.Section {
	deviceOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "vfio-pci",
		ccwName: "vfio-ccw",
	}

	entries := qemuDeviceEntries(&deviceOpts)
	entries["host"] = opts.pciSlotName

	return []cfg.Section{{
		Name:    fmt.Sprintf(`device "%s%s"`, qemuDeviceIDPrefix, opts.devName),
		Comment: fmt.Sprintf(`PCI card ("%s" device)`, opts.devName),
		Entries: entries,
	}}
}

type qemuGPUDevPhysicalOpts struct {
	dev         qemuDevOpts
	devName     string
	pciSlotName string
	vgpu        string
	vga         bool
}

func qemuGPUDevPhysical(opts *qemuGPUDevPhysicalOpts) []cfg.Section {
	deviceOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "vfio-pci",
		ccwName: "vfio-ccw",
	}

	entries := qemuDeviceEntries(&deviceOpts)

	if opts.vgpu != "" {
		sysfsdev := fmt.Sprintf("/sys/bus/mdev/devices/%s", opts.vgpu)
		entries["sysfsdev"] = sysfsdev
	} else {
		entries["host"] = opts.pciSlotName
	}

	if opts.vga {
		entries["x-vga"] = "on"
	}

	return []cfg.Section{{
		Name:    fmt.Sprintf(`device "%s%s"`, qemuDeviceIDPrefix, opts.devName),
		Comment: fmt.Sprintf(`GPU card ("%s" device)`, opts.devName),
		Entries: entries,
	}}
}

type qemuUSBOpts struct {
	devBus        string
	devAddr       string
	multifunction bool
	ports         int
}

func qemuUSB(opts *qemuUSBOpts) []cfg.Section {
	deviceOpts := qemuDevEntriesOpts{
		dev: qemuDevOpts{
			busName:       "pci",
			devAddr:       opts.devAddr,
			devBus:        opts.devBus,
			multifunction: opts.multifunction,
		},
		pciName: "qemu-xhci",
	}

	entries := qemuDeviceEntries(&deviceOpts)
	entries["p2"] = fmt.Sprintf("%d", opts.ports)
	entries["p3"] = fmt.Sprintf("%d", opts.ports)
	sections := []cfg.Section{{
		Name:    `device "qemu_usb"`,
		Comment: "USB controller",
		Entries: entries,
	}}

	for i := 1; i <= 3; i++ {
		chardev := fmt.Sprintf("qemu_spice-usb-chardev%d", i)
		sections = append(sections, []cfg.Section{{
			Name: fmt.Sprintf(`chardev "%s"`, chardev),
			Entries: map[string]string{
				"backend": "spicevmc",
				"name":    "usbredir",
			},
		}, {
			Name: fmt.Sprintf(`device "qemu_spice-usb%d"`, i),
			Entries: map[string]string{
				"driver":  "usb-redir",
				"chardev": chardev,
			},
		}}...)
	}

	return sections
}

type qemuTPMOpts struct {
	devName string
	path    string
	driver  string
}

func qemuTPM(opts *qemuTPMOpts) []cfg.Section {
	chardev := fmt.Sprintf("qemu_tpm-chardev_%s", opts.devName)
	tpmdev := fmt.Sprintf("qemu_tpm-tpmdev_%s", opts.devName)

	return []cfg.Section{{
		Name: fmt.Sprintf(`chardev "%s"`, chardev),
		Entries: map[string]string{
			"backend": "socket",
			"path":    opts.path,
		},
	}, {
		Name: fmt.Sprintf(`tpmdev "%s"`, tpmdev),
		Entries: map[string]string{
			"type":    "emulator",
			"chardev": chardev,
		},
	}, {
		Name: fmt.Sprintf(`device "%s%s"`, qemuDeviceIDPrefix, opts.devName),
		Entries: map[string]string{
			"driver": opts.driver,
			"tpmdev": tpmdev,
		},
	}}
}

type qemuVmgenIDOpts struct {
	guid string
}

func qemuVmgen(opts *qemuVmgenIDOpts) []cfg.Section {
	return []cfg.Section{{
		Name:    `device "vmgenid0"`,
		Comment: "VM Generation ID",
		Entries: map[string]string{
			"driver": "vmgenid",
			"guid":   opts.guid,
		},
	}}
}
