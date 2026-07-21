package device

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/v7/internal/linux"
	"github.com/lxc/incus/v7/internal/server/cgroup"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
)

func unixBlockLimitsEnabled(config deviceConfig.Device) bool {
	return config["type"] == "unix-block" && (config["limits.read"] != "" || config["limits.write"] != "")
}

func unixBlockDevicePath(devicesPath string, deviceName string, config deviceConfig.Device) string {
	prefix := deviceJoinPath("unix", deviceName)
	destPath := strings.TrimPrefix(unixDeviceDestPath(config), "/")
	name := linux.PathNameEncode(deviceJoinPath(prefix, destPath))
	return filepath.Join(devicesPath, name)
}

func unixBlockLimitValidate(config deviceConfig.Device) error {
	if !unixBlockLimitsEnabled(config) {
		return nil
	}

	readBps, readIops, writeBps, writeIops, err := diskParseLimits(config)
	if err != nil {
		return fmt.Errorf("Invalid I/O limit: %w", err)
	}

	if (config["limits.read"] != "" && readBps <= 0 && readIops <= 0) || (config["limits.write"] != "" && writeBps <= 0 && writeIops <= 0) {
		return errors.New("I/O limits must be greater than zero")
	}

	return nil
}

func unixBlockLimitApply(runConf *deviceConfig.RunConfig, devicePath string, config deviceConfig.Device) error {
	if !unixBlockLimitsEnabled(config) {
		return nil
	}

	if !cgroup.Supports(cgroup.IO) {
		return errors.New("Cannot apply block device limits as IO cgroup controller is missing")
	}

	dType, major, minor, err := unixDeviceAttributes(devicePath)
	if err != nil {
		return fmt.Errorf("Failed getting block device attributes for I/O limits: %w", err)
	}

	if dType != "b" {
		return errors.New("I/O limits require a block device")
	}

	readBps, readIops, writeBps, writeIops, err := diskParseLimits(config)
	if err != nil {
		return err
	}

	cg, err := cgroup.New(&cgroupWriter{runConf})
	if err != nil {
		return err
	}

	block := fmt.Sprintf("%d:%d", major, minor)
	limits := []struct {
		direction string
		unit      string
		value     int64
	}{
		{"read", "bps", readBps},
		{"read", "iops", readIops},
		{"write", "bps", writeBps},
		{"write", "iops", writeIops},
	}

	for _, limit := range limits {
		if limit.value <= 0 {
			continue
		}

		err = cg.SetBlkioLimit(block, limit.direction, limit.unit, limit.value)
		if err != nil {
			return err
		}
	}

	return nil
}

func unixBlockLimitClear(runConf *deviceConfig.RunConfig, devicePath string, config deviceConfig.Device) error {
	if !unixBlockLimitsEnabled(config) {
		return nil
	}

	dType, major, minor, err := unixDeviceAttributes(devicePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("Failed getting block device attributes to clear I/O limits: %w", err)
	}

	if dType != "b" {
		return nil
	}

	runConf.CGroups = append(runConf.CGroups, deviceConfig.RunConfigItem{
		Key:   "io.max",
		Value: fmt.Sprintf("%d:%d rbps=max riops=max wbps=max wiops=max", major, minor),
	})

	return nil
}
