package drivers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/internal/linux"
	"github.com/lxc/incus/v7/internal/linux/nbd"
	internalUtil "github.com/lxc/incus/v7/internal/util"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/subprocess"
	"github.com/lxc/incus/v7/shared/util"
)

// nbdStateDir returns the directory holding the qemu-nbd sockets and pid files.
func nbdStateDir() string {
	return internalUtil.RunPath("nbd")
}

// nbdPidFile returns the pid file of the qemu-nbd server behind an NBD device.
func nbdPidFile(nbdPath string) string {
	return filepath.Join(nbdStateDir(), filepath.Base(nbdPath)+".pid")
}

// nbdBackend returns the backend identifier the kernel holds for an NBD device.
// It is empty for devices attached by qemu-nbd itself (older Incus) and for unused devices.
func nbdBackend(nbdPath string) string {
	data, err := os.ReadFile(fmt.Sprintf("/sys/class/block/%s/backend", filepath.Base(nbdPath)))
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}

// nbdOwnerPid returns the process the kernel records as driving an NBD device, 0 when unused.
func nbdOwnerPid(nbdPath string) int {
	data, err := os.ReadFile(fmt.Sprintf("/sys/class/block/%s/pid", filepath.Base(nbdPath)))
	if err != nil {
		return 0
	}

	tid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}

	// The kernel records the thread driving the device, get the process it belongs to.
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", tid))
	if err != nil {
		return tid
	}

	for line := range strings.SplitSeq(string(status), "\n") {
		tgid, found := strings.CutPrefix(line, "Tgid:")
		if found {
			pid, err := strconv.Atoi(strings.TrimSpace(tgid))
			if err == nil {
				return pid
			}
		}
	}

	return tid
}

// qemuNbdArgs returns the command line of pid if it is a qemu-nbd process.
func qemuNbdArgs(pid int) []string {
	if pid <= 0 {
		return nil
	}

	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil
	}

	args := strings.Split(strings.TrimSuffix(string(cmdline), "\x00"), "\x00")
	if len(args) == 0 || filepath.Base(args[0]) != "qemu-nbd" {
		return nil
	}

	return args
}

// readPidFile returns the pid recorded in path, 0 if there is none.
func readPidFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}

	return pid
}

// qemuNbdServerPid returns the qemu-nbd process serving an NBD device, 0 if it cannot be found.
func qemuNbdServerPid(nbdPath string) int {
	// Servers started by this Incus record their pid next to their socket.
	pid := readPidFile(nbdPidFile(nbdPath))
	if qemuNbdArgs(pid) != nil {
		return pid
	}

	// Devices attached by qemu-nbd itself report it as their owner.
	backend := nbdBackend(nbdPath)
	if backend == "" {
		pid = nbdOwnerPid(nbdPath)
		if qemuNbdArgs(pid) != nil {
			return pid
		}

		return 0
	}

	// Fall back to the server exporting the device's backend.
	pids := qemuNbdServersFor(backend)
	if len(pids) > 0 {
		return pids[0]
	}

	return 0
}

// qemuNbdServersFor returns the pids of the qemu-nbd processes exporting the given backend.
func qemuNbdServersFor(backend string) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	pids := []int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		if slices.Contains(qemuNbdArgs(pid), backend) {
			pids = append(pids, pid)
		}
	}

	return pids
}

