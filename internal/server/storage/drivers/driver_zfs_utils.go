package drivers

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/lxc/incus/v6/internal/server/migration"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/ioprogress"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
)

const (
	// zfsBlockVolSuffix suffix used for block content type volumes.
	zfsBlockVolSuffix = ".block"

	// zfsISOVolSuffix suffix used for iso content type volumes.
	zfsISOVolSuffix = ".iso"

	// zfsMinBlockSize is a minimum value for recordsize and volblocksize properties.
	zfsMinBlocksize = 512

	// zfsMinBlockSize is a maximum value for recordsize and volblocksize properties.
	zfsMaxBlocksize = 16 * 1024 * 1024

	// zfsMaxVolBlocksize is a maximum value for volblocksize property.
	zfsMaxVolBlocksize = 128 * 1024
)

func (d *zfs) dataset(vol Volume, deleted bool) string {
	name, snapName, _ := api.GetParentAndSnapshotName(vol.name)

	if vol.volType == VolumeTypeImage && vol.contentType == ContentTypeFS && d.isBlockBacked(vol) {
		name = fmt.Sprintf("%s_%s", name, vol.ConfigBlockFilesystem())
	}

	if (vol.volType == VolumeTypeVM || vol.volType == VolumeTypeImage) && vol.contentType == ContentTypeBlock {
		name = fmt.Sprintf("%s%s", name, zfsBlockVolSuffix)
	} else if vol.volType == VolumeTypeCustom && vol.contentType == ContentTypeISO {
		name = fmt.Sprintf("%s%s", name, zfsISOVolSuffix)
	}

	if snapName != "" {
		if deleted {
			name = fmt.Sprintf("%s@deleted-%s", name, uuid.New().String())
		} else {
			name = fmt.Sprintf("%s@snapshot-%s", name, snapName)
		}
	} else if deleted {
		if vol.volType != VolumeTypeImage {
			name = uuid.New().String()
		}

		return filepath.Join(d.config["zfs.pool_name"], "deleted", string(vol.volType), name)
	}

	return filepath.Join(d.config["zfs.pool_name"], string(vol.volType), name)
}

func (d *zfs) createDataset(dataset string, options ...string) error {
	args := []string{"create"}
	for _, option := range options {
		args = append(args, "-o")
		args = append(args, option)
	}

	args = append(args, dataset)

	_, err := subprocess.RunCommand("zfs", args...)
	if err != nil {
		return err
	}

	return nil
}

func (d *zfs) createVolume(dataset string, size int64, options ...string) error {
	size = d.roundVolumeBlockSizeBytes(size)

	args := []string{"create", "-s", "-V", fmt.Sprintf("%d", size)}
	for _, option := range options {
		args = append(args, "-o")
		args = append(args, option)
	}

	args = append(args, dataset)

	_, err := subprocess.RunCommand("zfs", args...)
	if err != nil {
		return err
	}

	return nil
}

func (d *zfs) datasetExists(dataset string) (bool, error) {
	out, err := subprocess.RunCommand("zfs", "get", "-H", "-o", "name", "name", dataset)
	if err != nil {
		return false, nil
	}

	return strings.TrimSpace(out) == dataset, nil
}

