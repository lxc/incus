package drivers

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/backup"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/ioprogress"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/subprocess"
)

// Errors.
var errBtrfsNoQuota = fmt.Errorf("Quotas disabled on filesystem")
var errBtrfsNoQGroup = fmt.Errorf("Unable to find quota group")

// btrfsISOVolSuffix suffix used for iso content type volumes.
const btrfsISOVolSuffix = ".iso"

// setReceivedUUID sets the "Received UUID" field on a subvolume with the given path using ioctl.
func setReceivedUUID(path string, UUID string) error {
	type btrfsIoctlReceivedSubvolArgs struct {
		uuid [16]byte
		_    [22]uint64 // padding
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("Failed opening %s: %w", path, err)
	}

	defer func() { _ = f.Close() }()

	args := btrfsIoctlReceivedSubvolArgs{}

	strUUID, err := uuid.Parse(UUID)
	if err != nil {
		return fmt.Errorf("Failed parsing UUID: %w", err)
	}

	binUUID, err := strUUID.MarshalBinary()
	if err != nil {
		return fmt.Errorf("Failed coverting UUID: %w", err)
	}

	copy(args.uuid[:], binUUID)

	// 0xC0C09425 = _IOWR(BTRFS_IOCTL_MAGIC, 37, struct btrfs_ioctl_received_subvol_args)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), 0xC0C09425, uintptr(unsafe.Pointer(&args)))
	if errno != 0 {
		return fmt.Errorf("Failed setting received UUID: %w", unix.Errno(errno))
	}

	return nil
}

func (d *btrfs) getMountOptions() string {
	// Allow overriding the default options.
	if d.config["btrfs.mount_options"] != "" {
		return d.config["btrfs.mount_options"]
	}

	return "user_subvol_rm_allowed"
}

func (d *btrfs) isSubvolume(path string) bool {
	// Stat the path.
	fs := unix.Stat_t{}
	err := unix.Lstat(path, &fs)
	if err != nil {
		return false
	}

	// Check if BTRFS_FIRST_FREE_OBJECTID is the inode number.
	if fs.Ino != 256 {
		return false
	}

	return true
}

func (d *btrfs) hasSubvolumes(path string) (bool, error) {
	var stdout strings.Builder

	err := subprocess.RunCommandWithFds(d.state.ShutdownCtx, nil, &stdout, "btrfs", "subvolume", "list", "-o", path)
	if err != nil {
		return false, err
	}

	return stdout.Len() > 0, nil
}

