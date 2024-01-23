package drivers

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/lxc/incus/shared/osarch"
)

func TestQemuConfigTemplates(t *testing.T) {
	indent := regexp.MustCompile(`(?m)^[ \t]+`)

	normalize := func(s string) string {
		return strings.TrimSpace(indent.ReplaceAllString(s, "$1"))
	}

	runTest := func(expected string, sections []cfgSection) {
		t.Run(expected, func(t *testing.T) {
			actual := normalize(qemuStringifyCfg(sections...).String())
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
			graphics = "off"
			type = "q35"
			accel = "kvm"
			usb = "off"

			[global]
			driver = "ICH9-LPC"
			property = "disable_s3"
			value = "1"

			[global]
			driver = "ICH9-LPC"
			property = "disable_s4"
			value = "1"

			[boot-opts]
			strict = "on"`,
		}, {
			qemuBaseOpts{architecture: osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN},
			`# Machine
			[machine]
			graphics = "off"
			type = "virt"
			gic-version = "max"
			accel = "kvm"
			usb = "off"

			[boot-opts]
			strict = "on"`,
		}, {
			qemuBaseOpts{architecture: osarch.ARCH_64BIT_POWERPC_LITTLE_ENDIAN},
			`# Machine
			[machine]
			graphics = "off"
			type = "pseries"
			cap-large-decr = "off"
			accel = "kvm"
			usb = "off"

			[boot-opts]
			strict = "on"`,
		}, {
			qemuBaseOpts{architecture: osarch.ARCH_64BIT_S390_BIG_ENDIAN},
			`# Machine
			[machine]
			graphics = "off"
			type = "s390-ccw-virtio"
			accel = "kvm"
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
			qemuMemoryOpts{4096},
			`# Memory
			[memory]
			size = "4096M"`,
		}, {
			qemuMemoryOpts{8192},
			`# Memory
			[memory]
			size = "8192M"`,
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
			driver = "virtio-serial-pci"
			bus = "qemu_pcie0"
			addr = "00.5"

			# Serial identifier
			[chardev "qemu_serial-chardev"]
			backend = "ringbuf"
			size = "32B"

			[device "qemu_serial"]
			driver = "virtserialport"
			name = "org.linuxcontainers.incus"
			chardev = "qemu_serial-chardev"
			bus = "dev-qemu_serial.0"

			[device "qemu_serial_legacy"]
			driver = "virtserialport"
			name = "org.linuxcontainers.lxd"
			bus = "dev-qemu_serial.0"

			# Spice agent
			[chardev "qemu_spice-chardev"]
			backend = "spicevmc"
			name = "vdagent"

			[device "qemu_spice"]
			driver = "virtserialport"
			name = "com.redhat.spice.0"
			chardev = "qemu_spice-chardev"
			bus = "dev-qemu_serial.0"

			# Spice folder
			[chardev "qemu_spicedir-chardev"]
			backend = "spiceport"
			name = "org.spice-space.webdav.0"

			[device "qemu_spicedir"]
			driver = "virtserialport"
			name = "org.spice-space.webdav.0"
			chardev = "qemu_spicedir-chardev"
			bus = "dev-qemu_serial.0"
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
			driver = "pcie-root-port"
			bus = "pcie.0"
			addr = "1.0"
			chassis = "0"
			multifunction = "on"
			`,
		}, {
			qemuPCIeOpts{"qemu_pcie2", 3, "2.0", false},
			`[device "qemu_pcie2"]
			driver = "pcie-root-port"
			bus = "pcie.0"
			addr = "2.0"
			chassis = "3"
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
			driver = "virtio-scsi-pci"
			bus = "qemu_pcie1"
			addr = "00.0"
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
			driver = "virtio-balloon-pci"
			bus = "qemu_pcie0"
			addr = "00.0"
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
			qom-type = "rng-random"
			filename = "/dev/urandom"

			[device "dev-qemu_rng"]
			driver = "virtio-rng-pci"
			bus = "qemu_pcie0"
			addr = "00.1"
			rng = "qemu_rng"
			`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie0", "00.1", true},
			`# Random number generator
			[object "qemu_rng"]
			qom-type = "rng-random"
			filename = "/dev/urandom"

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
			driver = "vhost-vsock-pci"
			bus = "qemu_pcie0"
			addr = "00.4"
			multifunction = "on"
			guest-cid = "14"
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
			driver = "virtio-vga"
			bus = "qemu_pcie3"
			addr = "00.0"
			multifunction = "on"`,
		}, {
			qemuGpuOpts{dev: qemuDevOpts{"pci", "qemu_pci3", "00.1", false}, architecture: osarch.ARCH_UNKNOWN},
			`# GPU
			[device "qemu_gpu"]
			driver = "virtio-gpu-pci"
			bus = "qemu_pci3"
			addr = "00.1"`,
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
			driver = "virtio-keyboard-pci"
			bus = "qemu_pcie3"
			addr = "00.0"`,
		}, {
			qemuDevOpts{"pcie", "qemu_pcie3", "00.0", true},
			`# Input
			[device "qemu_keyboard"]
			driver = "virtio-keyboard-pci"
			bus = "qemu_pcie3"
			addr = "00.0"
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
			driver = "virtio-tablet-pci"
			bus = "qemu_pcie0"
			addr = "00.3"
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
			cpus = "8"
			sockets = "1"
			cores = "4"
			threads = "2"

			[object "mem0"]
			qom-type = "memory-backend-memfd"
			size = "7629M"
			share = "on"

			[numa]
			type = "node"
			nodeid = "0"
			memdev = "mem0"`,
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
			cpus = "2"
			sockets = "1"
			cores = "2"
			threads = "1"

			[object "mem0"]
			qom-type = "memory-backend-file"
			mem-path = "/hugepages/path"
			prealloc = "on"
			discard-data = "on"
			size = "12000M"
			policy = "bind"
			share = "on"
			host-nodes.0 = "8"

			[numa]
			type = "node"
			nodeid = "0"
			memdev = "mem0"

			[object "mem1"]
			qom-type = "memory-backend-file"
			mem-path = "/hugepages/path"
			prealloc = "on"
			discard-data = "on"
			size = "12000M"
			policy = "bind"
			share = "on"
			host-nodes.0 = "9"

			[numa]
			type = "node"
			nodeid = "1"
			memdev = "mem1"

			[object "mem2"]
			qom-type = "memory-backend-file"
			mem-path = "/hugepages/path"
			prealloc = "on"
			discard-data = "on"
			size = "12000M"
			policy = "bind"
			share = "on"
			host-nodes.0 = "10"

			[numa]
			type = "node"
			nodeid = "2"
			memdev = "mem2"

			[numa]
			type = "cpu"
			node-id = "20"
			socket-id = "21"
			core-id = "22"
			thread-id = "23"`,
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
			cpus = "2"
			sockets = "1"
			cores = "2"
			threads = "1"

			[object "mem0"]
			qom-type = "memory-backend-memfd"
			size = "12000M"
			policy = "bind"
			host-nodes.0 = "8"

			[numa]
			type = "node"
			nodeid = "0"
			memdev = "mem0"

			[object "mem1"]
			qom-type = "memory-backend-memfd"
			size = "12000M"
			policy = "bind"
			host-nodes.0 = "9"

			[numa]
			type = "node"
			nodeid = "1"
			memdev = "mem1"

			[object "mem2"]
			qom-type = "memory-backend-memfd"
			size = "12000M"
			policy = "bind"
			host-nodes.0 = "10"

			[numa]
			type = "node"
			nodeid = "2"
			memdev = "mem2"

			[numa]
			type = "cpu"
			node-id = "20"
			socket-id = "21"
			core-id = "22"
			thread-id = "23"`,
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
			cpus = "4"
			sockets = "1"
			cores = "4"
			threads = "1"

			[object "mem0"]
			qom-type = "memory-backend-memfd"
			size = "12000M"
			policy = "bind"
			host-nodes = "8"

			[numa]
			type = "node"
			nodeid = "0"
			memdev = "mem0"

			[object "mem1"]
			qom-type = "memory-backend-memfd"
			size = "12000M"
			policy = "bind"
			host-nodes = "9"

			[numa]
			type = "node"
			nodeid = "1"
			memdev = "mem1"

			[object "mem2"]
			qom-type = "memory-backend-memfd"
			size = "12000M"
			policy = "bind"
			host-nodes = "10"

			[numa]
			type = "node"
			nodeid = "2"
			memdev = "mem2"

			[numa]
			type = "cpu"
			node-id = "11"
			socket-id = "12"
			core-id = "13"
			thread-id = "14"

			[numa]
			type = "cpu"
			node-id = "20"
			socket-id = "21"
			core-id = "22"
			thread-id = "23"`,
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
			cpus = "4"
			sockets = "1"
			cores = "4"
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
			opts     qemuConsoleOpts
			expected string
		}{{
			qemuConsoleOpts{"/dev/shm/console-socket"},
			`# Console
			[chardev "console"]
			backend = "socket"
			path = "/dev/shm/console-socket"
			server = "on"
			wait = "off"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuConsole(&tc.opts))
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
			if = "pflash"
			format = "raw"
			unit = "0"
			readonly = "on"

			# Firmware settings (writable)
			[drive]
			file = "/tmp/settings.fd"
			if = "pflash"
			format = "raw"
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
			security_model = "none"
			readonly = "on"
			path = "/var/9p"

			[device "dev-qemu_config-drive-9p"]
			driver = "virtio-9p-pci"
			bus = "qemu_pcie0"
			addr = "00.5"
			mount_tag = "config"
			fsdev = "qemu_config"`,
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
			driver = "vhost-user-fs-pci"
			bus = "qemu_pcie1"
			addr = "10.2"
			multifunction = "on"
			tag = "config"
			chardev = "qemu_config"`,
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
			driver = "vhost-user-fs-ccw"
			tag = "config"
			chardev = "qemu_config"`,
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
			security_model = "none"
			readonly = "on"
			path = "/dev/9p"

			[device "dev-qemu_config-drive-9p"]
			driver = "virtio-9p-ccw"
			multifunction = "on"
			mount_tag = "config"
			fsdev = "qemu_config"`,
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
				protocol: "9p",
				readonly: false,
				proxyFD:  5,
			},
			`# stub drive (9p)
			[fsdev "incus_stub"]
			fsdriver = "proxy"
			sock_fd = "5"
			readonly = "off"

			[device "dev-incus_stub-9p"]
			driver = "virtio-9p-pci"
			bus = "qemu_pcie0"
			addr = "00.5"
			multifunction = "on"
			mount_tag = "mtag"
			fsdev = "incus_stub"`,
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
			driver = "vhost-user-fs-pci"
			bus = "qemu_pcie1"
			addr = "10.2"
			tag = "vtag"
			chardev = "incus_vfs"`,
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
			driver = "vhost-user-fs-ccw"
			multifunction = "on"
			tag = "vtag"
			chardev = "incus_vfs"`,
		}, {
			qemuDriveDirOpts{
				dev:      qemuDevOpts{"ccw", "qemu_pcie0", "00.0", false},
				devName:  "stub2",
				mountTag: "mtag2",
				protocol: "9p",
				readonly: true,
				proxyFD:  3,
			},
			`# stub2 drive (9p)
			[fsdev "incus_stub2"]
			fsdriver = "proxy"
			sock_fd = "3"
			readonly = "on"

			[device "dev-incus_stub2-9p"]
			driver = "virtio-9p-ccw"
			mount_tag = "mtag2"
			fsdev = "incus_stub2"`,
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
			driver = "vfio-pci"
			bus = "qemu_pcie1"
			addr = "00.0"
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
			multifunction = "on"
			host = "host-slot-ccw"`,
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
			driver = "vfio-pci"
			bus = "qemu_pcie1"
			addr = "00.0"
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
			multifunction = "on"
			host = "gpu-slot"
			x-vga = "on"`,
		}, {
			qemuGPUDevPhysicalOpts{
				dev:     qemuDevOpts{"pci", "qemu_pcie1", "00.0", true},
				devName: "vgpu-name",
				vgpu:    "vgpu-dev",
			},
			`# GPU card ("vgpu-name" device)
			[device "dev-incus_vgpu-name"]
			driver = "vfio-pci"
			bus = "qemu_pcie1"
			addr = "00.0"
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
			driver = "qemu-xhci"
			bus = "qemu_pcie1"
			addr = "00.0"
			multifunction = "on"
			p2 = "3"
			p3 = "3"

			[chardev "qemu_spice-usb-chardev1"]
			backend = "spicevmc"
			name = "usbredir"

			[device "qemu_spice-usb1"]
			driver = "usb-redir"
			chardev = "qemu_spice-usb-chardev1"

			[chardev "qemu_spice-usb-chardev2"]
			backend = "spicevmc"
			name = "usbredir"

			[device "qemu_spice-usb2"]
			driver = "usb-redir"
			chardev = "qemu_spice-usb-chardev2"

			[chardev "qemu_spice-usb-chardev3"]
			backend = "spicevmc"
			name = "usbredir"

			[device "qemu_spice-usb3"]
			driver = "usb-redir"
			chardev = "qemu_spice-usb-chardev3"`,
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
			},
			`[chardev "qemu_tpm-chardev_myTpm"]
			backend = "socket"
			path = "/dev/my/tpm"

			[tpmdev "qemu_tpm-tpmdev_myTpm"]
			type = "emulator"
			chardev = "qemu_tpm-chardev_myTpm"

			[device "dev-incus_myTpm"]
			driver = "tpm-crb"
			tpmdev = "qemu_tpm-tpmdev_myTpm"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuTPM(&tc.opts))
		}
	})

	t.Run("qemu_raw_cfg_override", func(t *testing.T) {
		cfg := []cfgSection{{
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
		}, {
			name: "memory",
			entries: []cfgEntry{
				{key: "size", value: "1024M"},
			},
		}, {
			name: `device "qemu_gpu"`,
			entries: []cfgEntry{
				{key: "driver", value: "virtio-gpu-pci"},
				{key: "bus", value: "qemu_pci3"},
				{key: "addr", value: "00.0"},
			},
		}, {
			name: `device "qemu_keyboard"`,
			entries: []cfgEntry{
				{key: "driver", value: "virtio-keyboard-pci"},
				{key: "bus", value: "qemu_pci2"},
				{key: "addr", value: "00.1"},
			},
		}}
		testCases := []struct {
			cfg       []cfgSection
			overrides map[string]string
			expected  string
		}{{
			// unmodified
			cfg,
			map[string]string{},
			`[global]
			driver = "ICH9-LPC"
			property = "disable_s3"
			value = "1"

			[global]
			driver = "ICH9-LPC"
			property = "disable_s4"
			value = "1"

			[memory]
			size = "1024M"

			[device "qemu_gpu"]
			driver = "virtio-gpu-pci"
			bus = "qemu_pci3"
			addr = "00.0"

			[device "qemu_keyboard"]
			driver = "virtio-keyboard-pci"
			bus = "qemu_pci2"
			addr = "00.1"`,
		}, {
			// override some keys
			cfg,
			map[string]string{
				"raw.qemu.conf": `
						[memory]
						size = "4096M"

						[device "qemu_gpu"]
						driver = "qxl-vga"`,
			},
			`[global]
			driver = "ICH9-LPC"
			property = "disable_s3"
			value = "1"

			[global]
			driver = "ICH9-LPC"
			property = "disable_s4"
			value = "1"

			[memory]
			size = "4096M"

			[device "qemu_gpu"]
			driver = "qxl-vga"
			bus = "qemu_pci3"
			addr = "00.0"

			[device "qemu_keyboard"]
			driver = "virtio-keyboard-pci"
			bus = "qemu_pci2"
			addr = "00.1"`,
		}, {
			// delete some keys
			cfg,
			map[string]string{
				"raw.qemu.conf": `
						[device "qemu_keyboard"]
						driver = ""

						[device "qemu_gpu"]
						addr = ""`,
			},
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "1"

				[memory]
				size = "1024M"

				[device "qemu_gpu"]
				driver = "virtio-gpu-pci"
				bus = "qemu_pci3"

				[device "qemu_keyboard"]
				bus = "qemu_pci2"
				addr = "00.1"`,
		}, {
			// add some keys to existing sections
			cfg,
			map[string]string{
				"raw.qemu.conf": `
						[memory]
						somekey = "somevalue"
						somekey2 =             "somevalue2"
						somekey3 =   "somevalue3"
						somekey4="somevalue4"

						[device "qemu_keyboard"]
						multifunction="off"

						[device "qemu_gpu"]
						multifunction=      "on"`,
			},
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "1"

				[memory]
				size = "1024M"
				somekey = "somevalue"
				somekey2 = "somevalue2"
				somekey3 = "somevalue3"
				somekey4 = "somevalue4"

				[device "qemu_gpu"]
				driver = "virtio-gpu-pci"
				bus = "qemu_pci3"
				addr = "00.0"
				multifunction = "on"

				[device "qemu_keyboard"]
				driver = "virtio-keyboard-pci"
				bus = "qemu_pci2"
				addr = "00.1"
				multifunction = "off"`,
		}, {
			// edit/add/remove
			cfg,
			map[string]string{
				"raw.qemu.conf": `
						[memory]
						size = "2048M"
						[device "qemu_gpu"]
						multifunction = "on"
						[device "qemu_keyboard"]
						addr = ""
						bus = ""`,
			},
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "1"

				[memory]
				size = "2048M"

				[device "qemu_gpu"]
				driver = "virtio-gpu-pci"
				bus = "qemu_pci3"
				addr = "00.0"
				multifunction = "on"

				[device "qemu_keyboard"]
				driver = "virtio-keyboard-pci"`,
		}, {
			// delete sections
			cfg,
			map[string]string{
				"raw.qemu.conf": `
						[memory]
						[device "qemu_keyboard"]
						[global][1]`,
			},
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[device "qemu_gpu"]
				driver = "virtio-gpu-pci"
				bus = "qemu_pci3"
				addr = "00.0"`,
		}, {
			// add sections
			cfg,
			map[string]string{
				"raw.qemu.conf": `
						[object1]
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
			},
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "1"

				[memory]
				size = "1024M"

				[device "qemu_gpu"]
				driver = "virtio-gpu-pci"
				bus = "qemu_pci3"
				addr = "00.0"

				[device "qemu_keyboard"]
				driver = "virtio-keyboard-pci"
				bus = "qemu_pci2"
				addr = "00.1"

				[object "2"]
				key3 = "value3"
				key5 = "value5"

				[object "3"]
				key4 = "value4"

				[object1]
				key1 = "value1"
				key2 = "value2"
				key6 = "value6"`,
		}, {
			// add/remove sections
			cfg,
			map[string]string{
				"raw.qemu.conf": `
						[device "qemu_gpu"]
						[object "2"]
						key3  = "value3"
						[object "3"]
						key4  = "value4"
						[object "2"]
						key5  = "value5"`,
			},
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "1"

				[memory]
				size = "1024M"

				[device "qemu_keyboard"]
				driver = "virtio-keyboard-pci"
				bus = "qemu_pci2"
				addr = "00.1"

				[object "2"]
				key3 = "value3"
				key5 = "value5"

				[object "3"]
				key4 = "value4"`,
		}, {
			// edit keys of repeated sections
			cfg,
			map[string]string{
				"raw.qemu.conf": `
						[global][1]
						property ="disable_s1"
						[global]
						property ="disable_s5"
						[global][1]
						value = ""
						[global][0]
						somekey ="somevalue"
						[global][1]
						anotherkey = "anothervalue"`,
			},
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s5"
				value = "1"
				somekey = "somevalue"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s1"
				anotherkey = "anothervalue"

				[memory]
				size = "1024M"

				[device "qemu_gpu"]
				driver = "virtio-gpu-pci"
				bus = "qemu_pci3"
				addr = "00.0"

				[device "qemu_keyboard"]
				driver = "virtio-keyboard-pci"
				bus = "qemu_pci2"
				addr = "00.1"`,
		}, {
			// create multiple sections with same name
			cfg,
			// note that for appending new sections, all that matters is that
			// the index is higher than the existing indexes
			map[string]string{
				"raw.qemu.conf": `
						[global][2]
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
			},
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "1"

				[memory]
				size = "1024M"

				[device "qemu_gpu"]
				driver = "virtio-gpu-pci"
				bus = "qemu_pci3"
				addr = "00.0"

				[device "qemu_keyboard"]
				driver = "virtio-keyboard-pci"
				bus = "qemu_pci2"
				addr = "00.1"

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
			// mix all operations
			cfg,
			map[string]string{
				"raw.qemu.conf": `
						[memory]
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
			},
			`[global]
				driver = "ICH9-LPC"
				property = "disable_s3"
				value = "1"

				[global]
				driver = "ICH9-LPC"
				property = "disable_s4"
				value = "1"

				[memory]
				size = "8192M"

				[device "qemu_keyboard"]
				driver = "virtio-keyboard-pci"
				addr = "00.1"
				multifunction = "on"

				[object "2"]
				key3 = "value3"

				[object "3"]
				key4 = " value4 "
				key5 = "value5"`,
		}}
		for _, tc := range testCases {
			runTest(tc.expected, qemuRawCfgOverride(tc.cfg, tc.overrides))
		}
	})

	t.Run("parse_conf_override", func(t *testing.T) {
		input := `
		[global]
		key1 = "val1"
		key3 = "val3"

		[global][0]
		key2 = "val2"

		[global][1]
		key1 = "val3"

		[global][4]
		key2 = "val4"

		[global]

		[global][4]
		[global][5]
		`
		expected := configMap{
			{"global", 0, "key1"}: "val1",
			{"global", 0, "key3"}: "val3",
			{"global", 0, "key2"}: "val2",
			{"global", 1, "key1"}: "val3",
			{"global", 4, "key2"}: "val4",
			{"global", 0, ""}:     "",
			{"global", 4, ""}:     "",
			{"global", 5, ""}:     "",
		}

		actual := parseConfOverride(input)
		if !reflect.DeepEqual(expected, actual) {
			t.Errorf("Expected: %v. Got: %v", expected, actual)
		}
	})
}
