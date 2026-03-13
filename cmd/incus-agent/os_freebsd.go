//go:build freebsd

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/shirou/gopsutil/v4/disk"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/server/metrics"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

// isMountPoint returns true if path is a mount point.
func isMountPoint(path string) bool {
	// Get the stat details.
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}

	rootStat, err := os.Lstat(path + "/..")
	if err != nil {
		return false
	}

	return stat.Sys().(*syscall.Stat_t).Dev != rootStat.Sys().(*syscall.Stat_t).Dev
}

func osMountShared(src string, dst string, fstype string, opts []string) error {
	if fstype != "9p" {
		return errors.New("Only 9p shares are supported on FreeBSD")
	}

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
	} else if isMountPoint(dst) {
		// Already mounted.
		return nil
	}

	args := []string{"-t", "p9fs", src, dst}
	for _, opt := range opts {
		args = append(args, "-o", opt)
	}

	_, err := subprocess.RunCommand("mount", args...)
	if err == nil {
		return err
	}

	return nil
}

// osUmount is currently not used, but it is implemented just in case.
func osUmount(src string, dst string, fstype string) error {
	if fstype != "9p" {
		return errors.New("Only 9p shares are supported on FreeBSD")
	}

	_, err := subprocess.RunCommand("umount", src)
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

		fsMetrics = append(fsMetrics, metrics.FilesystemMetrics{
			Device:         partition.Device,
			Mountpoint:     partition.Mountpoint,
			FSType:         partition.Fstype,
			AvailableBytes: uint64(stat.Bavail) * stat.Bsize,
			FreeBytes:      stat.Bfree * stat.Bsize,
			SizeBytes:      stat.Blocks * stat.Bsize,
		})
	}

	return fsMetrics, nil
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
	uname := unix.Utsname{}
	err = unix.Uname(&uname)
	if err == nil {
		osInfo.KernelVersion = parseBytes(uname.Release[:])
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

// openPty is is the same as linux.OpenPty for FreeBSD.
func openPty(uid, gid int64) (*os.File, *os.File, error) {
	reverter := revert.New()
	defer reverter.Fail()

	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}

	ptx := os.NewFile(uintptr(fd), "/dev/pts/ptmx")
	reverter.Add(func() { _ = ptx.Close() })

	// Get the pty side.
	id := 0
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(ptx.Fd()), unix.TIOCGPTN, uintptr(unsafe.Pointer(&id)))
	if errno != 0 {
		return nil, nil, unix.Errno(errno)
	}

	ptyPath := fmt.Sprintf("/dev/pts/%d", id)
	ptyFd, err := unix.Open(ptyPath, unix.O_NOCTTY|unix.O_CLOEXEC|os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}

	pty := os.NewFile(uintptr(ptyFd), ptyPath)
	reverter.Add(func() { _ = pty.Close() })

	// Configure both sides
	for _, entry := range []*os.File{ptx, pty} {
		// Get termios.
		t, err := unix.IoctlGetTermios(int(entry.Fd()), unix.TIOCGETA)
		if err != nil {
			return nil, nil, err
		}

		// Set flags.
		t.Cflag |= unix.IMAXBEL
		t.Cflag |= unix.BRKINT
		t.Cflag |= unix.IXANY
		t.Cflag |= unix.HUPCL

		// Set termios.
		err = unix.IoctlSetTermios(int(entry.Fd()), unix.TIOCSETA, t)
		if err != nil {
			return nil, nil, err
		}

		// Set the default window size.
		sz := &unix.Winsize{
			Col: 80,
			Row: 25,
		}

		err = unix.IoctlSetWinsize(int(entry.Fd()), unix.TIOCSWINSZ, sz)
		if err != nil {
			return nil, nil, err
		}

		// Set CLOEXEC.
		_, _, errno = unix.Syscall(unix.SYS_FCNTL, uintptr(entry.Fd()), unix.F_SETFD, unix.FD_CLOEXEC)
		if errno != 0 {
			return nil, nil, unix.Errno(errno)
		}
	}

	// Fix the ownership of the pty side.
	err = unix.Fchown(int(pty.Fd()), int(uid), int(gid))
	if err != nil {
		return nil, nil, err
	}

	reverter.Success()

	return ptx, pty, nil
}

func osSetEnv(post *api.InstanceExecPost, env map[string]string) {
	// Set default value for PATH.
	_, ok := env["PATH"]
	if !ok {
		env["PATH"] = "/sbin:/bin:/usr/sbin:/usr/bin:/usr/local/sbin:/usr/local/bin"
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
			post.Cwd = osBaseWorkingDirectory
		}
	}
}
