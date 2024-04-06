package device

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	internalIO "github.com/lxc/incus/v6/internal/io"
	"github.com/lxc/incus/v6/internal/linux"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/idmap"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

// unixDefaultMode default mode to create unix devices with if not specified in device config.
const unixDefaultMode = 0660

// unixDeviceAttributes returns the decice type, major and minor numbers for a device.
func unixDeviceAttributes(path string) (string, uint32, uint32, error) {
	// Get a stat struct from the provided path
	stat := unix.Stat_t{}
	err := unix.Stat(path, &stat)
	if err != nil {
		return "", 0, 0, err
	}

	// Check what kind of file it is
	dType := ""
	if stat.Mode&unix.S_IFMT == unix.S_IFBLK {
		dType = "b"
	} else if stat.Mode&unix.S_IFMT == unix.S_IFCHR {
		dType = "c"
	} else {
		return "", 0, 0, fmt.Errorf("Not a device")
	}

	// Return the device information
	major := unix.Major(uint64(stat.Rdev))
	minor := unix.Minor(uint64(stat.Rdev))
	return dType, major, minor, nil
}

// unixDeviceModeOct converts a string unix octal mode to an int.
func unixDeviceModeOct(strmode string) (int, error) {
	i, err := strconv.ParseInt(strmode, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("Bad device mode: %s", strmode)
	}

	return int(i), nil
}

// UnixDevice contains information about a created UNIX device.
type UnixDevice struct {
	HostPath     string      // Absolute path to the device on the host.
	RelativePath string      // Relative path where the device will be mounted inside instance.
	Type         string      // Type of device; c (for char) or b for (block).
	Major        uint32      // Major number.
	Minor        uint32      // Minor number.
	Mode         os.FileMode // File mode.
	UID          int         // Owner UID.
	GID          int         // Owner GID.
}

// unixDeviceSourcePath returns the absolute path for a device on the host.
// This is based on the "source" property of the device's config, or the "path" property if "source"
// not define.
func unixDeviceSourcePath(m deviceConfig.Device) string {
	srcPath := m["source"]
	if srcPath == "" {
		srcPath = m["path"]
	}

	return srcPath
}

// unixDeviceDestPath returns the absolute path for a device inside an instance.
// This is based on the "path" property of the device's config, or the "source" property if "path"
// not defined.
func unixDeviceDestPath(m deviceConfig.Device) string {
	destPath := m["path"]
	if destPath == "" {
		destPath = m["source"]
	}

	return destPath
}

