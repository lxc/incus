//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mdlayher/vsock"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/ports"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/ip"
	"github.com/lxc/incus/v6/internal/server/metrics"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

var (
	// These mountpoints are excluded as they are irrelevant for metrics.
	// /var/lib/docker/* subdirectories are excluded for this reason: https://github.com/prometheus/node_exporter/pull/1003
	osMetricsExcludeMountpoints = regexp.MustCompile(`^/(?:dev|proc|sys|var/lib/docker/.+)(?:$|/)`)
	osMetricsExcludeFilesystems = []string{"autofs", "binfmt_misc", "bpf", "cgroup", "cgroup2", "configfs", "debugfs", "devpts", "devtmpfs", "fusectl", "hugetlbfs", "iso9660", "mqueue", "nsfs", "overlay", "proc", "procfs", "pstore", "rpc_pipefs", "securityfs", "selinuxfs", "squashfs", "sysfs", "tracefs"}

	osShutdownSignal       = unix.SIGTERM
	osExitStatus           = linux.ExitStatus
	osBaseWorkingDirectory = "/"
	osMetricsSupported     = true
	osGuestAPISupport      = true
)

func osGetEnvironment() (*api.ServerEnvironment, error) {
	uname, err := linux.Uname()
	if err != nil {
		return nil, err
	}

	serverName, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	env := &api.ServerEnvironment{
		Kernel:             uname.Sysname,
		KernelArchitecture: uname.Machine,
		KernelVersion:      uname.Release,
		Server:             "incus-agent",
		ServerPid:          os.Getpid(),
		ServerVersion:      version.Version,
		ServerName:         serverName,
	}

	return env, nil
}

func osLoadModules() error {
	// Attempt to load the virtio_net driver in case it's not be loaded yet.
	// This may be needed for later network configuration.
	_ = linux.LoadModule("virtio_net")

	// Load the vsock driver if not loaded yet, this is required for host communication.
	if !util.PathExists("/dev/vsock") {
		logger.Info("Loading vsock module")

		err := linux.LoadModule("vsock")
		if err != nil {
			return fmt.Errorf("Unable to load the vsock kernel module: %w", err)
		}

		// Wait for vsock device to appear.
		for range 5 {
			if !util.PathExists("/dev/vsock") {
				time.Sleep(1 * time.Second)
			}
		}
	}

	return nil
}

func osMountShared(src string, dst string, fstype string, opts []string) error {
	// Convert relative mounts to absolute from / otherwise dir creation fails or mount fails.
	if !strings.HasPrefix(dst, "/") {
		dst = fmt.Sprintf("/%s", dst)
	}

	// Check mount path.
	if !util.PathExists(dst) {
		// Create the mount path.
		err := os.MkdirAll(dst, 0o755)
		if err != nil {
			return fmt.Errorf("Failed to create mount target %q", dst)
		}
	} else if linux.IsMountPoint(dst) {
		// Already mounted.
		return nil
	}

	args := []string{"-t", fstype, src, dst}
	for _, opt := range opts {
		args = append(args, "-o", opt)
	}

	_, err := subprocess.RunCommand("mount", args...)
	if err == nil {
		return err
	}

	return nil
}

func osUmount(src string) error {
	_, err := subprocess.RunCommand("umount", src)
	return err
}

