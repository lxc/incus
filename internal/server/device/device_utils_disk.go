package device

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/instance"
	storageDrivers "github.com/lxc/incus/v6/internal/server/storage/drivers"
	"github.com/lxc/incus/v6/shared/idmap"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

// RBDFormatPrefix is the prefix used in disk paths to identify RBD.
const RBDFormatPrefix = "rbd"

// RBDFormatSeparator is the field separate used in disk paths for RBD devices.
const RBDFormatSeparator = " "

// DiskParseRBDFormat parses an rbd formatted string, and returns the pool name, volume name, and list of options.
func DiskParseRBDFormat(rbd string) (string, string, []string, error) {
	if !strings.HasPrefix(rbd, fmt.Sprintf("%s%s", RBDFormatPrefix, RBDFormatSeparator)) {
		return "", "", nil, fmt.Errorf("Invalid rbd format, missing prefix")
	}

	fields := strings.SplitN(rbd, RBDFormatSeparator, 3)
	if len(fields) != 3 {
		return "", "", nil, fmt.Errorf("Invalid rbd format, invalid number of fields")
	}

	opts := fields[2]

	fields = strings.SplitN(fields[1], "/", 2)
	if len(fields) != 2 {
		return "", "", nil, fmt.Errorf("Invalid rbd format, invalid pool or volume")
	}

	return fields[0], fields[1], strings.Split(opts, ":"), nil
}

// DiskGetRBDFormat returns a rbd formatted string with the given values.
func DiskGetRBDFormat(clusterName string, userName string, poolName string, volumeName string) string {
	// Configuration values containing :, @, or = can be escaped with a leading \ character.
	// According to https://docs.ceph.com/docs/hammer/rbd/qemu-rbd/#usage
	optEscaper := strings.NewReplacer(":", `\:`, "@", `\@`, "=", `\=`)
	opts := []string{
		fmt.Sprintf("id=%s", optEscaper.Replace(userName)),
		fmt.Sprintf("pool=%s", optEscaper.Replace(poolName)),
		fmt.Sprintf("conf=/etc/ceph/%s.conf", optEscaper.Replace(clusterName)),
	}

	return fmt.Sprintf("%s%s%s/%s%s%s", RBDFormatPrefix, RBDFormatSeparator, optEscaper.Replace(poolName), optEscaper.Replace(volumeName), RBDFormatSeparator, strings.Join(opts, ":"))
}