func (d *btrfs) getSubvolumes(path string) ([]string, error) {
	// Make sure the path has a trailing slash.
	if !strings.HasSuffix(path, "/") {
		path = path + "/"
	}

	poolMountPath := GetPoolMountPath(d.name)
	if !strings.HasPrefix(path, poolMountPath+"/") {
		return nil, fmt.Errorf("%q is outside pool mount path %q", path, poolMountPath)
	}

	var result []string

	if d.state.OS.RunningInUserNS {
		// If using BTRFS in a nested container we cannot use "btrfs subvolume list" due to a permission error.
		// So instead walk the directory tree testing each directory to see if it is subvolume.
		err := filepath.Walk(path, func(fpath string, entry fs.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Ignore the base path.
			if strings.TrimRight(fpath, "/") == strings.TrimRight(path, "/") {
				return nil
			}

			// Subvolumes can only be directories.
			if !entry.IsDir() {
				return nil
			}

			// Check if directory is a subvolume.
			if d.isSubvolume(fpath) {
				result = append(result, strings.TrimPrefix(fpath, path))
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		// If not running inside a nested container we can use "btrfs subvolume list" to get subvolumes which is more
		// performant than walking the directory tree.
		var stdout bytes.Buffer
		err := subprocess.RunCommandWithFds(d.state.ShutdownCtx, nil, &stdout, "btrfs", "subvolume", "list", poolMountPath)
		if err != nil {
			return nil, err
		}

		path = strings.TrimPrefix(path, poolMountPath+"/")
		scanner := bufio.NewScanner(&stdout)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			if len(fields) != 9 {
				continue
			}

			if !strings.HasPrefix(fields[8], path) {
				continue
			}

			result = append(result, strings.TrimPrefix(fields[8], path))
		}
	}

	return result, nil
}

// snapshotSubvolume creates a snapshot of the specified path at the dest supplied. If recursion is true and
// sub volumes are found below the path then they are created at the relative location in dest.
func (d *btrfs) snapshotSubvolume(path string, dest string, recursion bool) (revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Single subvolume creation.
	snapshot := func(path string, dest string) error {
		_, err := subprocess.RunCommand("btrfs", "subvolume", "snapshot", path, dest)
		if err != nil {
			return err
		}

		revert.Add(func() {
			// Don't delete recursive since there already is a revert hook
			// for each subvolume that got created.
			_ = d.deleteSubvolume(dest, false)
		})

		return nil
	}

	// First snapshot the root.
	err := snapshot(path, dest)
	if err != nil {
		return nil, err
	}

	// Now snapshot all subvolumes of the root.
	if recursion {
		// Get the subvolumes list.
		subSubVols, err := d.getSubvolumes(path)
		if err != nil {
			return nil, err
		}

		sort.Strings(subSubVols)

		for _, subSubVol := range subSubVols {
			subSubVolSnapPath := filepath.Join(dest, subSubVol)

			// Clear the target for the subvol to use.
			_ = os.Remove(subSubVolSnapPath)

			err := snapshot(filepath.Join(path, subSubVol), subSubVolSnapPath)
			if err != nil {
				return nil, err
			}
		}
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, nil
}

func (d *btrfs) deleteSubvolume(rootPath string, recursion bool) error {
	// Single subvolume deletion.
	destroy := func(path string) error {
		// Attempt (but don't fail on) to delete any qgroup on the subvolume.
		qgroup, _, err := d.getQGroup(path)
		if err == nil {
			_, _ = subprocess.RunCommand("btrfs", "qgroup", "destroy", qgroup, path)
		}

		// Temporarily change ownership & mode to help with nesting.
		_ = os.Chmod(path, 0700)
		_ = os.Chown(path, 0, 0)

		// Delete the subvolume itself.
		_, err = subprocess.RunCommand("btrfs", "subvolume", "delete", path)

		return err
	}

	// Try and ensure volume is writable to possibility of destroy failing.
	err := d.setSubvolumeReadonlyProperty(rootPath, false)
	if err != nil {
		d.logger.Warn("Failed setting subvolume writable", logger.Ctx{"path": rootPath, "err": err})
	}

	// Attempt to delete the root subvol itself (short path).
	err = destroy(rootPath)
	if err == nil {
		return nil
	} else if !recursion {
		return fmt.Errorf("Failed deleting subvolume %q: %w", rootPath, err)
	}

	// Delete subsubvols as recursion enabled.

	// Get the subvolumes list.
	subSubVols, err := d.getSubvolumes(rootPath)
	if err != nil {
		return err
	}

	// Perform a first pass and ensure all sub volumes are writable.
	sort.Sort(sort.StringSlice(subSubVols))
	for _, subSubVol := range subSubVols {
		subSubVolPath := filepath.Join(rootPath, subSubVol)
		err = d.setSubvolumeReadonlyProperty(subSubVolPath, false)
		if err != nil {
			d.logger.Warn("Failed setting subvolume writable", logger.Ctx{"path": subSubVolPath, "err": err})
		}
	}

	// Perform a second pass to delete subvolumes.
	sort.Sort(sort.Reverse(sort.StringSlice(subSubVols)))
	for _, subSubVol := range subSubVols {
		subSubVolPath := filepath.Join(rootPath, subSubVol)
		err := destroy(subSubVolPath)
		if err != nil {
			return fmt.Errorf("Failed deleting subvolume %q: %w", subSubVolPath, err)
		}
	}

	// Delete the root subvol itself.
	err = destroy(rootPath)
	if err != nil {
		return fmt.Errorf("Failed deleting subvolume %q: %w", rootPath, err)
	}

	return nil
}

func (d *btrfs) getQGroup(path string) (string, int64, error) {
	// Try to get the qgroup details.
	output, err := subprocess.RunCommand("btrfs", "qgroup", "show", "-e", "-f", "--raw", path)
	if err != nil {
		return "", -1, errBtrfsNoQuota
	}

	// Parse to extract the qgroup identifier.
	var qgroup string
	usage := int64(-1)
	for _, line := range strings.Split(output, "\n") {
		// Use case-insensitive field title match because BTRFS tooling changed casing between versions.
		if line == "" || strings.HasPrefix(strings.ToLower(line), "qgroupid") || strings.HasPrefix(line, "-") {
			continue
		}

		fields := strings.Fields(line)

		// The BTRFS tooling changed the number of columns between versions so we only check for minimum.
		if len(fields) < 3 {
			continue
		}

		qgroup = fields[0]
		val, err := strconv.ParseInt(fields[2], 10, 64)
		if err == nil {
			usage = val
		}

		break
	}

	if qgroup == "" {
		return "", -1, errBtrfsNoQGroup
	}

	return qgroup, usage, nil
}

func (d *btrfs) sendSubvolume(path string, parent string, conn io.ReadWriteCloser, tracker *ioprogress.ProgressTracker) error {
	defer func() { _ = conn.Close() }()

	// Assemble btrfs send command.
	args := []string{"send"}
	if parent != "" {
		args = append(args, "-p", parent)
	}

	args = append(args, path)
	cmd := exec.Command("btrfs", args...)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	// Setup progress tracker.
	var stdout io.WriteCloser = conn
	if tracker != nil {
		stdout = &ioprogress.ProgressWriter{
			WriteCloser: conn,
			Tracker:     tracker,
		}
	}

	cmd.Stdout = stdout

	// Run the command.
	err = cmd.Start()
	if err != nil {
		return err
	}

	// Read any error.
	output, err := io.ReadAll(stderr)
	if err != nil {
		logger.Errorf("Problem reading btrfs send stderr: %s", err)
	}

	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("Btrfs send failed: %w (%s)", err, string(output))
	}

	return nil
}

// setSubvolumeReadonlyProperty sets the readonly property on the subvolume to true or false.
func (d *btrfs) setSubvolumeReadonlyProperty(path string, readonly bool) error {
	// Silently ignore requests to set subvolume readonly property if running in a user namespace as we won't
	// be able to change it if it is readonly already, and making it readonly will mean we cannot undo it.
	if d.state.OS.RunningInUserNS {
		return nil
	}

	args := []string{"property", "set"}
	if btrfsPropertyForce {
		args = append(args, "-f")
	}

	args = append(args, "-ts", path, "ro", fmt.Sprintf("%t", readonly))

	_, err := subprocess.RunCommand("btrfs", args...)
	return err
}

// BTRFSSubVolume is the structure used to store information about a subvolume.
// Note: This is used by both migration and backup subsystems so do not modify without considering both!
type BTRFSSubVolume struct {
	Path     string `json:"path" yaml:"path"`         // Path inside the volume where the subvolume belongs (so / is the top of the volume tree).
	Snapshot string `json:"snapshot" yaml:"snapshot"` // Snapshot name the subvolume belongs to.
	Readonly bool   `json:"readonly" yaml:"readonly"` // Is the sub volume read only or not.
	UUID     string `json:"uuid" yaml:"uuid"`         // The subvolume UUID.
}

// getSubvolumesMetaData retrieves subvolume meta data with paths relative to the root volume.
// The first item in the returned list is the root subvolume itself.
func (d *btrfs) getSubvolumesMetaData(vol Volume) ([]BTRFSSubVolume, error) {
	var subVols []BTRFSSubVolume

	snapName := ""
	if vol.IsSnapshot() {
		_, snapName, _ = api.GetParentAndSnapshotName(vol.name)
	}

	// Add main root volume to subvolumes list first.
	subVols = append(subVols, BTRFSSubVolume{
		Snapshot: snapName,
		Path:     string(filepath.Separator),
		Readonly: BTRFSSubVolumeIsRo(vol.MountPath()),
	})

	// Find any subvolumes in volume.
	subVolPaths, err := d.getSubvolumes(vol.MountPath())
	if err != nil {
		return nil, err
	}

	sort.Strings(subVolPaths)

	// Add any subvolumes under the root subvolume with relative path to root.
	for _, subVolPath := range subVolPaths {
		subVols = append(subVols, BTRFSSubVolume{
			Snapshot: snapName,
			Path:     fmt.Sprintf("%s%s", string(filepath.Separator), subVolPath),
			Readonly: BTRFSSubVolumeIsRo(filepath.Join(vol.MountPath(), subVolPath)),
		})
	}

	stdout := strings.Builder{}

	poolMountPath := GetPoolMountPath(vol.pool)

	if !d.state.OS.RunningInUserNS {
		// List all subvolumes in the given filesystem with their UUIDs and received UUIDs.
		err = subprocess.RunCommandWithFds(context.TODO(), nil, &stdout, "btrfs", "subvolume", "list", "-u", "-R", poolMountPath)
		if err != nil {
			return nil, err
		}

		uuidMap := make(map[string]string)
		receivedUUIDMap := make(map[string]string)

		scanner := bufio.NewScanner(strings.NewReader(stdout.String()))

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			if len(fields) != 13 {
				continue
			}

			uuidMap[filepath.Join(poolMountPath, fields[12])] = fields[10]

			if fields[8] != "-" {
				receivedUUIDMap[filepath.Join(poolMountPath, fields[12])] = fields[8]
			}
		}

		for i, subVol := range subVols {
			subVols[i].UUID = uuidMap[filepath.Join(vol.MountPath(), subVol.Path)]
		}
	}

	return subVols, nil
}

func (d *btrfs) getSubVolumeReceivedUUID(vol Volume) (string, error) {
	stdout := strings.Builder{}

	poolMountPath := GetPoolMountPath(vol.pool)

	// List all subvolumes in the given filesystem with their UUIDs.
	err := subprocess.RunCommandWithFds(context.TODO(), nil, &stdout, "btrfs", "subvolume", "list", "-R", poolMountPath)
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(strings.NewReader(stdout.String()))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())

		if len(fields) != 11 {
			continue
		}

		if vol.MountPath() == filepath.Join(poolMountPath, fields[10]) && fields[8] != "-" {
			return fields[8], nil
		}
	}

	return "", nil
}