func (d *zfs) deleteDatasetRecursive(dataset string) error {
	// Locate the origin snapshot (if any).
	origin, err := d.getDatasetProperty(dataset, "origin")
	if err != nil {
		return err
	}

	// Delete the dataset (and any snapshots left).
	_, err = subprocess.TryRunCommand("zfs", "destroy", "-r", dataset)
	if err != nil {
		return err
	}

	// Check if the origin can now be deleted.
	if origin != "" && origin != "-" {
		if strings.HasPrefix(origin, filepath.Join(d.config["zfs.pool_name"], "deleted")) {
			// Strip the snapshot name when dealing with a deleted volume.
			dataset = strings.SplitN(origin, "@", 2)[0]
		} else if strings.Contains(origin, "@deleted-") || strings.Contains(origin, "@copy-") {
			// Handle deleted snapshots.
			dataset = origin
		} else {
			// Origin is still active.
			dataset = ""
		}

		if dataset != "" {
			// Get all clones.
			clones, err := d.getClones(dataset)
			if err != nil {
				return err
			}

			if len(clones) == 0 {
				// Delete the origin.
				err = d.deleteDatasetRecursive(dataset)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (d *zfs) getClones(dataset string) ([]string, error) {
	out, err := subprocess.RunCommand("zfs", "get", "-H", "-p", "-r", "-o", "value", "clones", dataset)
	if err != nil {
		return nil, err
	}

	clones := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == dataset || line == "" || line == "-" {
			continue
		}

		line = strings.TrimPrefix(line, fmt.Sprintf("%s/", dataset))
		clones = append(clones, line)
	}

	return clones, nil
}

func (d *zfs) getDatasets(dataset string, types string) ([]string, error) {
	out, err := subprocess.RunCommand("zfs", "get", "-H", "-r", "-o", "name", "-t", types, "name", dataset)
	if err != nil {
		return nil, err
	}

	children := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == dataset || line == "" {
			continue
		}

		line = strings.TrimPrefix(line, dataset)
		children = append(children, line)
	}

	return children, nil
}

func (d *zfs) setDatasetProperties(dataset string, options ...string) error {
	args := []string{"set"}
	args = append(args, options...)
	args = append(args, dataset)

	_, err := subprocess.RunCommand("zfs", args...)
	if err != nil {
		return err
	}

	return nil
}

func (d *zfs) setBlocksizeFromConfig(vol Volume) error {
	size := vol.ExpandedConfig("zfs.blocksize")
	if size == "" {
		return nil
	}

	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(size)
	if err != nil {
		return err
	}

	return d.setBlocksize(vol, sizeBytes)
}

func (d *zfs) setBlocksize(vol Volume, size int64) error {
	if vol.contentType != ContentTypeFS {
		return nil
	}

	err := d.setDatasetProperties(d.dataset(vol, false), fmt.Sprintf("recordsize=%d", size))
	if err != nil {
		return err
	}

	return nil
}

func (d *zfs) getDatasetProperty(dataset string, key string) (string, error) {
	output, err := subprocess.RunCommand("zfs", "get", "-H", "-p", "-o", "value", key, dataset)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(output), nil
}

func (d *zfs) getDatasetProperties(dataset string, keys ...string) (map[string]string, error) {
	output, err := subprocess.RunCommand("zfs", "get", "-H", "-p", "-o", "property,value", strings.Join(keys, ","), dataset)
	if err != nil {
		return nil, err
	}

	props := make(map[string]string, len(keys))

	for _, row := range strings.Split(output, "\n") {
		prop := strings.Split(row, "\t")

		if len(prop) < 2 {
			continue
		}

		key := prop[0]
		val := prop[1]
		props[key] = val
	}

	return props, nil
}

// version returns the ZFS version based on package or kernel module version.
func (d *zfs) version() (string, error) {
	// This function is only really ever relevant on Ubuntu as the only
	// distro that ships out of sync tools and kernel modules
	out, err := subprocess.RunCommand("dpkg-query", "--showformat=${Version}", "--show", "zfsutils-linux")
	if out != "" && err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	// Loaded kernel module version
	if util.PathExists("/sys/module/zfs/version") {
		out, err := os.ReadFile("/sys/module/zfs/version")
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}

	// Module information version
	out, err = subprocess.RunCommand("modinfo", "-F", "version", "zfs")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	return "", fmt.Errorf("Could not determine ZFS module version")
}

// initialDatasets returns the list of all expected datasets.
func (d *zfs) initialDatasets() []string {
	entries := []string{"deleted"}

	// Iterate over the listed supported volume types.
	for _, volType := range d.Info().VolumeTypes {
		entries = append(entries, BaseDirectories[volType][0])
		entries = append(entries, filepath.Join("deleted", BaseDirectories[volType][0]))
	}

	return entries
}

func (d *zfs) needsRecursion(dataset string) bool {
	// Ignore snapshots for the test.
	dataset = strings.Split(dataset, "@")[0]

	entries, err := d.getDatasets(dataset, "filesystem,volume")
	if err != nil {
		return false
	}

	if len(entries) == 0 {
		return false
	}

	return true
}

func (d *zfs) sendDataset(dataset string, parent string, volSrcArgs *migration.VolumeSourceArgs, conn io.ReadWriteCloser, tracker *ioprogress.ProgressTracker) error {
	defer func() { _ = conn.Close() }()

	// Assemble zfs send command.
	args := []string{"send"}

	// Check if nesting is required.
	// We only want to use recursion (and possible raw) mode if required as it can interfere with ZFS encryption.
	if d.needsRecursion(dataset) {
		args = append(args, "-R")

		if zfsRaw {
			args = append(args, "-w")
		}
	}

	if slices.Contains(volSrcArgs.MigrationType.Features, "compress") {
		args = append(args, "-c")
		args = append(args, "-L")
	}

	if parent != "" {
		args = append(args, "-i", parent)
	}

	args = append(args, dataset)
	cmd := exec.Command("zfs", args...)

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
	output, _ := io.ReadAll(stderr)

	// Handle errors.
	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("zfs send failed: %w (%s)", err, string(output))
	}

	return nil
}

func (d *zfs) receiveDataset(vol Volume, r io.Reader, tracker *ioprogress.ProgressTracker) error {
	// Assemble zfs receive command.
	args := []string{"receive", "-x", "mountpoint", "-F", "-u", d.dataset(vol, false)}
	if vol.ContentType() == ContentTypeBlock || d.isBlockBacked(vol) {
		args = []string{"receive", "-F", "-u", d.dataset(vol, false)}
	}

	// Setup progress tracker.
	var stdin = r
	if tracker != nil {
		stdin = &ioprogress.ProgressReader{
			Reader:  r,
			Tracker: tracker,
		}
	}

	err := subprocess.RunCommandWithFds(context.TODO(), stdin, nil, "zfs", args...)
	if err != nil {
		return err
	}

	return nil
}

// ValidateZfsBlocksize validates blocksize property value on the pool.
func ValidateZfsBlocksize(value string) error {
	// Convert to bytes.
	sizeBytes, err := units.ParseByteSizeString(value)
	if err != nil {
		return err
	}

	if sizeBytes < zfsMinBlocksize || sizeBytes > zfsMaxBlocksize || (sizeBytes&(sizeBytes-1)) != 0 {
		return fmt.Errorf("Value should be between 512 and 16MiB, and be power of 2")
	}

	return nil
}

// ZFSDataset is the structure used to store information about a dataset.
type ZFSDataset struct {
	Name string `json:"name" yaml:"name"`
	GUID string `json:"guid" yaml:"guid"`
}

// ZFSMetaDataHeader is the meta data header about the datasets being sent/stored.
type ZFSMetaDataHeader struct {
	SnapshotDatasets []ZFSDataset `json:"snapshot_datasets" yaml:"snapshot_datasets"`
}

func (d *zfs) datasetHeader(vol Volume, snapshots []string) (*ZFSMetaDataHeader, error) {
	migrationHeader := ZFSMetaDataHeader{
		SnapshotDatasets: make([]ZFSDataset, len(snapshots)),
	}

	for i, snapName := range snapshots {
		snapVol, _ := vol.NewSnapshot(snapName)

		guid, err := d.getDatasetProperty(d.dataset(snapVol, false), "guid")
		if err != nil {
			return nil, err
		}

		migrationHeader.SnapshotDatasets[i].Name = snapName
		migrationHeader.SnapshotDatasets[i].GUID = guid
	}

	return &migrationHeader, nil
}

func (d *zfs) randomVolumeName(vol Volume) string {
	return fmt.Sprintf("%s_%s", vol.name, uuid.New().String())
}

func (d *zfs) delegateDataset(vol Volume, pid int) error {
	_, err := subprocess.RunCommand("zfs", "zone", fmt.Sprintf("/proc/%d/ns/user", pid), d.dataset(vol, false))
	if err != nil {
		return err
	}

	return nil
}

// ZFSSupportsDelegation returns true if the ZFS version on the system supports user namespace delegation.
func ZFSSupportsDelegation() bool {
	return zfsDelegate
}