func osGetCPUMetrics(d *Daemon) ([]metrics.CPUMetrics, error) {
	stats, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, fmt.Errorf("Failed to read /proc/stat: %w", err)
	}

	out := []metrics.CPUMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(stats))

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)

		// Only consider CPU info, skip everything else. Skip aggregated CPU stats since there will
		// be stats for each individual CPU.
		if !strings.HasPrefix(fields[0], "cpu") || fields[0] == "cpu" {
			continue
		}

		// Validate the number of fields only for lines starting with "cpu".
		if len(fields) < 9 {
			return nil, fmt.Errorf("Invalid /proc/stat content: %q", line)
		}

		stats := metrics.CPUMetrics{}

		stats.SecondsUser, err = strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[1], err)
		}

		stats.SecondsUser /= 100

		stats.SecondsNice, err = strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[2], err)
		}

		stats.SecondsNice /= 100

		stats.SecondsSystem, err = strconv.ParseFloat(fields[3], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[3], err)
		}

		stats.SecondsSystem /= 100

		stats.SecondsIdle, err = strconv.ParseFloat(fields[4], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[4], err)
		}

		stats.SecondsIdle /= 100

		stats.SecondsIOWait, err = strconv.ParseFloat(fields[5], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[5], err)
		}

		stats.SecondsIOWait /= 100

		stats.SecondsIRQ, err = strconv.ParseFloat(fields[6], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[6], err)
		}

		stats.SecondsIRQ /= 100

		stats.SecondsSoftIRQ, err = strconv.ParseFloat(fields[7], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[7], err)
		}

		stats.SecondsSoftIRQ /= 100

		stats.SecondsSteal, err = strconv.ParseFloat(fields[8], 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[8], err)
		}

		stats.SecondsSteal /= 100

		stats.CPU = fields[0]
		out = append(out, stats)
	}

	return out, nil
}

func osGetDiskMetrics(d *Daemon) ([]metrics.DiskMetrics, error) {
	diskStats, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return nil, fmt.Errorf("Failed to read /proc/diskstats: %w", err)
	}

	out := []metrics.DiskMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(diskStats))

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 10 {
			return nil, fmt.Errorf("Invalid /proc/diskstats content: %q", line)
		}

		stats := metrics.DiskMetrics{}

		stats.ReadsCompleted, err = strconv.ParseUint(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[3], err)
		}

		sectorsRead, err := strconv.ParseUint(fields[5], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[3], err)
		}

		stats.ReadBytes = sectorsRead * 512

		stats.WritesCompleted, err = strconv.ParseUint(fields[7], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[3], err)
		}

		sectorsWritten, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", fields[3], err)
		}

		stats.WrittenBytes = sectorsWritten * 512

		stats.Device = fields[2]
		out = append(out, stats)
	}

	return out, nil
}

func osGetFilesystemMetrics(d *Daemon) ([]metrics.FilesystemMetrics, error) {
	mounts, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("Failed to read /proc/mounts: %w", err)
	}

	out := []metrics.FilesystemMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(mounts))

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)

		if len(fields) < 3 {
			return nil, fmt.Errorf("Invalid /proc/mounts content: %q", line)
		}

		// Skip uninteresting mounts
		if slices.Contains(osMetricsExcludeFilesystems, fields[2]) || osMetricsExcludeMountpoints.MatchString(fields[1]) {
			continue
		}

		stats := metrics.FilesystemMetrics{}

		stats.Mountpoint = fields[1]

		statfs, err := linux.StatVFS(stats.Mountpoint)
		if err != nil {
			return nil, fmt.Errorf("Failed to stat %s: %w", stats.Mountpoint, err)
		}

		fsType, err := linux.FSTypeToName(int32(statfs.Type))
		if err == nil {
			stats.FSType = fsType
		}

		stats.AvailableBytes = statfs.Bavail * uint64(statfs.Bsize)
		stats.FreeBytes = statfs.Bfree * uint64(statfs.Bsize)
		stats.SizeBytes = statfs.Blocks * uint64(statfs.Bsize)

		stats.Device = fields[0]

		out = append(out, stats)
	}

	return out, nil
}

