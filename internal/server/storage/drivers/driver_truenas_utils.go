package drivers

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

const (
	// we haven't decided what the tool should be called yet... "truenas-admin" is probably a good choice.
	tnToolName = "admin-tool"
)

func (d *truenas) dataset(vol Volume, deleted bool) string {
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

		return filepath.Join(d.config["truenas.dataset"], "deleted", string(vol.volType), name)
	}

	return filepath.Join(d.config["truenas.dataset"], string(vol.volType), name)
}

func (d *truenas) runTool(args ...string) (string, error) {
	baseArgs := []string{}

	if tnHasLoginFlags {
		if d.config["truenas.url"] != "" {
			baseArgs = append(baseArgs, "--url", d.config["truenas.url"])
		}

		if d.config["truenas.api_key"] != "" {
			baseArgs = append(baseArgs, "--api-key", d.config["truenas.api_key"])
		}

		if d.config["truenas.key_file"] != "" {
			baseArgs = append(baseArgs, "--key-file", d.config["truenas.key_file"])
		}
	}

	args = append(baseArgs, args...)

	// will allow us to prepend args
	return subprocess.RunCommand(tnToolName, args...)
}

func optionsToOptionString(options ...string) string {
	var builder strings.Builder

	for i, option := range options {
		if i > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(option)
	}
	optionString := builder.String()

	return optionString
}

/*
--exec=on
*/
func (d *truenas) setDatasetProperties(dataset string, options ...string) error {
	args := []string{"dataset", "update"}

	// TODO: either move the "--" prepending here, or have the -o syntax work!

	optionString := optionsToOptionString(options...)
	if optionString != "" {
		args = append(args, "-o", optionString)
	}

	args = append(args, dataset)

	out, err := d.runTool(args...)
	_ = out
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) datasetExists(dataset string) (bool, error) {
	//out, err := d.runTool("dataset", "get", "-H", "-o", "name", dataset)
	//out, err := d.runTool("dataset", "list", "-H", "-o", "name", dataset)
	var cmd string
	if strings.Contains(dataset, "@") {
		cmd = "snapshot"
	} else {
		cmd = "dataset"
	}

	out, err := d.runTool(cmd, "list", "-H", "-o", "name", dataset)

	if err != nil {
		return false, nil
	}

	return strings.TrimSpace(out) == dataset, nil
}

// initialDatasets returns the list of all expected datasets.
func (d *truenas) initialDatasets() []string {
	entries := []string{"deleted"}

	// Iterate over the listed supported volume types.
	for _, volType := range d.Info().VolumeTypes {
		entries = append(entries, BaseDirectories[volType][0])
		entries = append(entries, filepath.Join("deleted", BaseDirectories[volType][0]))
	}

	return entries
}