// BlockFsDetect detects the type of block device.
func BlockFsDetect(dev string) (string, error) {
	out, err := subprocess.RunCommand("blkid", "-s", "TYPE", "-o", "value", dev)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// IsBlockdev returns boolean indicating whether device is block type.
func IsBlockdev(path string) bool {
	// Get a stat struct from the provided path
	stat := unix.Stat_t{}
	err := unix.Stat(path, &stat)
	if err != nil {
		return false
	}

	// Check if it's a block device
	if stat.Mode&unix.S_IFMT == unix.S_IFBLK {
		return true
	}

	// Not a device
	return false
}

// DiskMount mounts a disk device.
func DiskMount(srcPath string, dstPath string, recursive bool, propagation string, mountOptions []string, fsName string) error {
	var err error

	flags, mountOptionsStr := linux.ResolveMountOptions(mountOptions)

	var readonly bool
	if slices.Contains(mountOptions, "ro") {
		readonly = true
	}

	// Detect the filesystem
	if fsName == "none" {
		flags |= unix.MS_BIND
	}

	if propagation != "" {
		switch propagation {
		case "private":
			flags |= unix.MS_PRIVATE
		case "shared":
			flags |= unix.MS_SHARED
		case "slave":
			flags |= unix.MS_SLAVE
		case "unbindable":
			flags |= unix.MS_UNBINDABLE
		case "rprivate":
			flags |= unix.MS_PRIVATE | unix.MS_REC
		case "rshared":
			flags |= unix.MS_SHARED | unix.MS_REC
		case "rslave":
			flags |= unix.MS_SLAVE | unix.MS_REC
		case "runbindable":
			flags |= unix.MS_UNBINDABLE | unix.MS_REC
		default:
			return fmt.Errorf("Invalid propagation mode %q", propagation)
		}
	}

	if recursive {
		flags |= unix.MS_REC
	}

	// Mount the filesystem
	err = unix.Mount(srcPath, dstPath, fsName, uintptr(flags), mountOptionsStr)
	if err != nil {
		return fmt.Errorf("Unable to mount %q at %q with filesystem %q: %w", srcPath, dstPath, fsName, err)
	}

	// Remount bind mounts in readonly mode if requested
	if readonly && flags&unix.MS_BIND == unix.MS_BIND {
		flags = unix.MS_RDONLY | unix.MS_BIND | unix.MS_REMOUNT
		err = unix.Mount("", dstPath, fsName, uintptr(flags), "")
		if err != nil {
			return fmt.Errorf("Unable to mount %q in readonly mode: %w", dstPath, err)
		}
	}

	flags = unix.MS_REC | unix.MS_SLAVE
	err = unix.Mount("", dstPath, "", uintptr(flags), "")
	if err != nil {
		return fmt.Errorf("Unable to make mount %q private: %w", dstPath, err)
	}

	return nil
}

// DiskMountClear unmounts and removes the mount path used for disk shares.
func DiskMountClear(mntPath string) error {
	if util.PathExists(mntPath) {
		if linux.IsMountPoint(mntPath) {
			err := storageDrivers.TryUnmount(mntPath, 0)
			if err != nil {
				return fmt.Errorf("Failed unmounting %q: %w", mntPath, err)
			}
		}

		err := os.Remove(mntPath)
		if err != nil {
			return fmt.Errorf("Failed removing %q: %w", mntPath, err)
		}
	}

	return nil
}

func diskCephRbdMap(clusterName string, userName string, poolName string, volumeName string) (string, error) {
	devPath, err := subprocess.RunCommand(
		"rbd",
		"--id", userName,
		"--cluster", clusterName,
		"--pool", poolName,
		"map",
		volumeName)
	if err != nil {
		return "", err
	}

	idx := strings.Index(devPath, "/dev/rbd")
	if idx < 0 {
		return "", fmt.Errorf("Failed to detect mapped device path")
	}

	devPath = devPath[idx:]
	return strings.TrimSpace(devPath), nil
}

func diskCephRbdUnmap(deviceName string) error {
	unmapImageName := deviceName
	busyCount := 0
again:
	_, err := subprocess.RunCommand(
		"rbd",
		"unmap",
		unmapImageName)
	if err != nil {
		runError, ok := err.(subprocess.RunError)
		if ok {
			exitError, ok := runError.Unwrap().(*exec.ExitError)
			if ok {
				if exitError.ExitCode() == 22 {
					// EINVAL (already unmapped)
					return nil
				}

				if exitError.ExitCode() == 16 {
					// EBUSY (currently in use)
					busyCount++
					if busyCount == 10 {
						return err
					}

					// Wait a second an try again
					time.Sleep(time.Second)
					goto again
				}
			}
		}

		return err
	}

	goto again
}

// diskCephfsOptions returns the mntSrcPath and fsOptions to use for mounting a cephfs share.
func diskCephfsOptions(clusterName string, userName string, fsName string, fsPath string) (string, []string, error) {
	// Get the monitor list.
	monAddresses, err := storageDrivers.CephMonitors(clusterName)
	if err != nil {
		return "", nil, err
	}

	// Get the keyring entry.
	secret, err := storageDrivers.CephKeyring(clusterName, userName)
	if err != nil {
		return "", nil, err
	}

	// Prepare mount entry.
	fsOptions := []string{
		fmt.Sprintf("name=%v", userName),
		fmt.Sprintf("secret=%v", secret),
		fmt.Sprintf("mds_namespace=%v", fsName),
	}

	srcPath := strings.Join(monAddresses, ",") + ":/" + fsPath
	return srcPath, fsOptions, nil
}

// diskAddRootUserNSEntry takes a set of idmap entries, and adds host -> userns root uid/gid mappings if needed.
// Returns the supplied idmap entries with any added root entries.
func diskAddRootUserNSEntry(idmaps []idmap.Entry, hostRootID int64) []idmap.Entry {
	needsNSUIDRootEntry := true
	needsNSGIDRootEntry := true

	for _, idmap := range idmaps {
		// Check if the idmap entry contains the userns root user.
		if idmap.NSID == 0 {
			if idmap.IsUID {
				needsNSUIDRootEntry = false // Root UID mapping already present.
			}

			if idmap.IsGID {
				needsNSGIDRootEntry = false // Root GID mapping already present.
			}

			if !needsNSUIDRootEntry && needsNSGIDRootEntry {
				break // If we've found a root entry for UID and GID then we don't need to add one.
			}
		}
	}

	// Add UID/GID/both mapping entry if needed.
	if needsNSUIDRootEntry || needsNSGIDRootEntry {
		idmaps = append(idmaps, idmap.Entry{
			HostID:   hostRootID,
			IsUID:    needsNSUIDRootEntry,
			IsGID:    needsNSGIDRootEntry,
			NSID:     0,
			MapRange: 1,
		})
	}

	return idmaps
}

// DiskVMVirtfsProxyStart starts a new virtfs-proxy-helper process.
// If the idmaps slice is supplied then the proxy process is run inside a user namespace using the supplied maps.
// Returns a file handle to the proxy process and a revert fail function that can be used to undo this function if
// a subsequent step fails,.
func DiskVMVirtfsProxyStart(execPath string, pidPath string, sharePath string, idmaps []idmap.Entry) (*os.File, revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Locate virtfs-proxy-helper.
	cmd, err := exec.LookPath("virtfs-proxy-helper")
	if err != nil {
		if util.PathExists("/usr/lib/qemu/virtfs-proxy-helper") {
			cmd = "/usr/lib/qemu/virtfs-proxy-helper"
		} else if util.PathExists("/usr/libexec/virtfs-proxy-helper") {
			cmd = "/usr/libexec/virtfs-proxy-helper"
		}
	}

	if cmd == "" {
		return nil, nil, fmt.Errorf(`Required binary "virtfs-proxy-helper" couldn't be found`)
	}

	listener, err := net.Listen("unix", "")
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create unix listener for virtfs-proxy-helper: %w", err)
	}

	defer func() { _ = listener.Close() }()

	cDial, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to connect to virtfs-proxy-helper unix listener: %w", err)
	}

	defer func() { _ = cDial.Close() }()

	cDialUnix, ok := cDial.(*net.UnixConn)
	if !ok {
		return nil, nil, fmt.Errorf("Dialled virtfs-proxy-helper connection isn't unix socket")
	}

	defer func() { _ = cDialUnix.Close() }()

	cDialUnixFile, err := cDialUnix.File()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed getting virtfs-proxy-helper unix dialed file: %w", err)
	}

	revert.Add(func() { _ = cDialUnixFile.Close() })

	cAccept, err := listener.Accept()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to accept connection to virtfs-proxy-helper unix listener: %w", err)
	}

	defer func() { _ = cAccept.Close() }()

	cAcceptUnix, ok := cAccept.(*net.UnixConn)
	if !ok {
		return nil, nil, fmt.Errorf("Accepted virtfs-proxy-helper connection isn't unix socket")
	}

	defer func() { _ = cAcceptUnix.Close() }()

	acceptFile, err := cAcceptUnix.File()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed getting virtfs-proxy-helper unix listener file: %w", err)
	}

	defer func() { _ = acceptFile.Close() }()

	// Start the virtfs-proxy-helper process in non-daemon mode and as root so that when the VM process is
	// started as an unprivileged user, we can still share directories that process cannot access.
	args := []string{"--nodaemon", "--fd", "3", "--path", sharePath}
	proc, err := subprocess.NewProcess(cmd, args, "", "")
	if err != nil {
		return nil, nil, err
	}

	if len(idmaps) > 0 {
		idmapSet := &idmap.Set{Entries: idmaps}
		proc.SetUserns(idmapSet.ToUIDMappings(), idmapSet.ToGIDMappings())
	}

	err = proc.StartWithFiles(context.Background(), []*os.File{acceptFile})
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to start virtfs-proxy-helper: %w", err)
	}

	revert.Add(func() { _ = proc.Stop() })

	err = proc.Save(pidPath)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to save virtfs-proxy-helper state: %w", err)
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return cDialUnixFile, cleanup, err
}

