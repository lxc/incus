package drivers

import (
	"fmt"
	"strings"

	"github.com/lxc/incus/v6/internal/server/resources"
	"github.com/lxc/incus/v6/shared/osarch"
)

type cfgEntry struct {
	key   string
	value string
}

type cfgSection struct {
	name    string
	comment string
	entries []cfgEntry
}

func qemuStringifyCfg(cfg ...cfgSection) *strings.Builder {
	sb := &strings.Builder{}

	for _, section := range cfg {
		if section.comment != "" {
			sb.WriteString(fmt.Sprintf("# %s\n", section.comment))
		}

		sb.WriteString(fmt.Sprintf("[%s]\n", section.name))

		for _, entry := range section.entries {
			value := entry.value
			if value != "" {
				sb.WriteString(fmt.Sprintf("%s = \"%s\"\n", entry.key, value))
			}
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
}

func qemuBase(opts *qemuBaseOpts) []cfgSection {
	machineType := qemuMachineType(opts.architecture)
	gicVersion := ""
	capLargeDecr := ""

	switch opts.architecture {
	case osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN:
		gicVersion = "max"
	case osarch.ARCH_64BIT_POWERPC_LITTLE_ENDIAN:
		capLargeDecr = "off"
	}

	sections := []cfgSection{{
		name:    "machine",
		comment: "Machine",
		entries: []cfgEntry{
			{key: "graphics", value: "off"},
			{key: "type", value: machineType},
			{key: "gic-version", value: gicVersion},
			{key: "cap-large-decr", value: capLargeDecr},
			{key: "accel", value: "kvm"},
			{key: "usb", value: "off"},
		},
	}}

	if opts.architecture == osarch.ARCH_64BIT_INTEL_X86 {
		sections = append(sections, []cfgSection{{
			name: "global",
			entries: []cfgEntry{
				{key: "driver", value: "ICH9-LPC"},
				{key: "property", value: "disable_s3"},
				{key: "value", value: "1"},
			},
		}, {
			name: "global",
			entries: []cfgEntry{
				{key: "driver", value: "ICH9-LPC"},
				{key: "property", value: "disable_s4"},
				{key: "value", value: "1"},
			},
		}}...)
	}

	return append(
		sections,
		cfgSection{
			name:    "boot-opts",
			entries: []cfgEntry{{key: "strict", value: "on"}},
		})
}

type qemuMemoryOpts struct {
	memSizeMB int64
}

func qemuMemory(opts *qemuMemoryOpts) []cfgSection {
	return []cfgSection{{
		name:    "memory",
		comment: "Memory",
		entries: []cfgEntry{{key: "size", value: fmt.Sprintf("%dM", opts.memSizeMB)}},
	}}
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

func qemuDeviceEntries(opts *qemuDevEntriesOpts) []cfgEntry {
	entries := []cfgEntry{}

	if opts.dev.busName == "pci" || opts.dev.busName == "pcie" {
		entries = append(entries, []cfgEntry{
			{key: "driver", value: opts.pciName},
			{key: "bus", value: opts.dev.devBus},
			{key: "addr", value: opts.dev.devAddr},
		}...)
	} else if opts.dev.busName == "ccw" {
		entries = append(entries, cfgEntry{key: "driver", value: opts.ccwName})
	}

	if opts.dev.multifunction {
		entries = append(entries, cfgEntry{key: "multifunction", value: "on"})
	}

	return entries
}

type qemuSerialOpts struct {
	dev              qemuDevOpts
	charDevName      string
	ringbufSizeBytes int
}

func qemuSerial(opts *qemuSerialOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "virtio-serial-pci",
		ccwName: "virtio-serial-ccw",
	}

	return []cfgSection{{
		name:    `device "dev-qemu_serial"`,
		comment: "Virtual serial bus",
		entries: qemuDeviceEntries(&entriesOpts),
	}, {
		// Ring buffer used by the incus agent to report (write) its status to. Incus server will read
		// its content via QMP using "ringbuf-read" command.
		name:    fmt.Sprintf(`chardev "%s"`, opts.charDevName),
		comment: "Serial identifier",
		entries: []cfgEntry{
			{key: "backend", value: "ringbuf"},
			{key: "size", value: fmt.Sprintf("%dB", opts.ringbufSizeBytes)}},
	}, {
		// QEMU serial device connected to the above ring buffer.
		name: `device "qemu_serial"`,
		entries: []cfgEntry{
			{key: "driver", value: "virtserialport"},
			{key: "name", value: "org.linuxcontainers.incus"},
			{key: "chardev", value: opts.charDevName},
			{key: "bus", value: "dev-qemu_serial.0"},
		},
	}, {
		// Legacy QEMU serial device, not connected to any ring buffer. Its purpose is to
		// create a symlink in /dev/virtio-ports/, triggering a udev rule to start incus-agent.
		// This is necessary for backward compatibility with virtual machines lacking the
		// updated incus-agent-loader package, which includes updated udev rules and a systemd unit.
		name: `device "qemu_serial_legacy"`,
		entries: []cfgEntry{
			{key: "driver", value: "virtserialport"},
			{key: "name", value: "org.linuxcontainers.lxd"},
			{key: "bus", value: "dev-qemu_serial.0"},
		},
	}, {
		name:    `chardev "qemu_spice-chardev"`,
		comment: "Spice agent",
		entries: []cfgEntry{
			{key: "backend", value: "spicevmc"},
			{key: "name", value: "vdagent"},
		},
	}, {
		name: `device "qemu_spice"`,
		entries: []cfgEntry{
			{key: "driver", value: "virtserialport"},
			{key: "name", value: "com.redhat.spice.0"},
			{key: "chardev", value: "qemu_spice-chardev"},
			{key: "bus", value: "dev-qemu_serial.0"},
		},
	}, {
		name:    `chardev "qemu_spicedir-chardev"`,
		comment: "Spice folder",
		entries: []cfgEntry{
			{key: "backend", value: "spiceport"},
			{key: "name", value: "org.spice-space.webdav.0"},
		},
	}, {
		name: `device "qemu_spicedir"`,
		entries: []cfgEntry{
			{key: "driver", value: "virtserialport"},
			{key: "name", value: "org.spice-space.webdav.0"},
			{key: "chardev", value: "qemu_spicedir-chardev"},
			{key: "bus", value: "dev-qemu_serial.0"},
		},
	}}
}

type qemuPCIeOpts struct {
	portName      string
	index         int
	devAddr       string
	multifunction bool
}

func qemuPCIe(opts *qemuPCIeOpts) []cfgSection {
	entries := []cfgEntry{
		{key: "driver", value: "pcie-root-port"},
		{key: "bus", value: "pcie.0"},
		{key: "addr", value: opts.devAddr},
		{key: "chassis", value: fmt.Sprintf("%d", opts.index)},
	}

	if opts.multifunction {
		entries = append(entries, cfgEntry{key: "multifunction", value: "on"})
	}

	return []cfgSection{{
		name:    fmt.Sprintf(`device "%s"`, opts.portName),
		entries: entries,
	}}
}

func qemuSCSI(opts *qemuDevOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-scsi-pci",
		ccwName: "virtio-scsi-ccw",
	}

	return []cfgSection{{
		name:    `device "qemu_scsi"`,
		comment: "SCSI controller",
		entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuBalloon(opts *qemuDevOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-balloon-pci",
		ccwName: "virtio-balloon-ccw",
	}

	return []cfgSection{{
		name:    `device "qemu_balloon"`,
		comment: "Balloon driver",
		entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuRNG(opts *qemuDevOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-rng-pci",
		ccwName: "virtio-rng-ccw",
	}

	return []cfgSection{{
		name:    `object "qemu_rng"`,
		comment: "Random number generator",
		entries: []cfgEntry{
			{key: "qom-type", value: "rng-random"},
			{key: "filename", value: "/dev/urandom"},
		},
	}, {
		name: `device "dev-qemu_rng"`,
		entries: append(qemuDeviceEntries(&entriesOpts),
			cfgEntry{key: "rng", value: "qemu_rng"}),
	}}
}

type qemuSevOpts struct {
	cbitpos         int
	reducedPhysBits int
	policy          string
	dhCertFD        string
	sessionDataFD   string
}

func qemuSEV(opts *qemuSevOpts) []cfgSection {
	entries := []cfgEntry{
		{key: "qom-type", value: "sev-guest"},
		{key: "cbitpos", value: fmt.Sprintf("%d", opts.cbitpos)},
		{key: "reduced-phys-bits", value: fmt.Sprintf("%d", opts.reducedPhysBits)},
		{key: "policy", value: opts.policy},
	}

	if opts.dhCertFD != "" && opts.sessionDataFD != "" {
		entries = append(entries, cfgEntry{key: "dh-cert-file", value: opts.dhCertFD}, cfgEntry{key: "session-file", value: opts.sessionDataFD})
	}

	return []cfgSection{{
		name:    `object "sev0"`,
		comment: "Secure Encrypted Virtualization",
		entries: entries,
	}}
}

type qemuVsockOpts struct {
	dev     qemuDevOpts
	vsockFD int
	vsockID uint32
}

func qemuVsock(opts *qemuVsockOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "vhost-vsock-pci",
		ccwName: "vhost-vsock-ccw",
	}

	return []cfgSection{{
		name:    `device "qemu_vsock"`,
		comment: "Vsock",
		entries: append(qemuDeviceEntries(&entriesOpts),
			cfgEntry{key: "guest-cid", value: fmt.Sprintf("%d", opts.vsockID)},
			cfgEntry{key: "vhostfd", value: fmt.Sprintf("%d", opts.vsockFD)}),
	}}
}

type qemuGpuOpts struct {
	dev          qemuDevOpts
	architecture int
}

func qemuGPU(opts *qemuGpuOpts) []cfgSection {
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

	return []cfgSection{{
		name:    `device "qemu_gpu"`,
		comment: "GPU",
		entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuKeyboard(opts *qemuDevOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-keyboard-pci",
		ccwName: "virtio-keyboard-ccw",
	}

	return []cfgSection{{
		name:    `device "qemu_keyboard"`,
		comment: "Input",
		entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuTablet(opts *qemuDevOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-tablet-pci",
		ccwName: "virtio-tablet-ccw",
	}

	return []cfgSection{{
		name:    `device "qemu_tablet"`,
		comment: "Input",
		entries: qemuDeviceEntries(&entriesOpts),
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

func qemuCPUNumaHostNode(opts *qemuCPUOpts, index int) []cfgSection {
	entries := []cfgEntry{}

	if opts.hugepages != "" {
		entries = append(entries, []cfgEntry{
			{key: "qom-type", value: "memory-backend-file"},
			{key: "mem-path", value: opts.hugepages},
			{key: "prealloc", value: "on"},
			{key: "discard-data", value: "on"},
		}...)
	} else {
		entries = append(entries, cfgEntry{key: "qom-type", value: "memory-backend-memfd"})
	}

	entries = append(entries, cfgEntry{key: "size", value: fmt.Sprintf("%dM", opts.memory)})

	return []cfgSection{{
		name:    fmt.Sprintf("object \"mem%d\"", index),
		entries: entries,
	}, {
		name: "numa",
		entries: []cfgEntry{
			{key: "type", value: "node"},
			{key: "nodeid", value: fmt.Sprintf("%d", index)},
			{key: "memdev", value: fmt.Sprintf("mem%d", index)},
		},
	}}
}

func qemuCPU(opts *qemuCPUOpts, pinning bool) []cfgSection {
	entries := []cfgEntry{
		{key: "cpus", value: fmt.Sprintf("%d", opts.cpuCount)},
	}

	if pinning {
		entries = append(entries, cfgEntry{
			key: "sockets", value: fmt.Sprintf("%d", opts.cpuSockets),
		}, cfgEntry{
			key: "cores", value: fmt.Sprintf("%d", opts.cpuCores),
		}, cfgEntry{
			key: "threads", value: fmt.Sprintf("%d", opts.cpuThreads),
		})
	} else {
		cpu, err := resources.GetCPU()
		if err != nil {
			return nil
		}

		// Cap the max number of CPUs to 64 unless directly assigned more.
		max := 64
		if int(cpu.Total) < max {
			max = int(cpu.Total)
		} else if opts.cpuRequested > max {
			max = opts.cpuRequested
		} else if opts.cpuCount > max {
			max = opts.cpuCount
		}

		entries = append(entries, cfgEntry{
			key: "maxcpus", value: fmt.Sprintf("%d", max),
		})
	}

	sections := []cfgSection{{
		name:    "smp-opts",
		comment: "CPU",
		entries: entries,
	}}

	if opts.architecture != "x86_64" {
		return sections
	}

	share := cfgEntry{key: "share", value: "on"}

	if len(opts.cpuNumaHostNodes) == 0 {
		// Add one mem and one numa sections with index 0.
		numaHostNode := qemuCPUNumaHostNode(opts, 0)

		// Unconditionally append "share = "on" to the [object "mem0"] section
		numaHostNode[0].entries = append(numaHostNode[0].entries, share)

		// If NUMA memory restrictions are set, apply them.
		if len(opts.memoryHostNodes) > 0 {
			extraMemEntries := []cfgEntry{{key: "policy", value: "bind"}}

			for index, element := range opts.memoryHostNodes {
				var hostNodesKey string
				if opts.qemuMemObjectFormat == "indexed" {
					hostNodesKey = fmt.Sprintf("host-nodes.%d", index)
				} else {
					hostNodesKey = "host-nodes"
				}

				hostNode := cfgEntry{key: hostNodesKey, value: fmt.Sprintf("%d", element)}
				extraMemEntries = append(extraMemEntries, hostNode)
			}

			// Append the extra entries to the [object "mem{{idx}}"] section.
			numaHostNode[0].entries = append(numaHostNode[0].entries, extraMemEntries...)
		}

		return append(sections, numaHostNode...)
	}

	for index, element := range opts.cpuNumaHostNodes {
		numaHostNode := qemuCPUNumaHostNode(opts, index)

		extraMemEntries := []cfgEntry{{key: "policy", value: "bind"}}

		if opts.hugepages != "" {
			// append share = "on" only if hugepages is set
			extraMemEntries = append(extraMemEntries, share)
		}

		var hostNodesKey string
		if opts.qemuMemObjectFormat == "indexed" {
			hostNodesKey = "host-nodes.0"
		} else {
			hostNodesKey = "host-nodes"
		}

		hostNode := cfgEntry{key: hostNodesKey, value: fmt.Sprintf("%d", element)}
		extraMemEntries = append(extraMemEntries, hostNode)
		// append the extra entries to the [object "mem{{idx}}"] section
		numaHostNode[0].entries = append(numaHostNode[0].entries, extraMemEntries...)
		sections = append(sections, numaHostNode...)
	}

	for _, numa := range opts.cpuNumaMapping {
		sections = append(sections, cfgSection{
			name: "numa",
			entries: []cfgEntry{
				{key: "type", value: "cpu"},
				{key: "node-id", value: fmt.Sprintf("%d", numa.node)},
				{key: "socket-id", value: fmt.Sprintf("%d", numa.socket)},
				{key: "core-id", value: fmt.Sprintf("%d", numa.core)},
				{key: "thread-id", value: fmt.Sprintf("%d", numa.thread)},
			},
		})
	}

	return sections
}

type qemuControlSocketOpts struct {
	path string
}

func qemuControlSocket(opts *qemuControlSocketOpts) []cfgSection {
	return []cfgSection{{
		name:    `chardev "monitor"`,
		comment: "Qemu control",
		entries: []cfgEntry{
			{key: "backend", value: "socket"},
			{key: "path", value: opts.path},
			{key: "server", value: "on"},
			{key: "wait", value: "off"},
		},
	}, {
		name: "mon",
		entries: []cfgEntry{
			{key: "chardev", value: "monitor"},
			{key: "mode", value: "control"},
		},
	}}
}

type qemuConsoleOpts struct {
	path string
}

func qemuConsole(opts *qemuConsoleOpts) []cfgSection {
	return []cfgSection{{
		name:    `chardev "console"`,
		comment: "Console",
		entries: []cfgEntry{
			{key: "backend", value: "socket"},
			{key: "path", value: opts.path},
			{key: "server", value: "on"},
			{key: "wait", value: "off"},
		},
	}}
}

type qemuDriveFirmwareOpts struct {
	roPath    string
	nvramPath string
}

func qemuDriveFirmware(opts *qemuDriveFirmwareOpts) []cfgSection {
	return []cfgSection{{
		name:    "drive",
		comment: "Firmware (read only)",
		entries: []cfgEntry{
			{key: "file", value: opts.roPath},
			{key: "if", value: "pflash"},
			{key: "format", value: "raw"},
			{key: "unit", value: "0"},
			{key: "readonly", value: "on"},
		},
	}, {
		name:    "drive",
		comment: "Firmware settings (writable)",
		entries: []cfgEntry{
			{key: "file", value: opts.nvramPath},
			{key: "if", value: "pflash"},
			{key: "format", value: "raw"},
			{key: "unit", value: "1"},
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

func qemuHostDrive(opts *qemuHostDriveOpts) []cfgSection {
	var extraDeviceEntries []cfgEntry
	var driveSection cfgSection
	deviceOpts := qemuDevEntriesOpts{dev: opts.dev}

	if opts.protocol == "9p" {
		var readonly string
		if opts.readonly {
			readonly = "on"
		} else {
			readonly = "off"
		}

		driveSection = cfgSection{
			name:    fmt.Sprintf(`fsdev "%s"`, opts.name),
			comment: opts.comment,
			entries: []cfgEntry{
				{key: "fsdriver", value: opts.fsdriver},
				{key: "sock_fd", value: opts.sockFd},
				{key: "security_model", value: opts.securityModel},
				{key: "readonly", value: readonly},
				{key: "path", value: opts.path},
			},
		}

		deviceOpts.pciName = "virtio-9p-pci"
		deviceOpts.ccwName = "virtio-9p-ccw"

		extraDeviceEntries = []cfgEntry{
			{key: "mount_tag", value: opts.mountTag},
			{key: "fsdev", value: opts.name},
		}
	} else if opts.protocol == "virtio-fs" {
		driveSection = cfgSection{
			name:    fmt.Sprintf(`chardev "%s"`, opts.name),
			comment: opts.comment,
			entries: []cfgEntry{
				{key: "backend", value: "socket"},
				{key: "path", value: opts.path},
			},
		}

		deviceOpts.pciName = "vhost-user-fs-pci"
		deviceOpts.ccwName = "vhost-user-fs-ccw"

		extraDeviceEntries = []cfgEntry{
			{key: "tag", value: opts.mountTag},
			{key: "chardev", value: opts.name},
		}
	} else {
		return []cfgSection{}
	}

	return []cfgSection{
		driveSection,
		{
			name:    fmt.Sprintf(`device "dev-%s%s-%s"`, opts.name, opts.nameSuffix, opts.protocol),
			entries: append(qemuDeviceEntries(&deviceOpts), extraDeviceEntries...),
		},
	}
}

type qemuDriveConfigOpts struct {
	name     string
	dev      qemuDevOpts
	protocol string
	path     string
}

func qemuDriveConfig(opts *qemuDriveConfigOpts) []cfgSection {
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
	proxyFD  int
	readonly bool
}

func qemuDriveDir(opts *qemuDriveDirOpts) []cfgSection {
	return qemuHostDrive(&qemuHostDriveOpts{
		dev:      opts.dev,
		name:     fmt.Sprintf("incus_%s", opts.devName),
		comment:  fmt.Sprintf("%s drive (%s)", opts.devName, opts.protocol),
		mountTag: opts.mountTag,
		protocol: opts.protocol,
		fsdriver: "proxy",
		readonly: opts.readonly,
		path:     opts.path,
		sockFd:   fmt.Sprintf("%d", opts.proxyFD),
	})
}

type qemuPCIPhysicalOpts struct {
	dev         qemuDevOpts
	devName     string
	pciSlotName string
}

func qemuPCIPhysical(opts *qemuPCIPhysicalOpts) []cfgSection {
	deviceOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "vfio-pci",
		ccwName: "vfio-ccw",
	}

	entries := append(qemuDeviceEntries(&deviceOpts), []cfgEntry{
		{key: "host", value: opts.pciSlotName},
	}...)

	return []cfgSection{{
		name:    fmt.Sprintf(`device "%s%s"`, qemuDeviceIDPrefix, opts.devName),
		comment: fmt.Sprintf(`PCI card ("%s" device)`, opts.devName),
		entries: entries,
	}}
}

type qemuGPUDevPhysicalOpts struct {
	dev         qemuDevOpts
	devName     string
	pciSlotName string
	vgpu        string
	vga         bool
}

func qemuGPUDevPhysical(opts *qemuGPUDevPhysicalOpts) []cfgSection {
	deviceOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "vfio-pci",
		ccwName: "vfio-ccw",
	}

	entries := qemuDeviceEntries(&deviceOpts)

	if opts.vgpu != "" {
		sysfsdev := fmt.Sprintf("/sys/bus/mdev/devices/%s", opts.vgpu)
		entries = append(entries, cfgEntry{key: "sysfsdev", value: sysfsdev})
	} else {
		entries = append(entries, cfgEntry{key: "host", value: opts.pciSlotName})
	}

	if opts.vga {
		entries = append(entries, cfgEntry{key: "x-vga", value: "on"})
	}

	return []cfgSection{{
		name:    fmt.Sprintf(`device "%s%s"`, qemuDeviceIDPrefix, opts.devName),
		comment: fmt.Sprintf(`GPU card ("%s" device)`, opts.devName),
		entries: entries,
	}}
}

type qemuUSBOpts struct {
	devBus        string
	devAddr       string
	multifunction bool
	ports         int
}

func qemuUSB(opts *qemuUSBOpts) []cfgSection {
	deviceOpts := qemuDevEntriesOpts{
		dev: qemuDevOpts{
			busName:       "pci",
			devAddr:       opts.devAddr,
			devBus:        opts.devBus,
			multifunction: opts.multifunction,
		},
		pciName: "qemu-xhci",
	}

	sections := []cfgSection{{
		name:    `device "qemu_usb"`,
		comment: "USB controller",
		entries: append(qemuDeviceEntries(&deviceOpts), []cfgEntry{
			{key: "p2", value: fmt.Sprintf("%d", opts.ports)},
			{key: "p3", value: fmt.Sprintf("%d", opts.ports)},
		}...),
	}}

	for i := 1; i <= 3; i++ {
		chardev := fmt.Sprintf("qemu_spice-usb-chardev%d", i)
		sections = append(sections, []cfgSection{{
			name: fmt.Sprintf(`chardev "%s"`, chardev),
			entries: []cfgEntry{
				{key: "backend", value: "spicevmc"},
				{key: "name", value: "usbredir"},
			},
		}, {
			name: fmt.Sprintf(`device "qemu_spice-usb%d"`, i),
			entries: []cfgEntry{
				{key: "driver", value: "usb-redir"},
				{key: "chardev", value: chardev},
			},
		}}...)
	}

	return sections
}

type qemuTPMOpts struct {
	devName string
	path    string
}

func qemuTPM(opts *qemuTPMOpts) []cfgSection {
	chardev := fmt.Sprintf("qemu_tpm-chardev_%s", opts.devName)
	tpmdev := fmt.Sprintf("qemu_tpm-tpmdev_%s", opts.devName)

	return []cfgSection{{
		name: fmt.Sprintf(`chardev "%s"`, chardev),
		entries: []cfgEntry{
			{key: "backend", value: "socket"},
			{key: "path", value: opts.path},
		},
	}, {
		name: fmt.Sprintf(`tpmdev "%s"`, tpmdev),
		entries: []cfgEntry{
			{key: "type", value: "emulator"},
			{key: "chardev", value: chardev},
		},
	}, {
		name: fmt.Sprintf(`device "%s%s"`, qemuDeviceIDPrefix, opts.devName),
		entries: []cfgEntry{
			{key: "driver", value: "tpm-crb"},
			{key: "tpmdev", value: tpmdev},
		},
	}}
}

type qemuVmgenIDOpts struct {
	guid string
}

func qemuVmgen(opts *qemuVmgenIDOpts) []cfgSection {
	return []cfgSection{{
		name:    `device "vmgenid0"`,
		comment: "VM Generation ID",
		entries: []cfgEntry{
			{key: "driver", value: "vmgenid"},
			{key: "guid", value: opts.guid},
		},
	}}
}
