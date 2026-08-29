//go:build netbsd

package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/internal/server/metrics"
	"github.com/lxc/incus/v7/shared/subprocess"
	"github.com/lxc/incus/v7/shared/util"
)

var sharesMapping map[string]string

func osMountShared(src string, dst string, fstype string, opts []string) error {
	if fstype != "9p" {
		return errors.New("Only 9p shares are supported on common BSDs")
	}

	// NetBSD maps 9p shares to /dev/vio9pX devices at boot time. We need to parse diagnostic messages
	// to compute this map.
	if sharesMapping == nil {
		journal, err := subprocess.RunCommand("dmesg", "-t")
		if err != nil {
			return fmt.Errorf("Failed to read diagnostic messages: %w", err)
		}

		sharesMapping = map[string]string{}
		for _, line := range strings.Split(journal, "\n") {
			if strings.HasPrefix(line, "vio9p") {
				dev, share, ok := strings.Cut(line, ": tagged as ")
				if ok {
					sharesMapping[share] = "/dev/" + dev
				}
			}
		}
	}

	dev, ok := sharesMapping[src]
	if !ok {
		return fmt.Errorf("Failed to find mapped device for share %s", src)
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

	args := []string{"-cu"}
	for _, opt := range opts {
		if !strings.HasPrefix(opt, "trans=") {
			args = append(args, "-o", opt)
		}
	}

	args = append(args, dev, dst)

	// NetBSD can be extremely broken if the mount happens too early. mount_9p can hang and not
	// respond to SIGKILL, while still managing to mount the share. This may leave a goroutine hanging
	// indefinitely, but it is not that critical.
	result := make(chan error, 1)
	go func() {
		_, err := subprocess.RunCommand("mount_9p", args...)
		result <- err
	}()

	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		return errors.New("mount_9p timed out after 5 seconds")
	}
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
		var stat unix.Statvfs_t
		err = unix.Statvfs(partition.Mountpoint, &stat)
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