// UnixDeviceCreate creates a UNIX device (either block or char). If the supplied device config map
// contains a major and minor number for the device, then a stat is avoided, otherwise this info
// retrieved from the origin device. Similarly, if a mode is supplied in the device config map or
// defaultMode is set as true, then the device is created with the supplied or default mode (0660)
// respectively, otherwise the origin device's mode is used. If the device config doesn't contain a
// type field then it defaults to created a unix-char device. The ownership of the created device
// defaults to root (0) but can be specified with the uid and gid fields in the device config map.
// It returns a UnixDevice containing information about the device created.
func UnixDeviceCreate(s *state.State, idmapSet *idmap.Set, devicesPath string, prefix string, m deviceConfig.Device, defaultMode bool) (*UnixDevice, error) {
	var err error
	d := UnixDevice{}

	// Extra checks for nesting.
	if s.OS.RunningInUserNS {
		for key, value := range m {
			if slices.Contains([]string{"major", "minor", "mode", "uid", "gid"}, key) && value != "" {
				return nil, fmt.Errorf("The \"%s\" property may not be set when adding a device to a nested container", key)
			}
		}
	}

	srcPath := unixDeviceSourcePath(m)

	// Get the major/minor of the device we want to create.
	if m["major"] == "" && m["minor"] == "" {
		// If no major and minor are set, use those from the device on the host.
		_, d.Major, d.Minor, err = unixDeviceAttributes(srcPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to get device attributes for %s: %w", srcPath, err)
		}
	} else if m["major"] == "" || m["minor"] == "" {
		return nil, fmt.Errorf("Both major and minor must be supplied for device: %s", srcPath)
	} else {
		tmp, err := strconv.ParseUint(m["major"], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Bad major %s in device %s", m["major"], srcPath)
		}

		d.Major = uint32(tmp)

		tmp, err = strconv.ParseUint(m["minor"], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Bad minor %s in device %s", m["minor"], srcPath)
		}

		d.Minor = uint32(tmp)
	}

	// Get the device mode (defaults to unixDefaultMode if not supplied).
	d.Mode = os.FileMode(unixDefaultMode)
	if m["mode"] != "" {
		tmp, err := unixDeviceModeOct(m["mode"])
		if err != nil {
			return nil, fmt.Errorf("Bad mode %s in device %s", m["mode"], srcPath)
		}

		d.Mode = os.FileMode(tmp)
	} else if !defaultMode {
		// If not specified mode in device config, and default mode is false, then try and
		// read the source device's mode and use that inside the instance.
		d.Mode, err = internalIO.GetPathMode(srcPath)
		if err != nil {
			errno, isErrno := linux.GetErrno(err)
			if !isErrno || errno != unix.ENOENT {
				return nil, fmt.Errorf("Failed to retrieve mode of device %s: %w", srcPath, err)
			}

			d.Mode = os.FileMode(unixDefaultMode)
		}
	}

	if m["type"] == "unix-block" {
		d.Mode |= unix.S_IFBLK
		d.Type = "b"
	} else {
		d.Mode |= unix.S_IFCHR
		d.Type = "c"
	}

	// Get the device owner.
	if m["uid"] != "" {
		d.UID, err = strconv.Atoi(m["uid"])
		if err != nil {
			return nil, fmt.Errorf("Invalid uid %s in device %s", m["uid"], srcPath)
		}
	}

	if m["gid"] != "" {
		d.GID, err = strconv.Atoi(m["gid"])
		if err != nil {
			return nil, fmt.Errorf("Invalid gid %s in device %s", m["gid"], srcPath)
		}
	}

	// Create the devices directory if missing.
	if !util.PathExists(devicesPath) {
		err := os.Mkdir(devicesPath, 0711)
		if err != nil {
			return nil, fmt.Errorf("Failed to create devices path: %s", err)
		}
	}

	destPath := unixDeviceDestPath(m)
	relativeDestPath := strings.TrimPrefix(destPath, "/")
	devName := linux.PathNameEncode(deviceJoinPath(prefix, relativeDestPath))
	devPath := filepath.Join(devicesPath, devName)

	// Create the new entry.
	if !s.OS.RunningInUserNS {
		if s.OS.Nodev {
			return nil, fmt.Errorf("Can't create device as devices path is mounted nodev")
		}

		devNum := int(unix.Mkdev(d.Major, d.Minor))
		err := unix.Mknod(devPath, uint32(d.Mode), devNum)
		if err != nil {
			return nil, fmt.Errorf("Failed to create device %s for %s: %w", devPath, srcPath, err)
		}

		err = os.Chown(devPath, d.UID, d.GID)
		if err != nil {
			return nil, fmt.Errorf("Failed to chown device %s: %w", devPath, err)
		}

		// Needed as mknod respects the umask.
		err = os.Chmod(devPath, d.Mode)
		if err != nil {
			return nil, fmt.Errorf("Failed to chmod device %s: %w", devPath, err)
		}

		if idmapSet != nil {
			err := idmapSet.ShiftPath(devPath, nil)
			if err != nil {
				// uidshift failing is weird, but not a big problem. Log and proceed.
				logger.Debugf("Failed to uidshift device %s: %s\n", srcPath, err)
			}
		}
	} else {
		f, err := os.Create(devPath)
		if err != nil {
			return nil, err
		}

		_ = f.Close()

		err = DiskMount(srcPath, devPath, false, "", nil, "none")
		if err != nil {
			return nil, err
		}
	}

	d.HostPath = devPath
	d.RelativePath = relativeDestPath
	return &d, nil
}

