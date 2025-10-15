//go:build darwin

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/shirou/gopsutil/v4/disk"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/server/metrics"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/subprocess"
)

var osBaseWorkingDirectory = "/"

func parseBytes(b []byte) string {
	n := bytes.IndexByte(b, 0)
	if n < 0 {
		n = len(b)
	}

	return string(b[:n])
}

func osGetEnvironment() (*api.ServerEnvironment, error) {
	uname := unix.Utsname{}
	err := unix.Uname(&uname)
	if err != nil {
		return nil, err
	}

	env := &api.ServerEnvironment{
		Kernel:             parseBytes(uname.Sysname[:]),
		KernelArchitecture: parseBytes(uname.Machine[:]),
		KernelVersion:      parseBytes(uname.Release[:]),
		Server:             "incus-agent",
		ServerPid:          os.Getpid(),
		ServerVersion:      version.Version,
		ServerName:         parseBytes(uname.Nodename[:]),
	}

	return env, nil
}

func osMountShared(src string, dst string, fstype string, opts []string) error {
	if fstype != "9p" {
		return errors.New("Only 9p shares are supported on Darwin")
	}

	// 9p shares behave strangely on Darwin, as they don't get mounted in the file system, but rather
	// as volumes, and have their own subcommand that doesn't conform to the mount frontend.
	// Bind mounts are not natively supported, so we have to mount the 9p share, then symlink it.

	// Convert relative mounts to absolute from / otherwise dir creation fails or mount fails.
	if !strings.HasPrefix(dst, "/") {
		dst = fmt.Sprintf("/%s", dst)
	}

	// If the path exists and is neither an empty directory nor a broken symbolic link to a volume
	// (indicating with high probability a previous share mount which we didn't clean up properly), we
	// can't safely use it.
	stat, err := os.Lstat(dst)
	if err == nil {
		if stat.IsDir() {
			// Handle directories, failing if not empty.
			entries, err := os.ReadDir(dst)
			if err != nil {
				return fmt.Errorf("Failed to open directory %s: %w", dst, err)
			}

			if len(entries) > 0 {
				return errors.New("Unable to mount shares on non-empty directories")
			}
		} else if stat.Mode()&fs.ModeSymlink != 0 {
			// Handle symbolic links, failinks if not broken.
			// Try to follow the link.
			_, err := os.Stat(dst)
			if err == nil {
				return fmt.Errorf("Unable to mount shares on working symbolic link %s", dst)
			}
		} else {
			return fmt.Errorf("Mount destination %s exists and is not empty", dst)
		}

		err = os.Remove(dst)
		if err != nil {
			return fmt.Errorf("Failed to prepare destination %s: %w", dst, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	_, err = subprocess.RunCommand("mount_9p", src)
	if err != nil {
		return err
	}

	// Volume naming is very predictable. If a disk has the same name as a 9p share, `mount_9p` fails.
	return os.Symlink("/Volumes/"+src, dst)
}

// osUmount is currently not used, but it is implemented just in case.
func osUmount(src string, dst string, fstype string) error {
	if fstype != "9p" {
		return errors.New("Only 9p shares are supported on Darwin")
	}

	// First, remove the symlink.
	err := os.Remove(dst)
	if err != nil {
		return err
	}

	// Then, unmount the share.
	_, err = subprocess.RunCommand("umount", "/Volumes/"+src)
	return err
}

func osGetFilesystemMetrics(d *Daemon) ([]metrics.FilesystemMetrics, error) {
	partitions, err := disk.Partitions(true)
	if err != nil {
		return nil, err
	}

	sort.Slice(partitions, func(i, j int) bool {
		return partitions[i].Mountpoint < partitions[j].Mountpoint
	})

	fsMetrics := make([]metrics.FilesystemMetrics, 0, len(partitions))
	for _, partition := range partitions {
		var stat syscall.Statfs_t
		err = syscall.Statfs(partition.Mountpoint, &stat)
		if err != nil {
			continue
		}

		bsize := uint64(stat.Bsize)

		fsMetrics = append(fsMetrics, metrics.FilesystemMetrics{
			Device:         partition.Device,
			Mountpoint:     partition.Mountpoint,
			FSType:         partition.Fstype,
			AvailableBytes: stat.Bavail * bsize,
			FreeBytes:      stat.Bfree * bsize,
			SizeBytes:      stat.Blocks * bsize,
		})
	}

	return fsMetrics, nil
}

func macOSVersionName(version string) (string, error) {
	parts := strings.Split(version, ".")
	var major, minor int
	var err error

	if len(parts) > 0 {
		major, err = strconv.Atoi(parts[0])
		if err != nil {
			return "", err
		}
	}

	if len(parts) > 1 {
		minor, err = strconv.Atoi(parts[1])
		if err != nil {
			return "", err
		}
	}

	switch major {
	case 26:
		return "Tahoe", nil
	case 15:
		return "Sequoia", nil
	case 14:
		return "Sonoma", nil
	case 13:
		return "Ventura", nil
	case 12:
		return "Monterey", nil
	case 11:
		return "Big Sur", nil
	case 10:
		switch minor {
		case 16:
			// Apparently, this one can happen.
			return "Big Sur", nil
		case 15:
			return "Catalina", nil
		case 14:
			return "Mojave", nil
		case 13:
			return "High Sierra", nil
		case 12:
			return "Sierra", nil
		case 11:
			return "El Capitan", nil
		case 10:
			return "Yosemite", nil
		case 9:
			return "Mavericks", nil
		case 8:
			return "Mountain Lion", nil
		case 7:
			return "Lion", nil
		case 6:
			return "Snow Leopard", nil
		case 5:
			return "Leopard", nil
		case 4:
			return "Tiger", nil
		case 3:
			return "Panther", nil
		case 2:
			return "Jaguar", nil
		case 1:
			return "Puma", nil
		case 0:
			return "Cheetah", nil
		}
	}

	return "", errors.New("Unknown macOS version")
}

func osGetOSState() *api.InstanceStateOSInfo {
	swVers, err := subprocess.RunCommand("sw_vers")
	if err != nil {
		return nil
	}

	var productName, productVersion string
	for _, line := range strings.Split(strings.TrimSpace(swVers), "\n") {
		key, after, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		value := strings.TrimSpace(after)
		if key == "ProductName" {
			productName = value
		} else if key == "ProductVersion" {
			productVersion = value
		}
	}

	// Add the familiar version name if we are dealing with a known macOS version.
	if productName == "Mac OS X" || productName == "macOS" {
		versionName, err := macOSVersionName(productVersion)
		if err == nil {
			productVersion += " (" + versionName + ")"
		}
	}

	uname := unix.Utsname{}
	err = unix.Uname(&uname)
	if err != nil {
		return nil
	}

	serverName := parseBytes(uname.Nodename[:])

	// Prepare OS struct.
	osInfo := &api.InstanceStateOSInfo{
		OS:            productName,
		OSVersion:     productVersion,
		KernelVersion: parseBytes(uname.Release[:]),
		Hostname:      serverName,
		FQDN:          serverName,
	}

	return osInfo
}

func osGetInteractiveConsole(s *execWs) (io.ReadWriteCloser, io.ReadWriteCloser, error) {
	return nil, nil, errors.New("Only non-interactive exec sessions are currently supported on Darwin")
}

func osPrepareExecCommand(s *execWs, cmd *exec.Cmd) {
	if s.cwd == "" {
		cmd.Dir = osBaseWorkingDirectory
	}

	return
}

func osHandleExecControl(control api.InstanceExecControl, s *execWs, pty io.ReadWriteCloser, cmd *exec.Cmd, l logger.Logger) {
	// Ignore control messages.
	return
}

// osExitStatus is is the same as linux.ExitStatus for Darwin.
func osExitStatus(err error) (int, error) {
	if err == nil {
		return 0, err // No error exit status.
	}

	var exitErr *exec.ExitError

	// Detect and extract ExitError to check the embedded exit status.
	if errors.As(err, &exitErr) {
		// If the process was signaled, extract the signal.
		status, isWaitStatus := exitErr.Sys().(unix.WaitStatus)
		if isWaitStatus && status.Signaled() {
			return 128 + int(status.Signal()), nil // 128 + n == Fatal error signal "n"
		}

		// Otherwise capture the exit status from the command.
		return exitErr.ExitCode(), nil
	}

	return -1, err // Not able to extract an exit status.
}

func osSetEnv(post *api.InstanceExecPost, env map[string]string) {
	env["PATH"] = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}
