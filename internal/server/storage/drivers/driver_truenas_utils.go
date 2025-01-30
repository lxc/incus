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

		return filepath.Join(d.config["zfs.pool_name"], "deleted", string(vol.volType), name)
	}

	return filepath.Join(d.config["zfs.pool_name"], string(vol.volType), name)
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

func (d *truenas) datasetExists(dataset string) (bool, error) {
	out, err := d.runTool("dataset", "get", "-H", "-o", "name", dataset)
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

func (d *truenas) getDatasets(dataset string, types string) ([]string, error) {
	// NOTE: types not implemented... yet.
	out, err := d.runTool("dataset", "list", "-H", "-r", "-o", "name", dataset)
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

	args = append(args, dataset)

	_, err := d.runTool(args...)
	if err != nil {
		return err
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

func (d *truenas) version() (string, error) {
	out, err := subprocess.RunCommand(tnToolName, "version")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	return "", fmt.Errorf("Could not determine TrueNAS driver version")
}