func osGetMemoryMetrics(d *Daemon) (metrics.MemoryMetrics, error) {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return metrics.MemoryMetrics{}, fmt.Errorf("Failed to read /proc/meminfo: %w", err)
	}

	out := metrics.MemoryMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(content))

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)

		if len(fields) < 2 {
			return metrics.MemoryMetrics{}, fmt.Errorf("Invalid /proc/meminfo content: %q", line)
		}

		fields[0] = strings.TrimRight(fields[0], ":")

		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return metrics.MemoryMetrics{}, fmt.Errorf("Failed to parse %q: %w", fields[1], err)
		}

		// Multiply suffix (kB)
		if len(fields) == 3 {
			value *= 1024
		}

		// FIXME: Missing RSS
		switch fields[0] {
		case "Active":
			out.ActiveBytes = value
		case "Active(anon)":
			out.ActiveAnonBytes = value
		case "Active(file)":
			out.ActiveFileBytes = value
		case "Cached":
			out.CachedBytes = value
		case "Dirty":
			out.DirtyBytes = value
		case "HugePages_Free":
			out.HugepagesFreeBytes = value
		case "HugePages_Total":
			out.HugepagesTotalBytes = value
		case "Inactive":
			out.InactiveBytes = value
		case "Inactive(anon)":
			out.InactiveAnonBytes = value
		case "Inactive(file)":
			out.InactiveFileBytes = value
		case "Mapped":
			out.MappedBytes = value
		case "MemAvailable":
			out.MemAvailableBytes = value
		case "MemFree":
			out.MemFreeBytes = value
		case "MemTotal":
			out.MemTotalBytes = value
		case "Shmem":
			out.ShmemBytes = value
		case "SwapCached":
			out.SwapBytes = value
		case "Unevictable":
			out.UnevictableBytes = value
		case "Writeback":
			out.WritebackBytes = value
		}
	}

	return out, nil
}

func osGetCPUState() api.InstanceStateCPU {
	var value []byte
	var err error
	cpu := api.InstanceStateCPU{}

	if util.PathExists("/sys/fs/cgroup/cpuacct/cpuacct.usage") {
		// CPU usage in seconds
		value, err = os.ReadFile("/sys/fs/cgroup/cpuacct/cpuacct.usage")
		if err != nil {
			cpu.Usage = -1
			return cpu
		}

		valueInt, err := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err != nil {
			cpu.Usage = -1
			return cpu
		}

		cpu.Usage = valueInt

		return cpu
	} else if util.PathExists("/sys/fs/cgroup/cpu.stat") {
		stats, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
		if err != nil {
			cpu.Usage = -1
			return cpu
		}

		scanner := bufio.NewScanner(bytes.NewReader(stats))

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			if fields[0] == "usage_usec" {
				valueInt, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					cpu.Usage = -1
					return cpu
				}

				// usec -> nsec
				cpu.Usage = valueInt * 1000
				return cpu
			}
		}
	}

	cpu.Usage = -1
	return cpu
}

func osGetMemoryState() api.InstanceStateMemory {
	memory := api.InstanceStateMemory{}

	stats, err := osGetMemoryMetrics(nil)
	if err != nil {
		return memory
	}

	memory.Usage = int64(stats.MemTotalBytes) - int64(stats.MemFreeBytes)
	memory.Total = int64(stats.MemTotalBytes)

	// Memory peak in bytes
	value, err := os.ReadFile("/sys/fs/cgroup/memory/memory.max_usage_in_bytes")
	valueInt, err1 := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
	if err == nil && err1 == nil {
		memory.UsagePeak = valueInt
	}

	return memory
}