// unixDeviceSetup creates a UNIX device on host and then configures supplied RunConfig with the
// mount and cgroup rule instructions to have it be attached to the instance. If defaultMode is true
// or mode is supplied in the device config then the origin device does not need to be accessed for
// its file mode.
func unixDeviceSetup(s *state.State, devicesPath string, typePrefix string, deviceName string, m deviceConfig.Device, defaultMode bool, runConf *deviceConfig.RunConfig) error {
	// Before creating the device, check that another existing device isn't using the same mount
	// path inside the instance as our device. If we find an existing device with the same mount
	// path we will skip mounting our device inside the instance. This can happen when multiple
	// devices share the same parent device (such as Nvidia GPUs and Infiniband devices).

	// Convert the requested dest path inside the instance to an encoded relative one.
	ourDestPath := unixDeviceDestPath(m)
	ourEncRelDestFile := linux.PathNameEncode(strings.TrimPrefix(ourDestPath, "/"))

	// Load all existing host devices.
	dents, err := os.ReadDir(devicesPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	dupe := false
	for _, ent := range dents {
		devName := ent.Name()

		// Remove the device type and name prefix, leaving just the encoded dest path.
		idx := strings.LastIndex(devName, ".")
		if idx == -1 {
			return fmt.Errorf("Invalid device name \"%s\"", devName)
		}

		encRelDestFile := devName[idx+1:]

		// If the encoded relative path of the device file matches the encoded relative dest
		// path of our new device then return as we do not want to have
		// it mounted or cgroup rules created.
		if encRelDestFile == ourEncRelDestFile {
			dupe = true // There is an existing device using the same mount path.
			break
		}
	}

	// Create the device on the host.
	ourPrefix := deviceJoinPath(typePrefix, deviceName)
	d, err := UnixDeviceCreate(s, nil, devicesPath, ourPrefix, m, defaultMode)
	if err != nil {
		return err
	}

	// If there was an existing device using the same mount path detected then skip mounting.
	if dupe {
		return nil
	}

	// Ask for a mount to be performed.
	runConf.Mounts = append(runConf.Mounts, deviceConfig.MountEntryItem{
		DevPath:    d.HostPath,
		TargetPath: d.RelativePath,
		FSType:     "none",
		Opts:       []string{"bind", "create=file"},
		OwnerShift: deviceConfig.MountOwnerShiftStatic,
	})

	// Ask for cgroups to be configured.
	runConf.CGroups = append(runConf.CGroups, deviceConfig.RunConfigItem{
		Key:   "devices.allow",
		Value: fmt.Sprintf("%s %d:%d rwm", d.Type, d.Major, d.Minor),
	})

	return nil
}

// unixDeviceSetupCharNum calls unixDeviceSetup and overrides the supplied device config with the
// type as "unix-char" and the supplied major and minor numbers. This function can be used when you
// already know the device's major and minor numbers to avoid unixDeviceSetup() having to stat the
// device to ascertain these attributes. If defaultMode is true or mode is supplied in the device
// config then the origin device does not need to be accessed for its file mode.
func unixDeviceSetupCharNum(s *state.State, devicesPath string, typePrefix string, deviceName string, m deviceConfig.Device, major uint32, minor uint32, path string, defaultMode bool, runConf *deviceConfig.RunConfig) error {
	configCopy := deviceConfig.Device{}
	for k, v := range m {
		configCopy[k] = v
	}

	// Overridng these in the config copy should avoid the need for unixDeviceSetup to stat
	// the origin device to ascertain this information.
	configCopy["type"] = "unix-char"
	configCopy["major"] = fmt.Sprintf("%d", major)
	configCopy["minor"] = fmt.Sprintf("%d", minor)
	configCopy["path"] = path

	return unixDeviceSetup(s, devicesPath, typePrefix, deviceName, configCopy, defaultMode, runConf)
}

// unixDeviceSetupBlockNum calls unixDeviceSetup and overrides the supplied device config with the
// type as "unix-block" and the supplied major and minor numbers. This function can be used when you
// already know the device's major and minor numbers to avoid unixDeviceSetup() having to stat the
// device to ascertain these attributes. If defaultMode is true or mode is supplied in the device
// config then the origin device does not need to be accessed for its file mode.
func unixDeviceSetupBlockNum(s *state.State, devicesPath string, typePrefix string, deviceName string, m deviceConfig.Device, major uint32, minor uint32, path string, defaultMode bool, runConf *deviceConfig.RunConfig) error {
	configCopy := deviceConfig.Device{}
	for k, v := range m {
		configCopy[k] = v
	}

	// Overridng these in the config copy should avoid the need for unixDeviceSetup to stat
	// the origin device to ascertain this information.
	configCopy["type"] = "unix-block"
	configCopy["major"] = fmt.Sprintf("%d", major)
	configCopy["minor"] = fmt.Sprintf("%d", minor)
	configCopy["path"] = path

	return unixDeviceSetup(s, devicesPath, typePrefix, deviceName, configCopy, defaultMode, runConf)
}

// UnixDeviceExists checks if the unix device already exists in devices path.
func UnixDeviceExists(devicesPath string, prefix string, path string) bool {
	relativeDestPath := strings.TrimPrefix(path, "/")
	devName := fmt.Sprintf("%s.%s", linux.PathNameEncode(prefix), linux.PathNameEncode(relativeDestPath))
	devPath := filepath.Join(devicesPath, devName)

	return util.PathExists(devPath)
}

// unixRemoveDevice identifies all files related to the supplied typePrefix and deviceName and then
// populates the supplied runConf with the instructions to remove cgroup rules and unmount devices.
// It detects if any other devices attached to the instance that share the same prefix have the same
// relative mount path inside the instance encoded into the file name. If there is another device
// that shares the same mount path then the unmount rule is not added to the runConf as the device
// may still be in use with another device.
// Accepts an optional file prefix that will be used to narrow the selection of files to remove.
func unixDeviceRemove(devicesPath string, typePrefix string, deviceName string, optPrefix string, runConf *deviceConfig.RunConfig) error {
	// Load all devices.
	dents, err := os.ReadDir(devicesPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	var ourPrefix string
	// If a prefix override has been supplied, use that for filtering the devices to remove.
	if optPrefix != "" {
		ourPrefix = linux.PathNameEncode(deviceJoinPath(typePrefix, deviceName, optPrefix))
	} else {
		ourPrefix = linux.PathNameEncode(deviceJoinPath(typePrefix, deviceName))
	}

	ourDevs := []string{}
	otherDevs := []string{}

	for _, ent := range dents {
		devName := ent.Name()

		// This device file belongs to our device.
		if strings.HasPrefix(devName, ourPrefix) {
			ourDevs = append(ourDevs, devName)
			continue
		}

		// This device file belongs to another device.
		otherDevs = append(otherDevs, devName)
	}

	// It is possible for some devices to share the same device on the same mount point
	// inside the instance. We extract the relative path of the device that is encoded into its
	// name on the host so that we can compare the device files for our own device and check
	// none of them use the same mount point.
	encRelDevFiles := []string{}
	for _, otherDev := range otherDevs {
		// Remove the device type and name prefix, leaving just the encoded dest path.
		idx := strings.LastIndex(otherDev, ".")
		if idx == -1 {
			return fmt.Errorf("Invalid device name \"%s\"", otherDev)
		}

		encRelDestFile := otherDev[idx+1:]
		encRelDevFiles = append(encRelDevFiles, encRelDestFile)
	}

	// Check that none of our devices are in use by another device.
	for _, ourDev := range ourDevs {
		// Remove the device type and name prefix, leaving just the encoded dest path.
		idx := strings.LastIndex(ourDev, ".")
		if idx == -1 {
			return fmt.Errorf("Invalid device name \"%s\"", ourDev)
		}

		ourEncRelDestFile := ourDev[idx+1:]

		// Look for devices for other devices that match the same path.
		dupe := false
		for _, encRelDevFile := range encRelDevFiles {
			if encRelDevFile == ourEncRelDestFile {
				dupe = true
				break
			}
		}

		// If a device has been found that points to the same device inside the instance
		// then we cannot request it be umounted inside the instance as it's still in use.
		if dupe {
			continue
		}

		// Append this device to the mount rules (these will be unmounted).
		runConf.Mounts = append(runConf.Mounts, deviceConfig.MountEntryItem{
			TargetPath: linux.PathNameDecode(ourEncRelDestFile),
		})

		absDevPath := filepath.Join(devicesPath, ourDev)
		dType, dMajor, dMinor, err := unixDeviceAttributes(absDevPath)
		if err != nil {
			return fmt.Errorf("Failed to get UNIX device attributes for '%s': %w", absDevPath, err)
		}

		// Append a deny cgroup fule for this device.
		runConf.CGroups = append(runConf.CGroups, deviceConfig.RunConfigItem{
			Key:   "devices.deny",
			Value: fmt.Sprintf("%s %d:%d rwm", dType, dMajor, dMinor),
		})
	}

	return nil
}

// unixDeviceDeleteFiles removes all host side device files for a particular device.
// Accepts an optional file prefix that will be used to narrow the selection of files to delete.
// This should be run after the files have been detached from the instance as a post hook.
func unixDeviceDeleteFiles(s *state.State, devicesPath string, typePrefix string, deviceName string, optPrefix string) error {
	var ourPrefix string
	// If a prefix override has been supplied, use that for filtering the devices to remove.
	if optPrefix != "" {
		ourPrefix = linux.PathNameEncode(deviceJoinPath(typePrefix, deviceName, optPrefix))
	} else {
		ourPrefix = linux.PathNameEncode(deviceJoinPath(typePrefix, deviceName))
	}

	// Load all devices.
	dents, err := os.ReadDir(devicesPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	// Remove our host side device files.
	for _, ent := range dents {
		devName := ent.Name()

		// This device file belongs to our device.
		if strings.HasPrefix(devName, ourPrefix) {
			devPath := filepath.Join(devicesPath, devName)

			// Remove the host side mount.
			if s.OS.RunningInUserNS {
				_ = unix.Unmount(devPath, unix.MNT_DETACH)
			}

			// Remove the host side device file.
			err := os.Remove(devPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// unixValidDeviceNum validates the major and minor numbers for a UNIX device.
func unixValidDeviceNum(value string) error {
	if value == "" {
		return nil
	}

	_, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return fmt.Errorf("Invalid value for a UNIX device number")
	}

	return nil
}

// unixValidUserID validates the UNIX UID and GID values for ownership.
func unixValidUserID(value string) error {
	if value == "" {
		return nil
	}

	_, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return fmt.Errorf("Invalid value for a UNIX ID")
	}

	return nil
}

// unixValidOctalFileMode validates the UNIX file mode.
func unixValidOctalFileMode(value string) error {
	if value == "" {
		return nil
	}

	_, err := strconv.ParseUint(value, 8, 32)
	if err != nil {
		return fmt.Errorf("Invalid value for an octal file mode")
	}

	return nil
}