// BTRFSMetaDataHeader is the meta data header about the volumes being sent/stored.
// Note: This is used by both migration and backup subsystems so do not modify without considering both!
type BTRFSMetaDataHeader struct {
	Subvolumes []BTRFSSubVolume `json:"subvolumes" yaml:"subvolumes"` // Sub volumes inside the volume (including the top level ones).
}

// restorationHeader scans the volume and any specified snapshots, returning a header containing subvolume metadata
// for use in restoring a volume and its snapshots onto another system. The metadata returned represents how the
// subvolumes should be restored, not necessarily how they are on disk now. Most of the time this is the same,
// however in circumstances where the volume being scanned is itself a snapshot, the returned metadata will
// not report the volume as readonly or as being a snapshot, as the expectation is that this volume will be
// restored on the target system as a normal volume and not a snapshot.
func (d *btrfs) restorationHeader(vol Volume, snapshots []string) (*BTRFSMetaDataHeader, error) {
	var migrationHeader BTRFSMetaDataHeader

	// Add snapshots to volumes list.
	for _, snapName := range snapshots {
		snapVol, _ := vol.NewSnapshot(snapName)

		// Add snapshot root volume to volumes list.
		subVols, err := d.getSubvolumesMetaData(snapVol)
		if err != nil {
			return nil, err
		}

		migrationHeader.Subvolumes = append(migrationHeader.Subvolumes, subVols...)
	}

	// Add main root volume to volumes list.
	subVols, err := d.getSubvolumesMetaData(vol)
	if err != nil {
		return nil, err
	}

	// If vol is a snapshot itself, we force the volume as writable (even if it isn't on disk) and remove the
	// snapshot name indicator as the expectation is that this volume is going to be restored on the target
	// system as a normal (non-snapshot) writable volume.
	if vol.IsSnapshot() {
		subVols[0].Readonly = false
		for i := range subVols {
			subVols[i].Snapshot = ""
		}
	}

	migrationHeader.Subvolumes = append(migrationHeader.Subvolumes, subVols...)
	return &migrationHeader, nil
}