// waitProcessExit waits for a process to be gone.
func waitProcessExit(pid int, what string) error {
	waitUntil := time.Now().Add(30 * time.Second)
	for util.PathExists(fmt.Sprintf("/proc/%d", pid)) {
		if time.Now().After(waitUntil) {
			return fmt.Errorf("Timed out waiting for qemu-nbd to release %q", what)
		}

		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

// startQemuNbdServer starts a qemu-nbd server for devPath on a new unix socket.
// It returns the socket and pid file paths.
func startQemuNbdServer(devPath string, format string, detectZeroes string, readOnly bool) (string, string, error) {
	err := os.MkdirAll(nbdStateDir(), 0o700)
	if err != nil {
		return "", "", err
	}

	stateDir, err := os.Open(nbdStateDir())
	if err != nil {
		return "", "", err
	}

	defer func() { _ = stateDir.Close() }()

	id := uuid.New().String()
	sockPath := filepath.Join(nbdStateDir(), id+".sock")
	pidPath := filepath.Join(nbdStateDir(), id+".pid")

	// The socket is reached through an inherited directory fd to stay under the unix socket path length limit.
	args := []string{"--fork", "--socket=/proc/self/fd/3/" + id + ".sock", "--pid-file=" + pidPath}
	if detectZeroes != "" {
		if detectZeroes == "unmap" {
			args = append(args, "--discard=unmap")
		}

		args = append(args, "--detect-zeroes="+detectZeroes)
	}

	if format != "" {
		args = append(args, "--format="+format)
	}

	if readOnly {
		args = append(args, "--read-only")
	}

	args = append(args, devPath)

	_, err = subprocess.RunCommandInheritFds(context.Background(), []*os.File{stateDir}, "qemu-nbd", args...)
	if err != nil {
		return "", "", err
	}

	return sockPath, pidPath, nil
}

// killQemuNbdServer terminates a qemu-nbd server that never got attached to a device.
func killQemuNbdServer(pidPath string) {
	pid := readPidFile(pidPath)
	if qemuNbdArgs(pid) != nil {
		_ = unix.Kill(pid, unix.SIGTERM)
	}

	_ = os.Remove(pidPath)
}

// attachQemuNbd negotiates with the qemu-nbd server at sockPath and hands the connection to the kernel.
// A negative index allocates a new device, otherwise the given existing device is used.
func attachQemuNbd(sockPath string, backend string, index int) (int, error) {
	// Dial through a directory fd to stay under the unix socket path length limit.
	sockDir, err := os.Open(filepath.Dir(sockPath))
	if err != nil {
		return -1, err
	}

	defer func() { _ = sockDir.Close() }()

	shortPath := fmt.Sprintf("/proc/self/fd/%d/%s", sockDir.Fd(), filepath.Base(sockPath))

	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: shortPath, Net: "unix"})
	if err != nil {
		return -1, fmt.Errorf("Failed connecting to qemu-nbd: %w", err)
	}

	defer func() { _ = conn.Close() }()

	export, err := nbd.Negotiate(conn)
	if err != nil {
		return -1, err
	}

	// A device being re-attached stays busy until whoever probed it after the disconnect has closed it.
	waitUntil := time.Now().Add(10 * time.Second)
	for {
		index, err = nbd.Connect(conn, export, backend, index)
		if err == nil || !errors.Is(err, unix.EBUSY) || time.Now().After(waitUntil) {
			return index, err
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// ConnectQemuNbd exports devPath through qemu-nbd and attaches it to a newly allocated NBD device.
func ConnectQemuNbd(devPath string, format string, detectZeroes string, readOnly bool) (string, error) {
	err := linux.LoadModule("nbd")
	if err != nil {
		return "", fmt.Errorf("Error loading %q module: %w", "nbd", err)
	}

	sockPath, pidPath, err := startQemuNbdServer(devPath, format, detectZeroes, readOnly)
	if err != nil {
		return "", err
	}

	index, err := attachQemuNbd(sockPath, devPath, -1)
	if err != nil {
		killQemuNbdServer(pidPath)
		return "", err
	}

	nbdPath := fmt.Sprintf("/dev/nbd%d", index)

	// Key the pid file by device so the server can be found again later.
	err = os.Rename(pidPath, nbdPidFile(nbdPath))
	if err != nil {
		_ = DisconnectQemuNbd(nbdPath)
		killQemuNbdServer(pidPath)
		return "", err
	}

	// It can take a little while before the /dev/nbdX node shows up.
	for range 20 {
		if util.PathExists(nbdPath) {
			return nbdPath, nil
		}

		time.Sleep(100 * time.Millisecond)
	}

	_ = DisconnectQemuNbdWait(nbdPath)
	return "", fmt.Errorf("NBD device %q didn't appear after 2s", nbdPath)
}

// nbdIndex returns the device index of an NBD device path.
func nbdIndex(nbdPath string) (int, error) {
	index, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(nbdPath), "nbd"))
	if err != nil {
		return -1, fmt.Errorf("Invalid NBD device %q", nbdPath)
	}

	return index, nil
}

// ReconnectQemuNbd re-exports a volume on the NBD device already serving it, so that size changes are picked up.
func ReconnectQemuNbd(nbdPath string, devPath string, format string, detectZeroes string, readOnly bool) error {
	index, err := nbdIndex(nbdPath)
	if err != nil {
		return err
	}

	err = DisconnectQemuNbdWait(nbdPath)
	if err != nil {
		return err
	}

	sockPath, pidPath, err := startQemuNbdServer(devPath, format, detectZeroes, readOnly)
	if err != nil {
		return err
	}

	_, err = attachQemuNbd(sockPath, devPath, index)
	if err != nil {
		killQemuNbdServer(pidPath)
		return err
	}

	return os.Rename(pidPath, nbdPidFile(nbdPath))
}

// DisconnectQemuNbdWait disconnects the NBD device at nbdPath and waits for its qemu-nbd process to exit.
func DisconnectQemuNbdWait(nbdPath string) error {
	if nbdOwnerPid(nbdPath) == 0 {
		// Not connected.
		return nil
	}

	pid := qemuNbdServerPid(nbdPath)

	err := DisconnectQemuNbd(nbdPath)
	if err != nil {
		return err
	}

	if pid == 0 {
		return nil
	}

	// The backend is only fully released once qemu-nbd has exited.
	return waitProcessExit(pid, nbdPath)
}

// DisconnectQemuNbd disconnects the NBD device at nbdPath.
func DisconnectQemuNbd(nbdPath string) error {
	// Dirty pages are only written back on the last close of the device, so flush them while the server is still there.
	f, err := os.Open(nbdPath)
	if err == nil {
		err = f.Sync()
		_ = f.Close()
	}

	if err != nil {
		logger.Warn("Failed flushing NBD device", logger.Ctx{"dev": nbdPath, "err": err})
	}

	// Devices attached by qemu-nbd itself (older Incus) only take the ioctl interface.
	if nbdBackend(nbdPath) == "" {
		_, err = subprocess.RunCommand("qemu-nbd", "--disconnect", nbdPath)
	} else {
		var index int

		index, err = nbdIndex(nbdPath)
		if err == nil {
			err = nbd.Disconnect(index)
		}
	}

	if err != nil {
		return err
	}

	_ = os.Remove(nbdPidFile(nbdPath))

	return nil
}

// QemuNbdDevices returns the NBD devices currently serving the given qemu-nbd backend.
func QemuNbdDevices(backend string) ([]string, error) {
	entries, err := os.ReadDir("/sys/class/block")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, err
	}

	devPaths := []string{}
	for _, entry := range entries {
		// Skip anything that's not a whole NBD device.
		idxStr, found := strings.CutPrefix(entry.Name(), "nbd")
		if !found {
			continue
		}

		_, err := strconv.ParseUint(idxStr, 10, 64)
		if err != nil {
			continue
		}

		nbdPath := "/dev/" + entry.Name()

		// Devices attached by Incus carry their backend, those attached by qemu-nbd itself are matched through it.
		devBackend := nbdBackend(nbdPath)
		if devBackend == backend || (devBackend == "" && slices.Contains(qemuNbdArgs(nbdOwnerPid(nbdPath)), backend)) {
			devPaths = append(devPaths, nbdPath)
		}
	}

	return devPaths, nil
}