// DiskVMVirtfsProxyStop stops the virtfs-proxy-helper process.
func DiskVMVirtfsProxyStop(pidPath string) error {
	if util.PathExists(pidPath) {
		proc, err := subprocess.ImportProcess(pidPath)
		if err != nil {
			return err
		}

		err = proc.Stop()
		if err != nil && err != subprocess.ErrNotRunning {
			return err
		}

		// Remove PID file.
		_ = os.Remove(pidPath)
	}

	return nil
}

// DiskVMVirtiofsdStart starts a new virtiofsd process.
// If the idmaps slice is supplied then the proxy process is run inside a user namespace using the supplied maps.
// Returns UnsupportedError error if the host system or instance does not support virtiosfd, returns normal error
// type if process cannot be started for other reasons.
// Returns revert function and listener file handle on success.
func DiskVMVirtiofsdStart(execPath string, inst instance.Instance, socketPath string, pidPath string, logPath string, sharePath string, idmaps []idmap.Entry) (func(), net.Listener, error) {
	revert := revert.New()
	defer revert.Fail()

	if !filepath.IsAbs(sharePath) {
		return nil, nil, fmt.Errorf("Share path not absolute: %q", sharePath)
	}

	// Remove old socket if needed.
	_ = os.Remove(socketPath)

	// Locate virtiofsd.
	cmd, err := exec.LookPath("virtiofsd")
	if err != nil {
		if util.PathExists("/usr/lib/qemu/virtiofsd") {
			cmd = "/usr/lib/qemu/virtiofsd"
		} else if util.PathExists("/usr/libexec/virtiofsd") {
			cmd = "/usr/libexec/virtiofsd"
		} else if util.PathExists("/usr/lib/virtiofsd") {
			cmd = "/usr/lib/virtiofsd"
		}
	}

	if cmd == "" {
		return nil, nil, ErrMissingVirtiofsd
	}

	// Currently, virtiofs is broken on at least the ARM architecture.
	// We therefore restrict virtiofs to 64BIT_INTEL_X86.
	if inst.Architecture() != osarch.ARCH_64BIT_INTEL_X86 {
		return nil, nil, UnsupportedError{msg: "Architecture unsupported"}
	}

	if util.IsTrue(inst.ExpandedConfig()["migration.stateful"]) {
		return nil, nil, UnsupportedError{"Stateful migration unsupported"}
	}

	if util.IsTrue(inst.ExpandedConfig()["security.sev"]) || util.IsTrue(inst.ExpandedConfig()["security.sev.policy.es"]) {
		return nil, nil, UnsupportedError{"SEV unsupported"}
	}

	// Trickery to handle paths > 107 chars.
	socketFileDir, err := os.Open(filepath.Dir(socketPath))
	if err != nil {
		return nil, nil, err
	}

	defer func() { _ = socketFileDir.Close() }()

	socketFile := fmt.Sprintf("/proc/self/fd/%d/%s", socketFileDir.Fd(), filepath.Base(socketPath))

	listener, err := net.Listen("unix", socketFile)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create unix listener for virtiofsd: %w", err)
	}

	revert.Add(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})

	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		return nil, nil, fmt.Errorf("Failed getting UnixListener for virtiofsd")
	}

	unixFile, err := unixListener.File()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to getting unix listener file for virtiofsd: %w", err)
	}

	defer func() { _ = unixFile.Close() }()

	// Start the virtiofsd process in non-daemon mode.
	args := []string{"--fd=3", "--cache=never", "-o", fmt.Sprintf("source=%s", sharePath)}
	proc, err := subprocess.NewProcess(cmd, args, logPath, logPath)
	if err != nil {
		return nil, nil, err
	}

	if len(idmaps) > 0 {
		idmapSet := &idmap.Set{Entries: idmaps}
		proc.SetUserns(idmapSet.ToUIDMappings(), idmapSet.ToGIDMappings())
	}

	err = proc.StartWithFiles(context.Background(), []*os.File{unixFile})
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to start virtiofsd: %w", err)
	}

	revert.Add(func() { _ = proc.Stop() })

	err = proc.Save(pidPath)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to save virtiofsd state: %w", err)
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, listener, err
}

// DiskVMVirtiofsdStop stops an existing virtiofsd process and cleans up.
func DiskVMVirtiofsdStop(socketPath string, pidPath string) error {
	if util.PathExists(pidPath) {
		proc, err := subprocess.ImportProcess(pidPath)
		if err != nil {
			return err
		}

		err = proc.Stop()
		// The virtiofsd process will terminate automatically once the VM has stopped.
		// We therefore should only return an error if it's still running and fails to stop.
		if err != nil && err != subprocess.ErrNotRunning {
			return err
		}

		// Remove PID file if needed.
		err = os.Remove(pidPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("Failed to remove PID file: %w", err)
		}
	}

	// Remove socket file if needed.
	err := os.Remove(socketPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Failed to remove socket file: %w", err)
	}

	return nil
}