// loadOptimizedBackupHeader extracts optimized backup header from a given ReadSeeker.
func (d *btrfs) loadOptimizedBackupHeader(r io.ReadSeeker, mountPath string) (*BTRFSMetaDataHeader, error) {
	header := BTRFSMetaDataHeader{}

	// Extract.
	tr, cancelFunc, err := backup.TarReader(r, d.state.OS, mountPath)
	if err != nil {
		return nil, err
	}

	defer cancelFunc()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive.
		}

		if err != nil {
			return nil, fmt.Errorf("Error reading backup file for optimized backup header file: %w", err)
		}

		if hdr.Name == "backup/optimized_header.yaml" {
			err = yaml.NewDecoder(tr).Decode(&header)
			if err != nil {
				return nil, fmt.Errorf("Error parsing optimized backup header file: %w", err)
			}

			cancelFunc()
			return &header, nil
		}
	}

	return nil, fmt.Errorf("Optimized backup header file not found")
}

// receiveSubVolume receives a subvolume from an io.Reader into the receivePath and returns the path to the received subvolume.
func (d *btrfs) receiveSubVolume(r io.Reader, receivePath string) (string, error) {
	files, err := os.ReadDir(receivePath)
	if err != nil {
		return "", fmt.Errorf("Failed listing contents of %q: %w", receivePath, err)
	}

	err = subprocess.RunCommandWithFds(context.TODO(), r, nil, "btrfs", "receive", "-e", receivePath)
	if err != nil {
		return "", err
	}

	// Check contents of target path is expected after receive.
	newFiles, err := os.ReadDir(receivePath)
	if err != nil {
		return "", fmt.Errorf("Failed listing contents of %q: %w", receivePath, err)
	}

	filename := ""

	// Identify the latest received path.
	for _, a := range newFiles {
		found := false

		for _, b := range files {
			if a.Name() == b.Name() {
				found = true
				break
			}
		}

		if !found {
			filename = a.Name()
			break
		}
	}

	if filename == "" {
		return "", fmt.Errorf("Failed to determine received subvolume")
	}

	subVolPath := filepath.Join(receivePath, filename)

	return subVolPath, nil
}
