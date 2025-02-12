package drivers

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/subprocess"
)

const (
	tnToolName              = "truenas-admin"
	tnMinVersion            = "0.2.0"
	tnVerifyDatasetCreation = false // explicitly check that the dataset is created, work around for bugs in certain versions of the tool.
)

func (d *truenas) dataset(vol Volume, deleted bool) string {
	name, snapName, _ := api.GetParentAndSnapshotName(vol.name)

	if vol.volType == VolumeTypeImage && vol.contentType == ContentTypeFS && d.isBlockBacked(vol) {
		name = fmt.Sprintf("%s_%s", name, vol.ConfigBlockFilesystem())
	}

	if (vol.volType == VolumeTypeVM || vol.volType == VolumeTypeImage) && vol.contentType == ContentTypeBlock {
		//name = fmt.Sprintf("%s%s", name, zfsBlockVolSuffix)
	} else if vol.volType == VolumeTypeCustom && vol.contentType == ContentTypeISO {
		//name = fmt.Sprintf("%s%s", name, zfsISOVolSuffix)
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

	if d.config["truenas.url"] != "" {
		baseArgs = append(baseArgs, "--url", d.config["truenas.url"])
	}

	if d.config["truenas.api_key"] != "" {
		baseArgs = append(baseArgs, "--api-key", d.config["truenas.api_key"])
	}

	if d.config["truenas.key_file"] != "" {
		baseArgs = append(baseArgs, "--key-file", d.config["truenas.key_file"])
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

// returns "dataset" or "snapshot" depending on the supplied name
// used to disambiguate truenas-admin commands
func (d *truenas) getDatasetOrSnapshot(dataset string) string {
	if strings.Contains(dataset, "@") {
		return "snapshot"
	}

	return "dataset"
}

func (d *truenas) datasetExists(dataset string) (bool, error) {
	//out, err := d.runTool("dataset", "get", "-H", "-o", "name", dataset)
	//out, err := d.runTool("dataset", "list", "-H", "-o", "name", dataset)
	out, err := d.runTool(d.getDatasetOrSnapshot(dataset), "list", "-H", "-o", "name", dataset)

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
	// or add it to truenas-admin...
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

	args = append(args, "--managedby", tnDefaultSettings["managedby"], "--comments", tnDefaultSettings["comments"], dataset)

	out, err := d.runTool(args...)
	_ = out
	if err != nil {
		return err
	}

	// previous versions of the tool didnt' properly handle dataset creation failure
	if tnVerifyDatasetCreation {
		exists, _ := d.datasetExists(dataset)
		if !exists {
			return fmt.Errorf("Failed to createDataset: %s", dataset)
		}
	}

	return nil
}

func (d *truenas) createNfsShare(dataset string) error {
	args := []string{"share", "nfs"}

	/*
		`update --create` will create a share if it does not exist, with the supplied props, otherwise
		it will update the share with the supplied props if the share does not yet have them.

		This allows a share to be created, or even updated, "just-in-time" before mounting, as the tool will first lookup
		the share's existance, before modifying it, without risking duplicating the share

		This also means that if we add additional flags, or change them in the future to the share, they can be applied
	*/
	args = append(args, "update", "--create")
	args = append(args, "--comment", tnDefaultSettings["comments"], "--maproot-user=root", "--maproot-group=root")
	args = append(args, dataset)

	out, err := d.runTool(args...)
	_ = out
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) deleteNfsShare(dataset string) error {
	out, err := d.runTool("share", "nfs", "delete", dataset)
	_ = out
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) deleteDataset(dataset string, options ...string) error {
	args := []string{d.getDatasetOrSnapshot(dataset), "delete"}

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
	output, err := d.runTool(d.getDatasetOrSnapshot(dataset), "list", "-H", "-p", "-o", key, dataset)

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

	if updateShares {
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
	out, err := d.runTool(d.getDatasetOrSnapshot((dataset)), "delete", "-r", dataset)
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
	out, err := d.runTool("snapshot", "list", "-H", "-p", "-r", "-o", "clones", dataset)

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
