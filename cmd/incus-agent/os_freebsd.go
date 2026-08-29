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

	"github.com/shirou/gopsutil/v4/disk"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/internal/server/metrics"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/osarch"
	"github.com/lxc/incus/v7/shared/subprocess"
	"github.com/lxc/incus/v7/shared/util"
)

func osLoadModules() error {
	// No OS drivers to load by default.
	return nil
}

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
	return err
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