func osGetNetworkState() map[string]api.InstanceStateNetwork {
	result := map[string]api.InstanceStateNetwork{}

	ifs, err := linux.NetlinkInterfaces()
	if err != nil {
		logger.Errorf("Failed to retrieve network interfaces: %v", err)
		return result
	}

	for _, iface := range ifs {
		network := api.InstanceStateNetwork{
			Addresses: []api.InstanceStateNetworkAddress{},
			Counters:  api.InstanceStateNetworkCounters{},
		}

		network.Hwaddr = iface.HardwareAddr.String()
		network.Mtu = iface.MTU

		if iface.Flags&net.FlagUp != 0 {
			network.State = "up"
		} else {
			network.State = "down"
		}

		if iface.Flags&net.FlagBroadcast != 0 {
			network.Type = "broadcast"
		} else if iface.Flags&net.FlagLoopback != 0 {
			network.Type = "loopback"
		} else if iface.Flags&net.FlagPointToPoint != 0 {
			network.Type = "point-to-point"
		} else {
			network.Type = "unknown"
		}

		// Counters
		value, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/statistics/tx_bytes", iface.Name))
		valueInt, err1 := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err == nil && err1 == nil {
			network.Counters.BytesSent = valueInt
		}

		value, err = os.ReadFile(fmt.Sprintf("/sys/class/net/%s/statistics/rx_bytes", iface.Name))
		valueInt, err1 = strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err == nil && err1 == nil {
			network.Counters.BytesReceived = valueInt
		}

		value, err = os.ReadFile(fmt.Sprintf("/sys/class/net/%s/statistics/tx_packets", iface.Name))
		valueInt, err1 = strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err == nil && err1 == nil {
			network.Counters.PacketsSent = valueInt
		}

		value, err = os.ReadFile(fmt.Sprintf("/sys/class/net/%s/statistics/rx_packets", iface.Name))
		valueInt, err1 = strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err == nil && err1 == nil {
			network.Counters.PacketsReceived = valueInt
		}

		// Addresses
		for _, addr := range iface.Addresses {
			addressFields := strings.Split(addr.String(), "/")

			networkAddress := api.InstanceStateNetworkAddress{
				Address: addressFields[0],
				Netmask: addressFields[1],
			}

			scope := "global"
			if strings.HasPrefix(addressFields[0], "127") {
				scope = "local"
			}

			if addressFields[0] == "::1" {
				scope = "local"
			}

			if strings.HasPrefix(addressFields[0], "169.254") {
				scope = "link"
			}

			if strings.HasPrefix(addressFields[0], "fe80:") {
				scope = "link"
			}

			networkAddress.Scope = scope

			if strings.Contains(addressFields[0], ":") {
				networkAddress.Family = "inet6"
			} else {
				networkAddress.Family = "inet"
			}

			network.Addresses = append(network.Addresses, networkAddress)
		}

		result[iface.Name] = network
	}

	return result
}

func osGetProcessesState() int64 {
	pids := []int64{1}

	// Go through the pid list, adding new pids at the end so we go through them all.
	for i := range pids {
		fname := fmt.Sprintf("/proc/%d/task/%d/children", pids[i], pids[i])
		fcont, err := os.ReadFile(fname)
		if err != nil {
			// The process terminated during execution of this loop.
			continue
		}

		content := strings.Split(string(fcont), " ")
		for j := range content {
			pid, err := strconv.ParseInt(content[j], 10, 64)
			if err == nil {
				pids = append(pids, pid)
			}
		}
	}

	return int64(len(pids))
}

func osGetOSState() *api.InstanceStateOSInfo {
	osInfo := &api.InstanceStateOSInfo{}

	// Get information about the OS.
	lsbRelease, err := osarch.GetOSRelease()
	if err == nil {
		osInfo.OS = lsbRelease["NAME"]
		osInfo.OSVersion = lsbRelease["VERSION_ID"]
	}

	// Get information about the kernel version.
	uname, err := linux.Uname()
	if err == nil {
		osInfo.KernelVersion = uname.Release
	}

	// Get the hostname.
	hostname, err := os.Hostname()
	if err == nil {
		osInfo.Hostname = hostname
	}

	// Get the FQDN. To avoid needing to run `hostname -f`, do a reverse host lookup for 127.0.1.1, and if found, return the first hostname as the FQDN.
	ctx, cancel := context.WithTimeout(context.TODO(), 100*time.Millisecond)
	defer cancel()

	var r net.Resolver
	fqdn, err := r.LookupAddr(ctx, "127.0.0.1")
	if err == nil && len(fqdn) > 0 {
		// Take the first returned hostname and trim the trailing dot.
		osInfo.FQDN = strings.TrimSuffix(fqdn[0], ".")
	}

	return osInfo
}

