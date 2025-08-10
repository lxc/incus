package drivers

import (
	"regexp"
	"strings"
	"testing"

	"github.com/lxc/incus/v6/internal/server/instance/drivers/cfg"
	"github.com/lxc/incus/v6/shared/osarch"
)

func TestQemuConfigTemplates(t *testing.T) {
	indent := regexp.MustCompile(`(?m)^[ \t]+`)

	normalize := func(s string) string {
		return strings.TrimSpace(indent.ReplaceAllString(s, "$1"))
	}

	runTest := func(expected string, sections []cfg.Section) {
		t.Run(expected, func(t *testing.T) {
			actual := normalize(qemuStringifyCfgPredictably(sections...).String())
			expected = normalize(expected)
			if actual != expected {
				t.Errorf("Expected: %s. Got: %s", expected, actual)
			}
		})
	}

	t.Run("qemu_base", func(t *testing.T) {
		testCases := []struct {
			opts     qemuBaseOpts
			expected string
		}{{
			qemuBaseOpts{architecture: osarch.ARCH_64BIT_INTEL_X86},
			`# Machine
			[machine]
			accel = "kvm"
			graphics = "off"
			type = "q35"
			usb = "off"

			[global]
			driver = "ICH9-LPC"
			property = "disable_s3"
			value = "1"

			[global]
			driver = "ICH9-LPC"
			property = "disable_s4"
			value = "0"

			[boot-opts]
			strict = "on"`,
		}, {
			qemuBaseOpts{architecture: osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN},
			`# Machine
			[machine]
			accel = "kvm"
			gic-version = "max"
			graphics = "off"
			type = "virt"
			usb = "off"

			[boot-opts]
			strict = "on"`,
		}, {
			qemuBaseOpts{architecture: osarch.ARCH_64BIT_POWERPC_LITTLE_ENDIAN},
			`# Machine
			[machine]
			accel = "kvm"
			cap-large-decr = "off"
			graphics = "off"
			type = "pseries"
			usb = "off"

			[boot-opts]
			strict = "on"`,
		}, {
			qemuBaseOpts{architecture: osarch.ARCH_64BIT_S390_BIG_ENDIAN},
			`# Machine
			[machine]
			accel = "kvm"
			graphics = "off"
			type = "s390-ccw-virtio"
			usb = "off"

			[boot-opts]
			strict = "on"`,
		}}

		for _, tc := range testCases {
			runTest(tc.expected, qemuBase(&tc.opts))
		}
	})

	t.Run("qemu_memory", func(t *testing.T) {
		testCases := []struct {
			opts     qemuMemoryOpts
			expected string
		}{{
			qemuMemoryOpts{4096, 16384},
			`# Memory
			[memory]
			maxmem = "16384M"
			size = "4096M"
			slots = "8"`,
		}, {
			qemuMemoryOpts{8192, 16384},
			`# Memory
			[memory]
			maxmem = "16384M"
			size = "8192M"
			slots = "8"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuMemory(&tc.opts))
		}
	})

	t.Run("qemu_serial", func(t *testing.T) {
		testCases := []struct {
			opts     qemuSerialOpts
			expected string
		}{{
			qemuSerialOpts{qemuDevOpts{"pci", "qemu_pcie0", "00.5", false}, "qemu_serial-chardev", 32},
			`# Virtual serial bus
			[device "dev-qemu_serial"]
			addr = "00.5"
			bus = "qemu_pcie0"
			driver = "virtio-serial-pci"

			# Serial identifier
			[chardev "qemu_serial-chardev"]
			backend = "ringbuf"
			size = "32B"

			[device "qemu_serial"]
			bus = "dev-qemu_serial.0"
			chardev = "qemu_serial-chardev"
			driver = "virtserialport"
			name = "org.linuxcontainers.incus"

			[device "qemu_serial_legacy"]
			bus = "dev-qemu_serial.0"
			driver = "virtserialport"
			name = "org.linuxcontainers.lxd"

			# Spice agent
			[chardev "qemu_spice-chardev"]
			backend = "spicevmc"
			name = "vdagent"

			[device "qemu_spice"]
			bus = "dev-qemu_serial.0"
			chardev = "qemu_spice-chardev"
			driver = "virtserialport"
			name = "com.redhat.spice.0"

			# Spice folder
			[chardev "qemu_spicedir-chardev"]
			backend = "spiceport"
			name = "org.spice-space.webdav.0"

			[device "qemu_spicedir"]
			bus = "dev-qemu_serial.0"
			chardev = "qemu_spicedir-chardev"
			driver = "virtserialport"
			name = "org.spice-space.webdav.0"
			`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuSerial(&tc.opts))
		}
	})

	t.Run("qemu_pcie", func(t *testing.T) {
		testCases := []struct {
			opts     qemuPCIeOpts
			expected string
		}{{
			qemuPCIeOpts{"qemu_pcie0", 0, "1.0", true},
			`[device "qemu_pcie0"]
			addr = "1.0"
			bus = "pcie.0"
			chassis = "0"
			driver = "pcie-root-port"
			multifunction = "on"
			`,
		}, {
			qemuPCIeOpts{"qemu_pcie2", 3, "2.0", false},
			`[device "qemu_pcie2"]
			addr = "2.0"
			bus = "pcie.0"
			chassis = "3"
			driver = "pcie-root-port"
			`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuPCIe(&tc.opts))
		}
	})

	t.Run("qemu_scsi", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDevOpts
			expected string
		}{{
			qemuDevOpts{"pci", "qemu_pcie1", "00.0", false},
			`# SCSI controller
			[device "qemu_scsi"]
			addr = "00.0"
			bus = "qemu_pcie1"
			driver = "virtio-scsi-pci"
			`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie2", "00.2", true},
			`# SCSI controller
			[device "qemu_scsi"]
			driver = "virtio-scsi-ccw"
			multifunction = "on"
			`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuSCSI(&tc.opts))
		}
	})

	t.Run("qemu_balloon", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDevOpts
			expected string
		}{{
			qemuDevOpts{"pcie", "qemu_pcie0", "00.0", true},
			`# Balloon driver
			[device "qemu_balloon"]
			addr = "00.0"
			bus = "qemu_pcie0"
			driver = "virtio-balloon-pci"
			multifunction = "on"
			`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie0", "00.0", false},
			`# Balloon driver
			[device "qemu_balloon"]
			driver = "virtio-balloon-ccw"
			`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuBalloon(&tc.opts))
		}
	})

	t.Run("qemu_rng", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDevOpts
			expected string
		}{{
			qemuDevOpts{"pci", "qemu_pcie0", "00.1", false},
			`# Random number generator
			[object "qemu_rng"]
			filename = "/dev/urandom"
			qom-type = "rng-random"

			[device "dev-qemu_rng"]
			addr = "00.1"
			bus = "qemu_pcie0"
			driver = "virtio-rng-pci"
			rng = "qemu_rng"
			`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie0", "00.1", true},
			`# Random number generator
			[object "qemu_rng"]
			filename = "/dev/urandom"
			qom-type = "rng-random"

			[device "dev-qemu_rng"]
			driver = "virtio-rng-ccw"
			multifunction = "on"
			rng = "qemu_rng"
			`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuRNG(&tc.opts))
		}
	})

	t.Run("qemu_vsock", func(t *testing.T) {
		testCases := []struct {
			opts     qemuVsockOpts
			expected string
		}{{
			qemuVsockOpts{qemuDevOpts{"pcie", "qemu_pcie0", "00.4", true}, 4, 14},
			`# Vsock
			[device "qemu_vsock"]
			addr = "00.4"
			bus = "qemu_pcie0"
			driver = "vhost-vsock-pci"
			guest-cid = "14"
			multifunction = "on"
			vhostfd = "4"
			`,
		}, {
			qemuVsockOpts{qemuDevOpts{"ccw", "qemu_pcie0", "00.4", false}, 4, 3},
			`# Vsock
			[device "qemu_vsock"]
			driver = "vhost-vsock-ccw"
			guest-cid = "3"
			vhostfd = "4"
			`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuVsock(&tc.opts))
		}
	})

	t.Run("qemu_gpu", func(t *testing.T) {
		testCases := []struct {
			opts     qemuGpuOpts
			expected string
		}{{
			qemuGpuOpts{dev: qemuDevOpts{"pci", "qemu_pcie3", "00.0", true}, architecture: osarch.ARCH_64BIT_INTEL_X86},
			`# GPU
			[device "qemu_gpu"]
			addr = "00.0"
			bus = "qemu_pcie3"
			driver = "virtio-vga"
			multifunction = "on"`,
		}, {
			qemuGpuOpts{dev: qemuDevOpts{"pci", "qemu_pci3", "00.1", false}, architecture: osarch.ARCH_UNKNOWN},
			`# GPU
			[device "qemu_gpu"]
			addr = "00.1"
			bus = "qemu_pci3"
			driver = "virtio-gpu-pci"`,
		}, {
			qemuGpuOpts{dev: qemuDevOpts{"ccw", "devBus", "busAddr", true}, architecture: osarch.ARCH_UNKNOWN},
			`# GPU
			[device "qemu_gpu"]
			driver = "virtio-gpu-ccw"
			multifunction = "on"`,
		}, {
			qemuGpuOpts{dev: qemuDevOpts{"ccw", "devBus", "busAddr", false}, architecture: osarch.ARCH_64BIT_INTEL_X86},
			`# GPU
			[device "qemu_gpu"]
			driver = "virtio-gpu-ccw"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuGPU(&tc.opts))
		}
	})

	t.Run("qemu_keyboard", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDevOpts
			expected string
		}{{
			qemuDevOpts{"pci", "qemu_pcie3", "00.0", false},
			`# Input
			[device "qemu_keyboard"]
			addr = "00.0"
			bus = "qemu_pcie3"
			driver = "virtio-keyboard-pci"`,
		}, {
			qemuDevOpts{"pcie", "qemu_pcie3", "00.0", true},
			`# Input
			[device "qemu_keyboard"]
			addr = "00.0"
			bus = "qemu_pcie3"
			driver = "virtio-keyboard-pci"
			multifunction = "on"`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie3", "00.0", false},
			`# Input
			[device "qemu_keyboard"]
			driver = "virtio-keyboard-ccw"`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie3", "00.0", true},
			`# Input
			[device "qemu_keyboard"]
			driver = "virtio-keyboard-ccw"
			multifunction = "on"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuKeyboard(&tc.opts))
		}
	})

	t.Run("qemu_tablet", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDevOpts
			expected string
		}{{
			qemuDevOpts{"pci", "qemu_pcie0", "00.3", true},
			`# Input
			[device "qemu_tablet"]
			addr = "00.3"
			bus = "qemu_pcie0"
			driver = "virtio-tablet-pci"
			multifunction = "on"
			`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie0", "00.3", true},
			`# Input
			[device "qemu_tablet"]
			driver = "virtio-tablet-ccw"
			multifunction = "on"
			`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuTablet(&tc.opts))
		}
	})

	t.Run("qemu_cpu", func(t *testing.T) {
		testCases := []struct {
			opts     qemuCPUOpts
			expected string
		}{{
			qemuCPUOpts{
				architecture:        "x86_64",
				cpuCount:            8,
				cpuSockets:          1,
				cpuCores:            4,
				cpuThreads:          2,
				cpuNumaNodes:        []uint64{},
				cpuNumaMapping:      []qemuNumaEntry{},
				cpuNumaHostNodes:    []uint64{},
				hugepages:           "",
				memory:              7629,
				qemuMemObjectFormat: "repeated",
			},
			`# CPU
			[smp-opts]
			cores = "4"
			cpus = "8"
			sockets = "1"
			threads = "2"

			[object "mem0"]
			qom-type = "memory-backend-memfd"
			share = "on"
			size = "7629M"

			[numa]
			memdev = "mem0"
			nodeid = "0"
			type = "node"`,
		}, {
			qemuCPUOpts{
				architecture: "x86_64",
				cpuCount:     2,
				cpuSockets:   1,
				cpuCores:     2,
				cpuThreads:   1,
				cpuNumaNodes: []uint64{4, 5},
				cpuNumaMapping: []qemuNumaEntry{
					{node: 20, socket: 21, core: 22, thread: 23},
				},
				cpuNumaHostNodes:    []uint64{8, 9, 10},
				hugepages:           "/hugepages/path",
				memory:              12000,
				qemuMemObjectFormat: "indexed",
			},
			`# CPU
			[smp-opts]
			cores = "2"
			cpus = "2"
			sockets = "1"
			threads = "1"

			[object "mem0"]
			discard-data = "on"
			host-nodes.0 = "8"
			mem-path = "/hugepages/path"
			policy = "bind"
			prealloc = "on"
			qom-type = "memory-backend-file"
			share = "on"
			size = "12000M"

			[numa]
			memdev = "mem0"
			nodeid = "0"
			type = "node"

			[object "mem1"]
			discard-data = "on"
			host-nodes.0 = "9"
			mem-path = "/hugepages/path"
			policy = "bind"
			prealloc = "on"
			qom-type = "memory-backend-file"
			share = "on"
			size = "12000M"

			[numa]
			memdev = "mem1"
			nodeid = "1"
			type = "node"

			[object "mem2"]
			discard-data = "on"
			host-nodes.0 = "10"
			mem-path = "/hugepages/path"
			policy = "bind"
			prealloc = "on"
			qom-type = "memory-backend-file"
			share = "on"
			size = "12000M"

			[numa]
			memdev = "mem2"
			nodeid = "2"
			type = "node"

			[numa]
			core-id = "22"
			node-id = "20"
			socket-id = "21"
			thread-id = "23"
			type = "cpu"`,
		}, {
			qemuCPUOpts{
				architecture: "x86_64",
				cpuCount:     2,
				cpuSockets:   1,
				cpuCores:     2,
				cpuThreads:   1,
				cpuNumaNodes: []uint64{4, 5},
				cpuNumaMapping: []qemuNumaEntry{
					{node: 20, socket: 21, core: 22, thread: 23},
				},
				cpuNumaHostNodes:    []uint64{8, 9, 10},
				hugepages:           "",
				memory:              12000,
				qemuMemObjectFormat: "indexed",
			},
			`# CPU
			[smp-opts]
			cores = "2"
			cpus = "2"
			sockets = "1"
			threads = "1"

			[object "mem0"]
			host-nodes.0 = "8"
			policy = "bind"
			qom-type = "memory-backend-memfd"
			size = "12000M"

			[numa]
			memdev = "mem0"
			nodeid = "0"
			type = "node"

			[object "mem1"]
			host-nodes.0 = "9"
			policy = "bind"
			qom-type = "memory-backend-memfd"
			size = "12000M"

			[numa]
			memdev = "mem1"
			nodeid = "1"
			type = "node"

			[object "mem2"]
			host-nodes.0 = "10"
			policy = "bind"
			qom-type = "memory-backend-memfd"
			size = "12000M"

			[numa]
			memdev = "mem2"
			nodeid = "2"
			type = "node"

			[numa]
			core-id = "22"
			node-id = "20"
			socket-id = "21"
			thread-id = "23"
			type = "cpu"`,
		}, {
			qemuCPUOpts{
				architecture: "x86_64",
				cpuCount:     4,
				cpuSockets:   1,
				cpuCores:     4,
				cpuThreads:   1,
				cpuNumaNodes: []uint64{4, 5, 6},
				cpuNumaMapping: []qemuNumaEntry{
					{node: 11, socket: 12, core: 13, thread: 14},
					{node: 20, socket: 21, core: 22, thread: 23},
				},
				cpuNumaHostNodes:    []uint64{8, 9, 10},
				hugepages:           "",
				memory:              12000,
				qemuMemObjectFormat: "repeated",
			},
			`# CPU
			[smp-opts]
			cores = "4"
			cpus = "4"
			sockets = "1"
			threads = "1"

			[object "mem0"]
			host-nodes = "8"
			policy = "bind"
			qom-type = "memory-backend-memfd"
			size = "12000M"

			[numa]
			memdev = "mem0"
			nodeid = "0"
			type = "node"

			[object "mem1"]
			host-nodes = "9"
			policy = "bind"
			qom-type = "memory-backend-memfd"
			size = "12000M"

			[numa]
			memdev = "mem1"
			nodeid = "1"
			type = "node"

			[object "mem2"]
			host-nodes = "10"
			policy = "bind"
			qom-type = "memory-backend-memfd"
			size = "12000M"

			[numa]
			memdev = "mem2"
			nodeid = "2"
			type = "node"

			[numa]
			core-id = "13"
			node-id = "11"
			socket-id = "12"
			thread-id = "14"
			type = "cpu"

			[numa]
			core-id = "22"
			node-id = "20"
			socket-id = "21"
			thread-id = "23"
			type = "cpu"`,
		}, {
			qemuCPUOpts{
				architecture: "arm64",
				cpuCount:     4,
				cpuSockets:   1,
				cpuCores:     4,
				cpuThreads:   1,
				cpuNumaNodes: []uint64{4, 5, 6},
				cpuNumaMapping: []qemuNumaEntry{
					{node: 11, socket: 12, core: 13, thread: 14},
					{node: 20, socket: 21, core: 22, thread: 23},
				},
				cpuNumaHostNodes:    []uint64{8, 9, 10},
				hugepages:           "/hugepages",
				memory:              12000,
				qemuMemObjectFormat: "indexed",
			},
			`# CPU
			[smp-opts]
			cores = "4"
			cpus = "4"
			sockets = "1"
			threads = "1"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuCPU(&tc.opts, true))
		}
	})

	t.Run("qemu_control_socket", func(t *testing.T) {
		testCases := []struct {
			opts     qemuControlSocketOpts
			expected string
		}{{
			qemuControlSocketOpts{"/dev/shm/control-socket"},
			`# Qemu control
			[chardev "monitor"]
			backend = "socket"
			path = "/dev/shm/control-socket"
			server = "on"
			wait = "off"

			[mon]
			chardev = "monitor"
			mode = "control"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuControlSocket(&tc.opts))
		}
	})

	t.Run("qemu_console", func(t *testing.T) {
		testCases := []struct {
			expected string
		}{{
			`# Console
			[chardev "console"]
			backend = "ringbuf"
			size = "1048576"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuConsole())
		}
	})

	t.Run("qemu_drive_firmware", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDriveFirmwareOpts
			expected string
		}{{
			qemuDriveFirmwareOpts{"/tmp/ovmf.fd", "/tmp/settings.fd"},
			`# Firmware (read only)
			[drive]
			file = "/tmp/ovmf.fd"
			format = "raw"
			if = "pflash"
			readonly = "on"
			unit = "0"

			# Firmware settings (writable)
			[drive]
			file = "/tmp/settings.fd"
			format = "raw"
			if = "pflash"
			unit = "1"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuDriveFirmware(&tc.opts))
		}
	})

	t.Run("qemu_drive_config", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDriveConfigOpts
			expected string
		}{{
			qemuDriveConfigOpts{
				name:     "config",
				dev:      qemuDevOpts{"pci", "qemu_pcie0", "00.5", false},
				path:     "/var/9p",
				protocol: "9p",
			},
			`# Shared config drive (9p)
			[fsdev "qemu_config"]
			fsdriver = "local"
			path = "/var/9p"
			readonly = "on"
			security_model = "none"

			[device "dev-qemu_config-drive-9p"]
			addr = "00.5"
			bus = "qemu_pcie0"
			driver = "virtio-9p-pci"
			fsdev = "qemu_config"
			mount_tag = "config"`,
		}, {
			qemuDriveConfigOpts{
				name:     "config",
				dev:      qemuDevOpts{"pcie", "qemu_pcie1", "10.2", true},
				path:     "/dev/virtio-fs",
				protocol: "virtio-fs",
			},
			`# Shared config drive (virtio-fs)
			[chardev "qemu_config"]
			backend = "socket"
			path = "/dev/virtio-fs"

			[device "dev-qemu_config-drive-virtio-fs"]
			addr = "10.2"
			bus = "qemu_pcie1"
			chardev = "qemu_config"
			driver = "vhost-user-fs-pci"
			multifunction = "on"
			tag = "config"`,
		}, {
			qemuDriveConfigOpts{
				name:     "config",
				dev:      qemuDevOpts{"ccw", "qemu_pcie0", "00.0", false},
				path:     "/var/virtio-fs",
				protocol: "virtio-fs",
			},
			`# Shared config drive (virtio-fs)
			[chardev "qemu_config"]
			backend = "socket"
			path = "/var/virtio-fs"

			[device "dev-qemu_config-drive-virtio-fs"]
			chardev = "qemu_config"
			driver = "vhost-user-fs-ccw"
			tag = "config"`,
		}, {
			qemuDriveConfigOpts{
				name:     "config",
				dev:      qemuDevOpts{"ccw", "qemu_pcie0", "00.0", true},
				path:     "/dev/9p",
				protocol: "9p",
			},
			`# Shared config drive (9p)
			[fsdev "qemu_config"]
			fsdriver = "local"
			path = "/dev/9p"
			readonly = "on"
			security_model = "none"

			[device "dev-qemu_config-drive-9p"]
			driver = "virtio-9p-ccw"
			fsdev = "qemu_config"
			mount_tag = "config"
			multifunction = "on"`,
		}, {
			qemuDriveConfigOpts{
				dev:      qemuDevOpts{"ccw", "qemu_pcie0", "00.0", true},
				path:     "/dev/9p",
				protocol: "invalid",
			},
			``,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuDriveConfig(&tc.opts))
		}
	})

	t.Run("qemu_drive_dir", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDriveDirOpts
			expected string
		}{{
			qemuDriveDirOpts{
				dev:      qemuDevOpts{"pci", "qemu_pcie0", "00.5", true},
				devName:  "stub",
				mountTag: "mtag",
				path:     "/var/9p",
				protocol: "9p",
				readonly: false,
			},
			`# stub drive (9p)
			[fsdev "incus_stub"]
			fsdriver = "local"
			path = "/var/9p"
			readonly = "off"
			security_model = "passthrough"

			[device "dev-incus_stub-9p"]
			addr = "00.5"
			bus = "qemu_pcie0"
			driver = "virtio-9p-pci"
			fsdev = "incus_stub"
			mount_tag = "mtag"
			multifunction = "on"`,
		}, {
			qemuDriveDirOpts{
				dev:      qemuDevOpts{"pcie", "qemu_pcie1", "10.2", false},
				path:     "/dev/virtio",
				devName:  "vfs",
				mountTag: "vtag",
				protocol: "virtio-fs",
			},
			`# vfs drive (virtio-fs)
			[chardev "incus_vfs"]
			backend = "socket"
			path = "/dev/virtio"

			[device "dev-incus_vfs-virtio-fs"]
			addr = "10.2"
			bus = "qemu_pcie1"
			chardev = "incus_vfs"
			driver = "vhost-user-fs-pci"
			tag = "vtag"`,
		}, {
			qemuDriveDirOpts{
				dev:      qemuDevOpts{"ccw", "qemu_pcie0", "00.0", true},
				path:     "/dev/vio",
				devName:  "vfs",
				mountTag: "vtag",
				protocol: "virtio-fs",
			},
			`# vfs drive (virtio-fs)
			[chardev "incus_vfs"]
			backend = "socket"
			path = "/dev/vio"

			[device "dev-incus_vfs-virtio-fs"]
			chardev = "incus_vfs"
			driver = "vhost-user-fs-ccw"
			multifunction = "on"
			tag = "vtag"`,
		}, {
			qemuDriveDirOpts{
				dev:      qemuDevOpts{"ccw", "qemu_pcie0", "00.0", false},
				devName:  "stub2",
				mountTag: "mtag2",
				path:     "/var/9p",
				protocol: "9p",
				readonly: true,
			},
			`# stub2 drive (9p)
			[fsdev "incus_stub2"]
			fsdriver = "local"
			path = "/var/9p"
			readonly = "on"
			security_model = "passthrough"

			[device "dev-incus_stub2-9p"]
			driver = "virtio-9p-ccw"
			fsdev = "incus_stub2"
			mount_tag = "mtag2"`,
		}, {
			qemuDriveDirOpts{
				dev:      qemuDevOpts{"ccw", "qemu_pcie0", "00.0", true},
				path:     "/dev/9p",
				protocol: "invalid",
			},
			``,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuDriveDir(&tc.opts))
		}
	})

	t.Run("qemu_pci_physical", func(t *testing.T) {
		testCases := []struct {
			opts     qemuPCIPhysicalOpts
			expected string
		}{{
			qemuPCIPhysicalOpts{
				dev:         qemuDevOpts{"pci", "qemu_pcie1", "00.0", false},
				devName:     "physical-pci-name",
				pciSlotName: "host-slot",
			},
			`# PCI card ("physical-pci-name" device)
			[device "dev-incus_physical-pci-name"]
			addr = "00.0"
			bus = "qemu_pcie1"
			driver = "vfio-pci"
			host = "host-slot"`,
		}, {
			qemuPCIPhysicalOpts{
				dev:         qemuDevOpts{"ccw", "qemu_pcie2", "00.2", true},
				devName:     "physical-ccw-name",
				pciSlotName: "host-slot-ccw",
			},
			`# PCI card ("physical-ccw-name" device)
			[device "dev-incus_physical-ccw-name"]
			driver = "vfio-ccw"
			host = "host-slot-ccw"
			multifunction = "on"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuPCIPhysical(&tc.opts))
		}
	})

	t.Run("qemu_gpu_dev_physical", func(t *testing.T) {
		testCases := []struct {
			opts     qemuGPUDevPhysicalOpts
			expected string
		}{{
			qemuGPUDevPhysicalOpts{
				dev:         qemuDevOpts{"pci", "qemu_pcie1", "00.0", false},
				devName:     "gpu-name",
				pciSlotName: "gpu-slot",
			},
			`# GPU card ("gpu-name" device)
			[device "dev-incus_gpu-name"]
			addr = "00.0"
			bus = "qemu_pcie1"
			driver = "vfio-pci"
			host = "gpu-slot"`,
		}, {
			qemuGPUDevPhysicalOpts{
				dev:         qemuDevOpts{"ccw", "qemu_pcie1", "00.0", true},
				devName:     "gpu-name",
				pciSlotName: "gpu-slot",
				vga:         true,
			},
			`# GPU card ("gpu-name" device)
			[device "dev-incus_gpu-name"]
			driver = "vfio-ccw"
			host = "gpu-slot"
			multifunction = "on"
			x-vga = "on"`,
		}, {
			qemuGPUDevPhysicalOpts{
				dev:     qemuDevOpts{"pci", "qemu_pcie1", "00.0", true},
				devName: "vgpu-name",
				vgpu:    "vgpu-dev",
			},
			`# GPU card ("vgpu-name" device)
			[device "dev-incus_vgpu-name"]
			addr = "00.0"
			bus = "qemu_pcie1"
			driver = "vfio-pci"
			multifunction = "on"
			sysfsdev = "/sys/bus/mdev/devices/vgpu-dev"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuGPUDevPhysical(&tc.opts))
		}
	})

	t.Run("qemu_usb", func(t *testing.T) {
		testCases := []struct {
			opts     qemuUSBOpts
			expected string
		}{{
			qemuUSBOpts{
				devBus:        "qemu_pcie1",
				devAddr:       "00.0",
				multifunction: true,
				ports:         3,
			},
			`# USB controller
			[device "qemu_usb"]
			addr = "00.0"
			bus = "qemu_pcie1"
			driver = "qemu-xhci"
			multifunction = "on"
			p2 = "3"
			p3 = "3"

			[chardev "qemu_spice-usb-chardev1"]
			backend = "spicevmc"
			name = "usbredir"

			[device "qemu_spice-usb1"]
			chardev = "qemu_spice-usb-chardev1"
			driver = "usb-redir"

			[chardev "qemu_spice-usb-chardev2"]
			backend = "spicevmc"
			name = "usbredir"

			[device "qemu_spice-usb2"]
			chardev = "qemu_spice-usb-chardev2"
			driver = "usb-redir"

			[chardev "qemu_spice-usb-chardev3"]
			backend = "spicevmc"
			name = "usbredir"

			[device "qemu_spice-usb3"]
			chardev = "qemu_spice-usb-chardev3"
			driver = "usb-redir"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuUSB(&tc.opts))
		}
	})

	t.Run("qemu_tpm", func(t *testing.T) {
		testCases := []struct {
			opts     qemuTPMOpts
			expected string
		}{{
			qemuTPMOpts{
				devName: "myTpm",
				path:    "/dev/my/tpm",
				driver:  "tpm-crb",
			},
			`[chardev "qemu_tpm-chardev_myTpm"]
			backend = "socket"
			path = "/dev/my/tpm"

			[tpmdev "qemu_tpm-tpmdev_myTpm"]
			chardev = "qemu_tpm-chardev_myTpm"
			type = "emulator"

			[device "dev-incus_myTpm"]
			driver = "tpm-crb"
			tpmdev = "qemu_tpm-tpmdev_myTpm"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuTPM(&tc.opts))
		}
	})

	t.Run("qemu_raw_cfg_override", func(t *testing.T) {
		conf := []cfg.Section{{
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
		}, {
			Name:    "memory",
			Entries: map[string]string{"size": "1024M"},
		}, {
			Name: `device "qemu_gpu"`,
			Entries: map[string]string{
				"driver": "virtio-gpu-pci",
				"bus":    "qemu_pci3",
				"addr":   "00.0",
			},
		}, {
			Name: `device "qemu_keyboard"`,
			Entries: map[string]string{
				"driver": "virtio-keyboard-pci",
				"bus":    "qemu_pci2",
				"addr":   "00.1",
			},
		}}
		testCases := []struct {
			cfg       []cfg.Section
			overrides string
			expected  string
		}{{
			// override some keys
			conf,
			`[memory]
			size = "4096M"

			[device "qemu_gpu"]
			driver = "qxl-vga"`,
			`[global]
			driver = "ICH9-LPC"
			property = "disable_s3"
			value = "1"

			[global]
			driver = "ICH9-LPC"
			property = "disable_s4"
			value = "0"

			[memory]
			size = "4096M"

			[device "qemu_gpu"]
			addr = "00.0"
			bus = "qemu_pci3"
			driver = "qxl-vga"

			[device "qemu_keyboard"]
			addr = "00.1"
			bus = "qemu_pci2"
			driver = "virtio-keyboard-pci"`,
		}, {
			// delete some keys
			conf,
			`[device "qemu_keyboard"]
			driver = ""

			[device "qemu_gpu"]
			addr = ""`,
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "0"

				[memory]
				size = "1024M"

				[device "qemu_gpu"]
				bus = "qemu_pci3"
				driver = "virtio-gpu-pci"

				[device "qemu_keyboard"]
				addr = "00.1"
				bus = "qemu_pci2"`,
		}, {
			// add some keys to existing sections
			conf,
			`[memory]
			somekey = "somevalue"
			somekey2 =             "somevalue2"
			somekey3 =   "somevalue3"
			somekey4="somevalue4"

			[device "qemu_keyboard"]
			multifunction="off"

			[device "qemu_gpu"]
			multifunction=      "on"`,
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "0"

				[memory]
				size = "1024M"
				somekey = "somevalue"
				somekey2 = "somevalue2"
				somekey3 = "somevalue3"
				somekey4 = "somevalue4"

				[device "qemu_gpu"]
				addr = "00.0"
				bus = "qemu_pci3"
				driver = "virtio-gpu-pci"
				multifunction = "on"

				[device "qemu_keyboard"]
				addr = "00.1"
				bus = "qemu_pci2"
				driver = "virtio-keyboard-pci"
				multifunction = "off"`,
		}, {
			// edit/add/remove
			conf,
			`[memory]
			size = "2048M"
			[device "qemu_gpu"]
			multifunction = "on"
			[device "qemu_keyboard"]
			addr = ""
			bus = ""`,
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "0"

				[memory]
				size = "2048M"

				[device "qemu_gpu"]
				addr = "00.0"
				bus = "qemu_pci3"
				driver = "virtio-gpu-pci"
				multifunction = "on"

				[device "qemu_keyboard"]
				driver = "virtio-keyboard-pci"`,
		}, {
			// delete sections
			conf,
			`[memory]
			[device "qemu_keyboard"]
			[global][1]`,
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[device "qemu_gpu"]
				addr = "00.0"
				bus = "qemu_pci3"
				driver = "virtio-gpu-pci"`,
		}, {
			// add sections
			conf,
			`[object1]
			key1     = "value1"
			key2     = "value2"

			[object "2"]
			key3  = "value3"
			[object "3"]
			key4  = "value4"

			[object "2"]
			key5  = "value5"

			[object1]
			key6     = "value6"`,
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "0"

				[memory]
				size = "1024M"

				[device "qemu_gpu"]
				addr = "00.0"
				bus = "qemu_pci3"
				driver = "virtio-gpu-pci"

				[device "qemu_keyboard"]
				addr = "00.1"
				bus = "qemu_pci2"
				driver = "virtio-keyboard-pci"

				[object1]
				key1 = "value1"
				key2 = "value2"
				key6 = "value6"

				[object "2"]
				key3 = "value3"
				key5 = "value5"

				[object "3"]
				key4 = "value4"`,
		}, {
			// add/remove sections
			conf,
			`[device "qemu_gpu"]
			[object "2"]
			key3  = "value3"
			[object "3"]
			key4  = "value4"
			[object "2"]
			key5  = "value5"`,
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "0"

				[memory]
				size = "1024M"

				[device "qemu_keyboard"]
				addr = "00.1"
				bus = "qemu_pci2"
				driver = "virtio-keyboard-pci"

				[object "2"]
				key3 = "value3"
				key5 = "value5"

				[object "3"]
				key4 = "value4"`,
		}, {
			// edit keys of repeated sections
			conf,
			`[global][1]
			property ="disable_s1"
			[global]
			property ="disable_s5"
			[global][1]
			value = ""
			[global][0]
			somekey ="somevalue"
			[global][1]
			anotherkey = "anothervalue"`,
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s5"
				somekey = "somevalue"
				value = "1"

				[global]
				anotherkey = "anothervalue"
				driver = "ICH9-LPC"
				property = "disable_s1"

				[memory]
				size = "1024M"

				[device "qemu_gpu"]
				addr = "00.0"
				bus = "qemu_pci3"
				driver = "virtio-gpu-pci"

				[device "qemu_keyboard"]
				addr = "00.1"
				bus = "qemu_pci2"
				driver = "virtio-keyboard-pci"`,
		}, {
			// create multiple sections with same name
			conf,
			// note that for appending new sections, all that matters is that
			// the index is higher than the existing indexes
			`[global][2]
			property =  "new section"
			[global][2]
			value =     "new value"
			[object][3]
			k1 =        "v1"
			[object][3]
			k2 =        "v2"
			[object][4]
			k3 =        "v1"
			[object][4]
			k2 =        "v2"
			[object][11]
			k11 =  "v11"`,
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "0"

				[memory]
				size = "1024M"

				[device "qemu_gpu"]
				addr = "00.0"
				bus = "qemu_pci3"
				driver = "virtio-gpu-pci"

				[device "qemu_keyboard"]
				addr = "00.1"
				bus = "qemu_pci2"
				driver = "virtio-keyboard-pci"

				[global]
				property = "new section"
				value = "new value"

				[object]
				k1 = "v1"
				k2 = "v2"

				[object]
				k2 = "v2"
				k3 = "v1"

				[object]
				k11 = "v11"`,
		}, {
			// create multiple sections with same name, with decreasing indices
			conf,
			`[object][3]
			k1 =        "v1"
			[object][3]
			k2 =        "v2"
			[object][2]
			k3 =        "v1"
			[object][2]
			k2 =        "v2"`,
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "0"

				[memory]
				size = "1024M"

				[device "qemu_gpu"]
				addr = "00.0"
				bus = "qemu_pci3"
				driver = "virtio-gpu-pci"

				[device "qemu_keyboard"]
				addr = "00.1"
				bus = "qemu_pci2"
				driver = "virtio-keyboard-pci"

				[object]
				k1 = "v1"
				k2 = "v2"

				[object]
				k2 = "v2"
				k3 = "v1"`,
		}, {
			// mix all operations
			conf,
			`[memory]
			size = "8192M"
			[device "qemu_keyboard"]
			multifunction=on
			bus =
			[device "qemu_gpu"]
			[object "3"]
			key4 = " value4 "
			[object "2"]
			key3 =   value3
			[object "3"]
			key5 = "value5"`,
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "0"

				[memory]
				size = "8192M"

				[device "qemu_keyboard"]
				addr = "00.1"
				driver = "virtio-keyboard-pci"
				multifunction = "on"

				[object "3"]
				key4 = " value4 "
				key5 = "value5"

				[object "2"]
				key3 = "value3"`,
		}}
		for _, tc := range testCases {
			overridden, err := qemuRawCfgOverride(tc.cfg, tc.overrides)
			if err != nil {
				t.Error(err)
			}

			runTest(tc.expected, overridden)
		}
	})
}
