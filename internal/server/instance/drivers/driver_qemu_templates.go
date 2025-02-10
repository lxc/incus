package drivers

import (
	"fmt"
	"strings"

	"github.com/lxc/incus/v6/internal/server/instance/drivers/cfg"
	"github.com/lxc/incus/v6/internal/server/resources"
	"github.com/lxc/incus/v6/shared/osarch"
)

func qemuStringifyCfg(conf ...cfg.Section) *strings.Builder {
	sb := &strings.Builder{}

	for _, section := range conf {
		if section.Comment != "" {
			sb.WriteString(fmt.Sprintf("# %s\n", section.Comment))
		}

		sb.WriteString(fmt.Sprintf("[%s]\n", section.Name))

		for _, entry := range section.Entries {
			value := entry.Value
			if value != "" {
				sb.WriteString(fmt.Sprintf("%s = \"%s\"\n", entry.Key, value))
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

	sections := []cfg.Section{{
		Name:    "machine",
		Comment: "Machine",
		Entries: []cfg.Entry{
			{Key: "graphics", Value: "off"},
			{Key: "type", Value: machineType},
			{Key: "gic-version", Value: gicVersion},
			{Key: "cap-large-decr", Value: capLargeDecr},
			{Key: "accel", Value: "kvm"},
			{Key: "usb", Value: "off"},
		},
	}}

	if opts.architecture == osarch.ARCH_64BIT_INTEL_X86 {
		sections = append(sections, []cfg.Section{{
			Name: "global",
			Entries: []cfg.Entry{
				{Key: "driver", Value: "ICH9-LPC"},
				{Key: "property", Value: "disable_s3"},
				{Key: "value", Value: "1"},
			},
		}, {
			Name: "global",
			Entries: []cfg.Entry{
				{Key: "driver", Value: "ICH9-LPC"},
				{Key: "property", Value: "disable_s4"},
				{Key: "value", Value: "1"},
			},
		}}...)
	}

	return append(
		sections,
		cfg.Section{
			Name:    "boot-opts",
			Entries: []cfg.Entry{{Key: "strict", Value: "on"}},
		})
}

type qemuMemoryOpts struct {
	memSizeMB int64
}

func qemuMemory(opts *qemuMemoryOpts) []cfg.Section {
	return []cfg.Section{{
		Name:    "memory",
		Comment: "Memory",
		Entries: []cfg.Entry{{Key: "size", Value: fmt.Sprintf("%dM", opts.memSizeMB)}},
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

func qemuDeviceEntries(opts *qemuDevEntriesOpts) []cfg.Entry {
	entries := []cfg.Entry{}

	if opts.dev.busName == "pci" || opts.dev.busName == "pcie" {
		entries = append(entries, []cfg.Entry{
			{Key: "driver", Value: opts.pciName},
			{Key: "bus", Value: opts.dev.devBus},
			{Key: "addr", Value: opts.dev.devAddr},
		}...)
	} else if opts.dev.busName == "ccw" {
		entries = append(entries, cfg.Entry{Key: "driver", Value: opts.ccwName})
	}

	if opts.dev.multifunction {
		entries = append(entries, cfg.Entry{Key: "multifunction", Value: "on"})
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
		Entries: []cfg.Entry{
			{Key: "backend", Value: "ringbuf"},
			{Key: "size", Value: fmt.Sprintf("%dB", opts.ringbufSizeBytes)}},
	}, {
		// QEMU serial device connected to the above ring buffer.
		Name: `device "qemu_serial"`,
		Entries: []cfg.Entry{
			{Key: "driver", Value: "virtserialport"},
			{Key: "name", Value: "org.linuxcontainers.incus"},
			{Key: "chardev", Value: opts.charDevName},
			{Key: "bus", Value: "dev-qemu_serial.0"},
		},
	}, {
		// Legacy QEMU serial device, not connected to any ring buffer. Its purpose is to
		// create a symlink in /dev/virtio-ports/, triggering a udev rule to start incus-agent.
		// This is necessary for backward compatibility with virtual machines lacking the
		// updated incus-agent-loader package, which includes updated udev rules and a systemd unit.
		Name: `device "qemu_serial_legacy"`,
		Entries: []cfg.Entry{
			{Key: "driver", Value: "virtserialport"},
			{Key: "name", Value: "org.linuxcontainers.lxd"},
			{Key: "bus", Value: "dev-qemu_serial.0"},
		},
	}, {
		Name:    `chardev "qemu_spice-chardev"`,
		Comment: "Spice agent",
		Entries: []cfg.Entry{
			{Key: "backend", Value: "spicevmc"},
			{Key: "name", Value: "vdagent"},
		},
	}, {
		Name: `device "qemu_spice"`,
		Entries: []cfg.Entry{
			{Key: "driver", Value: "virtserialport"},
			{Key: "name", Value: "com.redhat.spice.0"},
			{Key: "chardev", Value: "qemu_spice-chardev"},
			{Key: "bus", Value: "dev-qemu_serial.0"},
		},
	}, {
		Name:    `chardev "qemu_spicedir-chardev"`,
		Comment: "Spice folder",
		Entries: []cfg.Entry{
			{Key: "backend", Value: "spiceport"},
			{Key: "name", Value: "org.spice-space.webdav.0"},
		},
	}, {
		Name: `device "qemu_spicedir"`,
		Entries: []cfg.Entry{
			{Key: "driver", Value: "virtserialport"},
			{Key: "name", Value: "org.spice-space.webdav.0"},
			{Key: "chardev", Value: "qemu_spicedir-chardev"},
			{Key: "bus", Value: "dev-qemu_serial.0"},
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
	entries := []cfg.Entry{
		{Key: "driver", Value: "pcie-root-port"},
		{Key: "bus", Value: "pcie.0"},
		{Key: "addr", Value: opts.devAddr},
		{Key: "chassis", Value: fmt.Sprintf("%d", opts.index)},
	}

	if opts.multifunction {
		entries = append(entries, cfg.Entry{Key: "multifunction", Value: "on"})
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
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-rng-pci",
		ccwName: "virtio-rng-ccw",
	}

	return []cfg.Section{{
		Name:    `object "qemu_rng"`,
		Comment: "Random number generator",
		Entries: []cfg.Entry{
			{Key: "qom-type", Value: "rng-random"},
			{Key: "filename", Value: "/dev/urandom"},
		},
	}, {
		Name: `device "dev-qemu_rng"`,
		Entries: append(qemuDeviceEntries(&entriesOpts),
			cfg.Entry{Key: "rng", Value: "qemu_rng"}),
	}}
}

func qemuCoreInfo() []cfg.Section {
	return []cfg.Section{{
		Name:    `device "qemu_vmcoreinfo"`,
		Comment: "VM core info driver",
		Entries: []cfg.Entry{
			{Key: "driver", Value: "vmcoreinfo"},
		},
	}}
}

func qemuIOMMU(opts *qemuDevOpts) []cfg.Section {
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
	entries := []cfg.Entry{
		{Key: "qom-type", Value: "sev-guest"},
		{Key: "cbitpos", Value: fmt.Sprintf("%d", opts.cbitpos)},
		{Key: "reduced-phys-bits", Value: fmt.Sprintf("%d", opts.reducedPhysBits)},
		{Key: "policy", Value: opts.policy},
	}

	if opts.dhCertFD != "" && opts.sessionDataFD != "" {
		entries = append(entries, cfg.Entry{Key: "dh-cert-file", Value: opts.dhCertFD}, cfg.Entry{Key: "session-file", Value: opts.sessionDataFD})
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
	entriesOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "vhost-vsock-pci",
		ccwName: "vhost-vsock-ccw",
	}

	return []cfg.Section{{
		Name:    `device "qemu_vsock"`,
		Comment: "Vsock",
		Entries: append(qemuDeviceEntries(&entriesOpts),
			cfg.Entry{Key: "guest-cid", Value: fmt.Sprintf("%d", opts.vsockID)},
			cfg.Entry{Key: "vhostfd", Value: fmt.Sprintf("%d", opts.vsockFD)}),
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
	entries := []cfg.Entry{}

	if opts.hugepages != "" {
		entries = append(entries, []cfg.Entry{
			{Key: "qom-type", Value: "memory-backend-file"},
			{Key: "mem-path", Value: opts.hugepages},
			{Key: "prealloc", Value: "on"},
			{Key: "discard-data", Value: "on"},
		}...)
	} else {
		entries = append(entries, cfg.Entry{Key: "qom-type", Value: "memory-backend-memfd"})
	}

	entries = append(entries, cfg.Entry{Key: "size", Value: fmt.Sprintf("%dM", opts.memory)})

	return []cfg.Section{{
		Name:    fmt.Sprintf("object \"mem%d\"", index),
		Entries: entries,
	}, {
		Name: "numa",
		Entries: []cfg.Entry{
			{Key: "type", Value: "node"},
			{Key: "nodeid", Value: fmt.Sprintf("%d", index)},
			{Key: "memdev", Value: fmt.Sprintf("mem%d", index)},
		},
	}}
}

func qemuCPU(opts *qemuCPUOpts, pinning bool) []cfg.Section {
	entries := []cfg.Entry{
		{Key: "cpus", Value: fmt.Sprintf("%d", opts.cpuCount)},
	}

	if pinning {
		entries = append(entries, cfg.Entry{
			Key: "sockets", Value: fmt.Sprintf("%d", opts.cpuSockets),
		}, cfg.Entry{
			Key: "cores", Value: fmt.Sprintf("%d", opts.cpuCores),
		}, cfg.Entry{
			Key: "threads", Value: fmt.Sprintf("%d", opts.cpuThreads),
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

		entries = append(entries, cfg.Entry{
			Key: "maxcpus", Value: fmt.Sprintf("%d", max),
		})
	}

	sections := []cfg.Section{{
		Name:    "smp-opts",
		Comment: "CPU",
		Entries: entries,
	}}

	if opts.architecture != "x86_64" {
		return sections
	}

	share := cfg.Entry{Key: "share", Value: "on"}

	if len(opts.cpuNumaHostNodes) == 0 {
		// Add one mem and one numa sections with index 0.
		numaHostNode := qemuCPUNumaHostNode(opts, 0)

		// Unconditionally append "share = "on" to the [object "mem0"] section
		numaHostNode[0].Entries = append(numaHostNode[0].Entries, share)

		// If NUMA memory restrictions are set, apply them.
		if len(opts.memoryHostNodes) > 0 {
			extraMemEntries := []cfg.Entry{{Key: "policy", Value: "bind"}}

			for index, element := range opts.memoryHostNodes {
				var hostNodesKey string
				if opts.qemuMemObjectFormat == "indexed" {
					hostNodesKey = fmt.Sprintf("host-nodes.%d", index)
				} else {
					hostNodesKey = "host-nodes"
				}

				hostNode := cfg.Entry{Key: hostNodesKey, Value: fmt.Sprintf("%d", element)}
				extraMemEntries = append(extraMemEntries, hostNode)
			}

			// Append the extra entries to the [object "mem{{idx}}"] section.
			numaHostNode[0].Entries = append(numaHostNode[0].Entries, extraMemEntries...)
		}

		return append(sections, numaHostNode...)
	}

	for index, element := range opts.cpuNumaHostNodes {
		numaHostNode := qemuCPUNumaHostNode(opts, index)

		extraMemEntries := []cfg.Entry{{Key: "policy", Value: "bind"}}

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

		hostNode := cfg.Entry{Key: hostNodesKey, Value: fmt.Sprintf("%d", element)}
		extraMemEntries = append(extraMemEntries, hostNode)
		// append the extra entries to the [object "mem{{idx}}"] section
		numaHostNode[0].Entries = append(numaHostNode[0].Entries, extraMemEntries...)
		sections = append(sections, numaHostNode...)
	}

	for _, numa := range opts.cpuNumaMapping {
		sections = append(sections, cfg.Section{
			Name: "numa",
			Entries: []cfg.Entry{
				{Key: "type", Value: "cpu"},
				{Key: "node-id", Value: fmt.Sprintf("%d", numa.node)},
				{Key: "socket-id", Value: fmt.Sprintf("%d", numa.socket)},
				{Key: "core-id", Value: fmt.Sprintf("%d", numa.core)},
				{Key: "thread-id", Value: fmt.Sprintf("%d", numa.thread)},
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
		Entries: []cfg.Entry{
			{Key: "backend", Value: "socket"},
			{Key: "path", Value: opts.path},
			{Key: "server", Value: "on"},
			{Key: "wait", Value: "off"},
		},
	}, {
		Name: "mon",
		Entries: []cfg.Entry{
			{Key: "chardev", Value: "monitor"},
			{Key: "mode", Value: "control"},
		},
	}}
}

func qemuConsole() []cfg.Section {
	return []cfg.Section{{
		Name:    `chardev "console"`,
		Comment: "Console",
		Entries: []cfg.Entry{
			{Key: "backend", Value: "ringbuf"},
			{Key: "size", Value: "1048576"},
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
		Entries: []cfg.Entry{
			{Key: "file", Value: opts.roPath},
			{Key: "if", Value: "pflash"},
			{Key: "format", Value: "raw"},
			{Key: "unit", Value: "0"},
			{Key: "readonly", Value: "on"},
		},
	}, {
		Name:    "drive",
		Comment: "Firmware settings (writable)",
		Entries: []cfg.Entry{
			{Key: "file", Value: opts.nvramPath},
			{Key: "if", Value: "pflash"},
			{Key: "format", Value: "raw"},
			{Key: "unit", Value: "1"},
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
	var extraDeviceEntries []cfg.Entry
	var driveSection cfg.Section
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
			Entries: []cfg.Entry{
				{Key: "fsdriver", Value: opts.fsdriver},
				{Key: "sock_fd", Value: opts.sockFd},
				{Key: "security_model", Value: opts.securityModel},
				{Key: "readonly", Value: readonly},
				{Key: "path", Value: opts.path},
			},
		}

		deviceOpts.pciName = "virtio-9p-pci"
		deviceOpts.ccwName = "virtio-9p-ccw"

		extraDeviceEntries = []cfg.Entry{
			{Key: "mount_tag", Value: opts.mountTag},
			{Key: "fsdev", Value: opts.name},
		}
	} else if opts.protocol == "virtio-fs" {
		driveSection = cfg.Section{
			Name:    fmt.Sprintf(`chardev "%s"`, opts.name),
			Comment: opts.comment,
			Entries: []cfg.Entry{
				{Key: "backend", Value: "socket"},
				{Key: "path", Value: opts.path},
			},
		}

		deviceOpts.pciName = "vhost-user-fs-pci"
		deviceOpts.ccwName = "vhost-user-fs-ccw"

		extraDeviceEntries = []cfg.Entry{
			{Key: "tag", Value: opts.mountTag},
			{Key: "chardev", Value: opts.name},
		}
	} else {
		return []cfg.Section{}
	}

	return []cfg.Section{
		driveSection,
		{
			Name:    fmt.Sprintf(`device "dev-%s%s-%s"`, opts.name, opts.nameSuffix, opts.protocol),
			Entries: append(qemuDeviceEntries(&deviceOpts), extraDeviceEntries...),
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

	entries := append(qemuDeviceEntries(&deviceOpts), []cfg.Entry{
		{Key: "host", Value: opts.pciSlotName},
	}...)

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
		entries = append(entries, cfg.Entry{Key: "sysfsdev", Value: sysfsdev})
	} else {
		entries = append(entries, cfg.Entry{Key: "host", Value: opts.pciSlotName})
	}

	if opts.vga {
		entries = append(entries, cfg.Entry{Key: "x-vga", Value: "on"})
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

	sections := []cfg.Section{{
		Name:    `device "qemu_usb"`,
		Comment: "USB controller",
		Entries: append(qemuDeviceEntries(&deviceOpts), []cfg.Entry{
			{Key: "p2", Value: fmt.Sprintf("%d", opts.ports)},
			{Key: "p3", Value: fmt.Sprintf("%d", opts.ports)},
		}...),
	}}

	for i := 1; i <= 3; i++ {
		chardev := fmt.Sprintf("qemu_spice-usb-chardev%d", i)
		sections = append(sections, []cfg.Section{{
			Name: fmt.Sprintf(`chardev "%s"`, chardev),
			Entries: []cfg.Entry{
				{Key: "backend", Value: "spicevmc"},
				{Key: "name", Value: "usbredir"},
			},
		}, {
			Name: fmt.Sprintf(`device "qemu_spice-usb%d"`, i),
			Entries: []cfg.Entry{
				{Key: "driver", Value: "usb-redir"},
				{Key: "chardev", Value: chardev},
			},
		}}...)
	}

	return sections
}

type qemuTPMOpts struct {
	devName string
	path    string
}

func qemuTPM(opts *qemuTPMOpts) []cfg.Section {
	chardev := fmt.Sprintf("qemu_tpm-chardev_%s", opts.devName)
	tpmdev := fmt.Sprintf("qemu_tpm-tpmdev_%s", opts.devName)

	return []cfg.Section{{
		Name: fmt.Sprintf(`chardev "%s"`, chardev),
		Entries: []cfg.Entry{
			{Key: "backend", Value: "socket"},
			{Key: "path", Value: opts.path},
		},
	}, {
		Name: fmt.Sprintf(`tpmdev "%s"`, tpmdev),
		Entries: []cfg.Entry{
			{Key: "type", Value: "emulator"},
			{Key: "chardev", Value: chardev},
		},
	}, {
		Name: fmt.Sprintf(`device "%s%s"`, qemuDeviceIDPrefix, opts.devName),
		Entries: []cfg.Entry{
			{Key: "driver", Value: "tpm-crb"},
			{Key: "tpmdev", Value: tpmdev},
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
		Entries: []cfg.Entry{
			{Key: "driver", Value: "vmgenid"},
			{Key: "guid", Value: opts.guid},
		},
	}}
}