// osReconfigureNetworkInterfaces checks for the existence of files under NICConfigDir in the config share.
// Each file is named <device>.json and contains the Device Name, NIC Name, MTU and MAC address.
func osReconfigureNetworkInterfaces() {
	nicDirEntries, err := os.ReadDir(deviceConfig.NICConfigDir)
	if err != nil {
		// Abort if configuration folder does not exist (nothing to do), otherwise log and return.
		if errors.Is(err, fs.ErrNotExist) {
			return
		}

		logger.Error("Could not read network interface configuration directory", logger.Ctx{"err": err})
		return
	}

	// Attempt to load the virtio_net driver in case it's not be loaded yet.
	_ = linux.LoadModule("virtio_net")

	// nicData is a map of MAC address to NICConfig.
	nicData := make(map[string]deviceConfig.NICConfig, len(nicDirEntries))

	for _, f := range nicDirEntries {
		nicBytes, err := os.ReadFile(filepath.Join(deviceConfig.NICConfigDir, f.Name()))
		if err != nil {
			logger.Error("Could not read network interface configuration file", logger.Ctx{"err": err})
		}

		var conf deviceConfig.NICConfig
		err = json.Unmarshal(nicBytes, &conf)
		if err != nil {
			logger.Error("Could not parse network interface configuration file", logger.Ctx{"err": err})
			return
		}

		if conf.MACAddress != "" {
			nicData[conf.MACAddress] = conf
		}
	}

	// configureNIC applies any config specified for the interface based on its current MAC address.
	configureNIC := func(currentNIC net.Interface) error {
		reverter := revert.New()
		defer reverter.Fail()

		// Look for a NIC config entry for this interface based on its MAC address.
		nic, ok := nicData[currentNIC.HardwareAddr.String()]
		if !ok {
			return nil
		}

		var changeName, changeMTU bool
		if nic.NICName != "" && currentNIC.Name != nic.NICName {
			changeName = true
		}

		if nic.MTU > 0 && currentNIC.MTU != int(nic.MTU) {
			changeMTU = true
		}

		if !changeName && !changeMTU {
			return nil // Nothing to do.
		}

		link := ip.Link{
			Name: currentNIC.Name,
			MTU:  uint32(currentNIC.MTU),
		}

		err := link.SetDown()
		if err != nil {
			return err
		}

		reverter.Add(func() {
			_ = link.SetUp()
		})

		// Apply the name from the NIC config if needed.
		if changeName {
			err = link.SetName(nic.NICName)
			if err != nil {
				return err
			}

			reverter.Add(func() {
				err := link.SetName(currentNIC.Name)
				if err != nil {
					return
				}

				link.Name = currentNIC.Name
			})

			link.Name = nic.NICName
		}

		// Apply the MTU from the NIC config if needed.
		if changeMTU {
			err = link.SetMTU(nic.MTU)
			if err != nil {
				return err
			}

			link.MTU = nic.MTU

			reverter.Add(func() {
				err := link.SetMTU(uint32(currentNIC.MTU))
				if err != nil {
					return
				}

				link.MTU = uint32(currentNIC.MTU)
			})
		}

		err = link.SetUp()
		if err != nil {
			return err
		}

		reverter.Success()
		return nil
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		logger.Error("Unable to read network interfaces", logger.Ctx{"err": err})
	}

	for _, iface := range ifaces {
		err = configureNIC(iface)
		if err != nil {
			logger.Error("Unable to reconfigure network interface", logger.Ctx{"interface": iface.Name, "err": err})
		}
	}
}

func osGetInteractiveConsole(s *execWs) (*os.File, *os.File, error) {
	pty, tty, err := linux.OpenPty(int64(s.uid), int64(s.gid))
	if err != nil {
		return nil, nil, err
	}

	if s.width > 0 && s.height > 0 {
		_ = linux.SetPtySize(int(pty.Fd()), s.width, s.height)
	}

	return pty, tty, nil
}

