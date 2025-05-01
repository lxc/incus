package drivers

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/subprocess"
)

const (
	tnToolName              = "truenas_incus_ctl"
	tnMinVersion            = "0.5.3" // `iscsi locate --activate` support
	tnVerifyDatasetCreation = false   // explicitly check that the dataset is created, work around for bugs in certain versions of the tool.
)

func (d *truenas) dataset(vol Volume, deleted bool) string {
	name, snapName, _ := api.GetParentAndSnapshotName(vol.name)

	/*
		update, we can't tell the when generating an image name, thus we MUST not append <filesytem> to an image name... unless its
		deleted... in which case we probably can.
	*/

	// need to disambiguate different images based on the root.img filesystem
	//if vol.volType == VolumeTypeImage && vol.contentType == ContentTypeFS && d.isBlockBacked(vol) {
	if deleted && vol.volType == VolumeTypeImage && ((vol.contentType == ContentTypeFS && needsFsImgVol(vol)) || isFsImgVol(vol)) {
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

	if d.config["truenas.url"] == "" && d.config["truenas.host"] != "" {
		d.config["truenas.url"] = fmt.Sprintf("wss://%s/api/current", d.config["truenas.host"])
	}

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

// zero, or >= 1GiB
func (d *truenas) setDatasetQuota(dataset string, sizeBytes int64) error {

	if sizeBytes < 0 {
		return fmt.Errorf("negative quota not allowed: %d", sizeBytes)
	}
	if sizeBytes > 0 && sizeBytes < 1073741824 {
		sizeBytes = 1073741824 // middleware rejects < 1GiB
	}

	props := []string{fmt.Sprintf("quota=%d", sizeBytes), "refquota=0", "reservation=0", "refreservation=0"}

	err := d.setDatasetProperties(dataset, props...)
	if err != nil {
		return err
	}
	return nil
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

func (d *truenas) objectsExist(objects []string, optType string) (map[string]bool, error) {
	var t string
	if optType == "" {
		t = "fs,vol,snap"
	} else if optType == "dataset" {
		t = "fs,vol"
	} else {
		t = optType
	}
	args := []string{"list", "-H", "-o", "name", "-t", t}
	args = append(args, objects...)

	out, err := d.runTool(args...)
	if err != nil {
		return nil, nil
	}

	existsMap := make(map[string]bool)
	for _, str := range objects {
		existsMap[str] = false
	}

	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if l == "" || l == "-" {
			continue
		}
		if _, exists := existsMap[l]; exists {
			existsMap[l] = true
		}
	}

	return existsMap, nil
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

	// tool does not support "all", but it also supports "nfs"
	if types == "all" {
		types = "filesystem,volume,snapshot"
	}

	out, err := d.runTool("list", "-H", "-r", "-o", "name", "-t", types, dataset)
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

// batch updates or creates one or more datasets with the same options
func (d *truenas) updateDatasets(datasets []string, orCreate bool, options ...string) error {
	args := []string{"dataset", "update"}

	// for _, option := range options {
	// 	args = append(args, "-o")
	// 	args = append(args, option)
	// }

	if orCreate {
		args = append(args, "--create")
	}

	optionString := optionsToOptionString(options...)
	if optionString != "" {
		args = append(args, "-o", optionString)
	}

	args = append(args, "--managedby", tnDefaultSettings["managedby"], "--comments", tnDefaultSettings["comments"])

	args = append(args, datasets...)

	out, err := d.runTool(args...)
	_ = out
	if err != nil {
		return err
	}

	return nil
}

// batch creates one or more datasets with the same options
func (d *truenas) createDatasets(datasets []string, options ...string) error {
	args := []string{"dataset", "create"}

	// for _, option := range options {
	// 	args = append(args, "-o")
	// 	args = append(args, option)
	// }

	optionString := optionsToOptionString(options...)
	if optionString != "" {
		args = append(args, "-o", optionString)
	}

	args = append(args, "--managedby", tnDefaultSettings["managedby"], "--comments", tnDefaultSettings["comments"])

	args = append(args, datasets...)

	out, err := d.runTool(args...)
	_ = out
	if err != nil {
		return err
	}

	return nil
}

// create a dataset by cloning a snapshot
func (d *truenas) cloneSnapshot(srcSnapshot string, destDataset string) error {
	args := []string{"snapshot", "clone", srcSnapshot, destDataset}

	// Clone the snapshot.
	_, err := d.runTool(args...)
	if err != nil {
		return err
	}

	return nil
}

// take a recursive snapshot of dataset@snapname, and optionally delete the old snapshot first
func (d *truenas) createSnapshot(snapName string, deleteFirst bool) error {
	args := []string{"snapshot", "create", "-r"}

	if deleteFirst {
		args = append(args, "--delete")
	}

	args = append(args, snapName)

	// Make the snapshot.
	out, err := d.runTool(args...)
	_ = out
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) createDataset(dataset string, options ...string) error {
	err := d.createDatasets([]string{dataset}, options...)

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

func (d *truenas) createIscsiShare(dataset string, readonly bool) error {
	args := []string{"share", "iscsi", "create", "--target-prefix=incus"}

	if readonly {
		args = append(args, "--readonly")
	}

	args = append(args, dataset)

	out, err := d.runTool(args...)
	_ = out
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) deleteIscsiShare(dataset string) error {
	out, err := d.runTool("share", "iscsi", "delete", "--target-prefix=incus", dataset)
	_ = out
	if err != nil {
		return err
	}

	return nil
}

// locates a ZFS volume if already active. Returns devpath if activated, "" if not, or an error
func (d *truenas) locateIscsiDataset(dataset string) (string, error) {

	reverter := revert.New()
	defer reverter.Fail()

	volDiskPath, err := d.runTool("share", "iscsi", "locate", "--target-prefix=incus", "--parsable", dataset)
	if err != nil {
		return "", err
	}

	volDiskPath = strings.TrimSpace(volDiskPath)

	return volDiskPath, nil
}

// activateVolume activates a ZFS volume if not already active, then returns the devpath even if already activated, and if activation was required
func (d *truenas) locateOrActivateIscsiDataset(dataset string) (bool, string, error) {
	reverter := revert.New()
	defer reverter.Fail()

	statusPath, err := d.runTool("share", "iscsi", "locate", "--activate", "--target-prefix=incus", "--parsable", dataset)
	if err != nil {
		return false, "", err
	}
	reverter.Add(func() { _ = d.deactivateIscsiDataset(dataset) })

	status, volDiskPath, found := strings.Cut(statusPath, "\t")
	if !found {
		return false, "", fmt.Errorf("No status when activating TrueNAS volume: %v", dataset)
	}

	didActivate := status == "activated"

	volDiskPath = strings.TrimSpace(volDiskPath)

	if volDiskPath != "" {
		reverter.Success()
		return didActivate, volDiskPath, nil
	}

	return false, "", fmt.Errorf("No path for activated TrueNAS volume: %v", dataset)
}

// activateVolume activates a ZFS volume if not already active. Returns devpath if activated, "" if not.
func (d *truenas) activateIscsiDataset(dataset string) (string, error) {
	reverter := revert.New()
	defer reverter.Fail()

	volDiskPath, err := d.runTool("share", "iscsi", "activate", "--target-prefix=incus", "--parsable", dataset)
	if err != nil {
		return "", err
	}
	reverter.Add(func() { _ = d.deactivateIscsiDataset(dataset) })
	volDiskPath = strings.TrimSpace(volDiskPath)

	if volDiskPath != "" {
		reverter.Success()
		return volDiskPath, nil
	}

	return "", fmt.Errorf("No path for activated TrueNAS volume: %v", dataset)
}

// deactivates a dataset if activated, returns true if deactivated
func (d *truenas) deactivateIscsiDatasetIfActive(dataset string) (bool, error) {
	statusPath, err := d.runTool("share", "iscsi", "locate", "--deactivate", "--target-prefix=incus", "--parsable", dataset)
	if err != nil {
		return false, err
	}

	if statusPath == "" {
		return false, nil
	}

	status, _, _ := strings.Cut(statusPath, "\t")

	if status != "deactivated" {
		return false, fmt.Errorf("Unexpected status when decativating TrueNAS volume: %v, '%s'", dataset, statusPath)
	}

	return true, nil

}

// deactivateVolume deactivates a ZFS volume if activate. Returns true if deactivated, false if not.
func (d *truenas) deactivateIscsiDataset(dataset string) error {
	_, err := d.runTool("share", "iscsi", "deactivate", "--target-prefix=incus", dataset)
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) deleteSnapshot(snapshot string, recursive bool, options ...string) error {
	if strings.Count(snapshot, "@") != 1 {
		return fmt.Errorf("invalid snapshot name: %s", snapshot)
	}

	return d.deleteDataset(snapshot, recursive, options...)
}

func (d *truenas) deleteDataset(dataset string, recursive bool, options ...string) error {
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

	output, err := d.runTool(d.getDatasetOrSnapshot(dataset), "list", "-H", "-p", "-o", key, dataset)

	if err != nil {
		return "", err
	}

	return strings.TrimSpace(output), nil
}

func (d *truenas) getDatasetProperties(dataset string, properties []string) (map[string]string, error) {
	response, err := d.getDatasetsAndProperties([]string{dataset}, properties)
	if err != nil {
		return nil, err
	}
	if result, exists := response[dataset]; exists {
		return result, nil
	}
	return nil, nil
}

func (d *truenas) getDatasetsAndProperties(datasets []string, properties []string) (map[string]map[string]string, error) {
	propsStr := strings.Join(properties, ",")
	out, err := d.runTool(append([]string{"list", "-j", "-p", "-o", propsStr}, datasets...)...)
	if err != nil {
		return nil, err
	}

	var response interface{}
	if err = json.Unmarshal([]byte(out), &response); err != nil {
		return nil, err
	}

	var resultsMap map[string]interface{}
	if responseMap, ok := response.(map[string]interface{}); ok {
		for _, v := range responseMap {
			if r, ok := v.(map[string]interface{}); ok {
				resultsMap = r
				break
			}
		}
	}
	if resultsMap == nil {
		return nil, fmt.Errorf("Could not find object inside list --json response")
	}

	objectsAsMap := make(map[string]bool)
	for _, obj := range datasets {
		objectsAsMap[obj] = true
	}

	outMap := make(map[string]map[string]string)
	for k, result := range resultsMap {
		if _, exists := objectsAsMap[k]; !exists {
			continue
		}
		if r, ok := result.(map[string]interface{}); ok {
			formattedMap := make(map[string]string)
			for p, v := range r {
				var value interface{}
				if vF, ok := v.(float64); ok && vF == math.Floor(vF) {
					value = int64(vF)
				} else {
					value = v
				}
				formattedMap[p] = fmt.Sprint(value)
			}

			outMap[k] = formattedMap
		}
	}

	return outMap, nil
}

// same as renameDataset, except that there's no point updating shares on a snapshot rename
func (d *truenas) renameSnapshot(sourceDataset string, destDataset string) (string, error) {
	return d.renameDataset(sourceDataset, destDataset, false)
}

// will rename a dataset, or snapshot. updateShares is relatively expensive if there is no possibility of there being a share
func (d *truenas) renameDataset(sourceDataset string, destDataset string, updateShares bool) (string, error) {
	args := []string{d.getDatasetOrSnapshot(sourceDataset), "rename"}

	if updateShares {
		args = append(args, "--update-shares")
	}

	args = append(args, sourceDataset, destDataset)

	_, err := d.runTool(args...)
	if err != nil {
		return err
	}

	if updateShares {
		_ = d.createIscsiShare(destDataset, false) // TODO: remove this when --update-shares supports iscsi
	}

	return nil
}

func (d *truenas) deleteDatasetRecursive(dataset string) error {
	// Locate the origin snapshot (if any).
	origin, err := d.getDatasetProperty(dataset, "origin")
	if err != nil {
		return err
	}

	// Delete the dataset (and any snapshots left).
	out, err := d.runTool(d.getDatasetOrSnapshot(dataset), "delete", "-r", dataset)
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

func (d *truenas) getClones(dataset string) ([]string, error) {
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
