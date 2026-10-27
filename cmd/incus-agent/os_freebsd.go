//go:build freebsd

package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/shirou/gopsutil/v4/disk"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/internal/server/metrics"
	"github.com/lxc/incus/v7/shared/subprocess"
	"github.com/lxc/incus/v7/shared/util"
)

var (
	osAgentConfigPath = "/usr/local/etc/incus-agent.yml"
	osVioSerialPath   = "/dev/vtcon/org.linuxcontainers.incus"
)

func osMountShared(src string, dst string, fstype string, opts []string) error {
	if fstype != "9p" {
		return errors.New("Only 9p shares are supported on common BSDs")
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
		var stat unix.Statfs_t
		err = unix.Statfs(partition.Mountpoint, &stat)
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