func osPrepareExecCommand(s *execWs, cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: s.uid,
			Gid: s.gid,
		},
		// Creates a new session if the calling process is not a process group leader.
		// The calling process is the leader of the new session, the process group leader of
		// the new process group, and has no controlling terminal.
		// This is important to allow remote shells to handle ctrl+c.
		Setsid: true,
	}

	// Make the given terminal the controlling terminal of the calling process.
	// The calling process must be a session leader and not have a controlling terminal already.
	// This is important as allows ctrl+c to work as expected for non-shell programs.
	if s.interactive {
		cmd.SysProcAttr.Setctty = true
	}
}

func osHandleExecControl(control api.InstanceExecControl, s *execWs, pty io.ReadWriteCloser, cmd *exec.Cmd, l logger.Logger) {
	if control.Command == "window-resize" && s.interactive {
		winchWidth, err := strconv.Atoi(control.Args["width"])
		if err != nil {
			l.Debug("Unable to extract window width", logger.Ctx{"err": err})
			return
		}

		winchHeight, err := strconv.Atoi(control.Args["height"])
		if err != nil {
			l.Debug("Unable to extract window height", logger.Ctx{"err": err})
			return
		}

		osFile, ok := pty.(*os.File)
		if ok {
			err = linux.SetPtySize(int(osFile.Fd()), winchWidth, winchHeight)
			if err != nil {
				l.Debug("Failed to set window size", logger.Ctx{"err": err, "width": winchWidth, "height": winchHeight})
				return
			}
		}
	} else if control.Command == "signal" {
		err := unix.Kill(cmd.Process.Pid, unix.Signal(control.Signal))
		if err != nil {
			l.Debug("Failed forwarding signal", logger.Ctx{"err": err, "signal": control.Signal})
			return
		}

		l.Info("Forwarded signal", logger.Ctx{"signal": control.Signal})
	}
}

func osExecWrapper(ctx context.Context, pty io.ReadWriteCloser) io.ReadWriteCloser {
	osFile, ok := pty.(*os.File)
	if !ok {
		return pty
	}

	return linux.NewExecWrapper(ctx, osFile)
}

func osGetListener(port int64) (net.Listener, error) {
	const CIDAny uint32 = 4294967295 // Equivalent to VMADDR_CID_ANY.

	// Setup the listener on wildcard CID for inbound connections from Incus.
	// We use the VMADDR_CID_ANY CID so that if the VM's CID changes in the future the listener still works.
	// A CID change can occur when restoring a stateful VM that was previously using one CID but is
	// subsequently restored using a different one.
	l, err := vsock.ListenContextID(CIDAny, ports.HTTPSDefaultPort, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to listen on vsock: %w", err)
	}

	logger.Info("Started vsock listener")

	return l, nil
}

func osSetEnv(post *api.InstanceExecPost, env map[string]string) {
	// Set default value for PATH.
	_, ok := env["PATH"]
	if !ok {
		env["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}

	if util.PathExists("/snap/bin") {
		env["PATH"] = fmt.Sprintf("%s:/snap/bin", env["PATH"])
	}

	// If running as root, set some env variables.
	if post.User == 0 {
		// Set default value for HOME.
		_, ok = env["HOME"]
		if !ok {
			env["HOME"] = "/root"
		}

		// Set default value for USER.
		_, ok = env["USER"]
		if !ok {
			env["USER"] = "root"
		}
	}

	// Set default value for LANG.
	_, ok = env["LANG"]
	if !ok {
		env["LANG"] = "C.UTF-8"
	}

	// Set the default working directory.
	if post.Cwd == "" {
		post.Cwd = env["HOME"]
		if post.Cwd == "" {
			post.Cwd = "/"
		}
	}
}