func (d *truenas) needsRecursion(dataset string) bool {
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

func (d *truenas) getDatasets(dataset string, types string) ([]string, error) {
	/*
		NOTE: types not fully implemented... yet.

		filesystem OR snapshot OR all should work.
	*/

	noun := "dataset"

	// TODO: we need to be clever to get combined/datasets+snapshots etc
	// or add it to admin-tool...
	if types == "snapshot" {
		noun = "snapshot"
	} else if types == "all" {
		// ideally, admin=tool takes care of this.
		datasets, err := d.getDatasets(dataset, "filesystem")
		if err != nil {
			return nil, err
		}
		snapshots, err := d.getDatasets(dataset, "snapshot")
		if err != nil {
			return nil, err
		}
		return append(datasets, snapshots...), nil

	}

	out, err := d.runTool(noun, "list", "-H", "-r", "-o", "name", dataset)
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

func (d *truenas) createDataset(dataset string, options ...string) error {
	args := []string{"dataset", "create"}

	// for _, option := range options {
	// 	args = append(args, "-o")
	// 	args = append(args, option)
	// }

	optionString := optionsToOptionString(options...)
	if optionString != "" {
		args = append(args, "-o", optionString)
	}

	args = append(args, dataset)

	out, err := d.runTool(args...)
	_ = out
	if err != nil {
		return err
	}

	// TODO: `dataset create` doesn't currently implement error handling! so we need to chec if it worked!

	exists, _ := d.datasetExists(dataset)
	if !exists {
		return fmt.Errorf("Failed to createDataset: %s", dataset)
	}

	return nil
}

func (d *truenas) createNfsShare(dataset string) error {
	if tnHasShareNfs {
		args := []string{"share", "nfs", "create"}
		args = append(args, "--comment", "Managed By Incus", "--maproot-user", "root", "--maproot-group", "root")
		args = append(args, dataset)

		out, err := d.runTool(args...)
		_ = out
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *truenas) deleteNfsShare(dataset string) error {
	if tnHasNfsDeleteByDataset {
		out, err := d.runTool("share", "nfs", "delete", dataset)
		_ = out
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *truenas) deleteDataset(dataset string, options ...string) error {
	args := []string{"dataset", "delete"}

	// for _, option := range options {
	// 	args = append(args, "-o")
	// 	args = append(args, option)
	// }

	args = append(args, dataset)

	_, err := d.runTool(args...)
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) getDatasetProperty(dataset string, key string) (string, error) {

	//output, err := subprocess.RunCommand("zfs", "get", "-H", "-p", "-o", "value", key, dataset)
	output, err := d.runTool("dataset", "list", "-H" /*"-p",*/, "-o", key, dataset)

	if err != nil {
		return "", err
	}

	return strings.TrimSpace(output), nil
}

// same as renameDataset, except that there's no point updating shares on a snapshot rename
func (d *truenas) renameSnapshot(sourceDataset string, destDataset string) (string, error) {
	return d.renameDataset(sourceDataset, destDataset, false)
}

// will rename a dataset, or snapshot. updateShares is relatively expensive if there is no possibility of there being a share
func (d *truenas) renameDataset(sourceDataset string, destDataset string, updateShares bool) (string, error) {
	args := []string{"dataset", "rename"}

	if updateShares && tnHasUpdateShares {
		args = append(args, "--update-shares")
	}

	args = append(args, sourceDataset, destDataset)

	return d.runTool(args...)
}

func (d *truenas) deleteDatasetRecursive(dataset string) error {
	// Locate the origin snapshot (if any).
	origin, err := d.getDatasetProperty(dataset, "origin")
	if err != nil {
		return err
	}

	// Delete the dataset (and any snapshots left).
	//_, err = subprocess.TryRunCommand("zfs", "destroy", "-r", dataset)
	out, err := d.runTool("dataset", "delete", "-r", dataset)
	_ = out
	if err != nil {
		return err
	}

	// Check if the origin can now be deleted.
	if origin != "" && origin != "-" {
		if strings.HasPrefix(origin, filepath.Join(d.config["truenas.dataset"], "deleted")) {
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

// activateVolume activates a ZFS volume if not already active. Returns true if activated, false if not.
func (d *truenas) activateVolume(vol Volume) (bool, error) {
	if !IsContentBlock(vol.contentType) && !vol.IsBlockBacked() {
		return false, nil // Nothing to do for non-block or non-block backed volumes.
	}

	reverter := revert.New()
	defer reverter.Fail()

	dataset := d.dataset(vol, false)

	// Check if already active.
	current, err := d.getDatasetProperty(dataset, "volmode")
	if err != nil {
		return false, err
	}

	if current != "dev" {
		// For block backed volumes, we make their associated device appear.
		err = d.setDatasetProperties(dataset, "volmode=dev")
		if err != nil {
			return false, err
		}

		reverter.Add(func() { _ = d.setDatasetProperties(dataset, fmt.Sprintf("volmode=%s", current)) })

		// Wait up to 30 seconds for the device to appear.
		ctx, cancel := context.WithTimeout(d.state.ShutdownCtx, 30*time.Second)
		defer cancel()

		_, err := d.tryGetVolumeDiskPathFromDataset(ctx, dataset)
		if err != nil {
			return false, fmt.Errorf("Failed to activate volume: %v", err)
		}

		d.logger.Debug("Activated ZFS volume", logger.Ctx{"volName": vol.Name(), "dev": dataset})

		reverter.Success()
		return true, nil
	}

	return false, nil
}

// deactivateVolume deactivates a ZFS volume if activate. Returns true if deactivated, false if not.
func (d *truenas) deactivateVolume(vol Volume) (bool, error) {
	if vol.contentType != ContentTypeBlock && !vol.IsBlockBacked() {
		return false, nil // Nothing to do for non-block and non-block backed volumes.
	}

	dataset := d.dataset(vol, false)

	// Check if currently active.
	current, err := d.getDatasetProperty(dataset, "volmode")
	if err != nil {
		return false, err
	}

	if current == "dev" {
		devPath, err := d.GetVolumeDiskPath(vol)
		if err != nil {
			return false, fmt.Errorf("Failed locating zvol for deactivation: %w", err)
		}

		// We cannot wait longer than the operationlock.TimeoutShutdown to avoid continuing
		// the unmount process beyond the ongoing request.
		waitDuration := time.Minute * 5
		waitUntil := time.Now().Add(waitDuration)
		i := 0
		for {
			// Sometimes it takes multiple attempts for ZFS to actually apply this.
			err = d.setDatasetProperties(dataset, "volmode=none")
			if err != nil {
				return false, err
			}

			if !util.PathExists(devPath) {
				d.logger.Debug("Deactivated ZFS volume", logger.Ctx{"volName": vol.name, "dev": dataset})
				break
			}

			if time.Now().After(waitUntil) {
				return false, fmt.Errorf("Failed to deactivate zvol after %v", waitDuration)
			}

			// Wait for ZFS a chance to flush and udev to remove the device path.
			d.logger.Debug("Waiting for ZFS volume to deactivate", logger.Ctx{"volName": vol.name, "dev": dataset, "path": devPath, "attempt": i})

			if i <= 5 {
				// Retry more quickly early on.
				time.Sleep(time.Second * time.Duration(i))
			} else {
				time.Sleep(time.Second * time.Duration(5))
			}

			i++
		}

		return true, nil
	}

	return false, nil
}

func (d *truenas) version() (string, error) {
	out, err := subprocess.RunCommand(tnToolName, "version")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	return "", fmt.Errorf("Could not determine TrueNAS driver version")
}

func (d *truenas) setBlocksizeFromConfig(vol Volume) error {
	// size := vol.ExpandedConfig("zfs.blocksize")
	// if size == "" {
	// 	return nil
	// }

	// Convert to bytes.
	// sizeBytes, err := units.ParseByteSizeString(size)
	// if err != nil {
	// 	return err
	// }

	// return d.setBlocksize(vol, sizeBytes)

	return nil
}

func (d *truenas) setBlocksize(vol Volume, size int64) error {
	if vol.contentType != ContentTypeFS {
		return nil
	}

	err := d.setDatasetProperties(d.dataset(vol, false), fmt.Sprintf("recordsize=%d", size))
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) createVolume(dataset string, size int64, options ...string) error {
	// args := []string{"create", "-s", "-V", fmt.Sprintf("%d", size)}
	// for _, option := range options {
	// 	args = append(args, "-o")
	// 	args = append(args, option)
	// }

	// args = append(args, dataset)

	// _, err := subprocess.RunCommand("zfs", args...)
	// if err != nil {
	// 	return err
	// }

	return nil
}

func (d *truenas) getClones(dataset string) ([]string, error) {
	//out, err := subprocess.RunCommand("zfs", "get", "-H", "-p", "-r", "-o", "value", "clones", dataset)
	out, err := d.runTool("snapshot", "list", "-H", "-r", "-o", "clones", dataset)

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

func (d *truenas) randomVolumeName(vol Volume) string {
	return fmt.Sprintf("%s_%s", vol.name, uuid.New().String())
}
