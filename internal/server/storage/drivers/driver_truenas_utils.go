package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	tnToolName            = "truenas_incus_ctl"
	tnMinVersion          = "0.7.2" // deactivate --wait with sync functionality
	tnDefaultVolblockSize = 16 * 1024
)

func (d *truenas) dataset(vol Volume, deleted bool) string {
	name, snapName, _ := api.GetParentAndSnapshotName(vol.name)

	if vol.volType == VolumeTypeImage && vol.contentType == ContentTypeFS {
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

// runTool runs the truenas control tool with the supplied arguments, whilst applying the global flags as appropriate.
func (d *truenas) runTool(args ...string) (string, error) {
	baseArgs := []string{}

	if util.IsTrue(d.config["truenas.allow_insecure"]) {
		baseArgs = append(baseArgs, "--allow-insecure")
	}

	if d.config["truenas.api_key"] != "" {
		baseArgs = append(baseArgs, "--api-key", d.config["truenas.api_key"])
	}

	if d.config["truenas.config"] != "" {
		baseArgs = append(baseArgs, "--config", d.config["truenas.config"])
	}

	if d.config["truenas.host"] != "" {
		baseArgs = append(baseArgs, "--host", d.config["truenas.host"])
	}

	args = append(baseArgs, args...)

	out, err := subprocess.RunCommand(tnToolName, args...)

	if err != nil && strings.Contains(err.Error(), "Post \"http://unix/tnc-daemon\": EOF)") {
		// this error indicates that the connection to the server was closed when the command was posted. It should be safe to retry the command
		// the daemon *should've* re-opened the connection, but as of 0.7.2 it doesn't, re-trying should force the connection to be re-opened.
		d.logger.Error("TrueNAS Tool POST failed with socket EOF, will retry", logger.Ctx{"err": err})
		out, err = subprocess.RunCommand(tnToolName, args...)
	}

	// will allow us to prepend args
	return out, err
}

// runIscsiCmd runs the supplied args against the tools `share iscsi` command whilst applying the appropriate iscsi global flags.
func (d *truenas) runIscsiCmd(cmd string, args ...string) (string, error) {
	baseArgs := []string{"share", "iscsi", cmd}

	baseArgs = append(baseArgs, "--target-prefix=incus")

	if d.config["truenas.portal"] != "" {
		baseArgs = append(baseArgs, "--portal", d.config["truenas.portal"])
	}

	if d.config["truenas.initiator"] != "" {
		baseArgs = append(baseArgs, "--initiator", d.config["truenas.initiator"])
	}

	args = append(baseArgs, args...)

	return d.runTool(args...)
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

	// optionString := optionsToOptionString(options...)
	// if optionString != "" {
	// 	args = append(args, "-o", optionString)
	// }

	for _, option := range options {
		args = append(args, fmt.Sprintf("--%s", option))
	}

	args = append(args, dataset)

	out, err := d.runTool(args...)
	_ = out
	if err != nil {
		return err
	}

	return nil
}

// getDatasetOrSnapshotreturns "dataset" or "snapshot" depending on the supplied name
// used to disambiguate truenas-admin commands.
func (d *truenas) getDatasetOrSnapshot(dataset string) string {
	if strings.Contains(dataset, "@") {
		return "snapshot"
	}

	return "dataset"
}

func (d *truenas) datasetExists(dataset string) (bool, error) {
	out, err := d.runTool(d.getDatasetOrSnapshot(dataset), "list", "--no-headers", "-o", "name", dataset)
	if err != nil {
		return false, nil // TODO: need to check if tool returns errors for bad connections, vs not-found. Ie, this occurs when recovering with a bad API key or HOST.
	}

	return strings.TrimSpace(out) == dataset, nil
}

// objectsExist returns a map of existence for a number of objects (snaps, vols, etc).
// unused currently but planned to be used in DeleteVolume.
func (d *truenas) objectsExist(objects []string, optType string) (map[string]bool, error) { //nolint:unused
	var t string

	// unlike zfs, `list` will return nfs and other objects for all.
	switch optType {
	case "":
		t = "fs,vol,snap"
	case "dataset":
		t = "fs,vol"
	default:
		t = optType
	}

	args := []string{"list", "--no-headers", "-o", "name", "-t", t}
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

		_, exists := existsMap[l]
		if exists {
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

func (d *truenas) getDatasets(dataset string, types string) ([]string, error) {
	// tool does not support "all", but it also supports "nfs"
	if types == "all" {
		types = "filesystem,volume,snapshot"
	}

	out, err := d.runTool("list", "--no-headers", "-r", "-o", "name", "-t", types, dataset)
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

// updateDatasets batch updates or creates one or more datasets with the same options.
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

// createDatasets batch creates one or more datasets with the same options.
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

// cloneSnapshot create a dataset by cloning a snapshot.
func (d *truenas) cloneSnapshot(srcSnapshot string, destDataset string) error {
	args := []string{"snapshot", "clone", srcSnapshot, destDataset}

	// Clone the snapshot.
	_, err := d.runTool(args...)
	if err != nil {
		return err
	}

	return nil
}

// createSnapshot take a recursive snapshot of dataset@snapname, and optionally delete the old snapshot first.
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

	return nil
}

func (d *truenas) createVolume(dataset string, size int64, options ...string) error {
	args := []string{"dataset", "create", "-s", "-V", fmt.Sprintf("%d", size)}

	// for _, option := range options {
	// 	args = append(args, "-o")
	// 	args = append(args, option)
	// }

	for _, option := range options {
		args = append(args, fmt.Sprintf("--%s", option))
	}

	// optionString := optionsToOptionString(options...)
	// if optionString != "" {
	// 	args = append(args, "-o", optionString)
	// }

	args = append(args, "--managedby", tnDefaultSettings["managedby"], "--comments", tnDefaultSettings["comments"])

	args = append(args, dataset)

	out, err := d.runTool(args...)
	_ = out
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) verifyIscsiFunctionality(ensureSetup bool) error {
	args := []string{"--parsable"}

	if ensureSetup {
		args = append(args, "--setup")
	}

	_, err := d.runIscsiCmd("test", args...)
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) createIscsiShare(dataset string, readonly bool) error {
	args := []string{}

	if readonly {
		args = append(args, "--readonly")
	}

	args = append(args, dataset)

	_, err := d.runIscsiCmd("create", args...)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			d.logger.Debug(fmt.Sprintf("Detected error while attempting to create iscsi share for: %s, %v", dataset, err))

			// there's a race when obtaining an iscsi id in `iscsi create`, lets try sleeping for a bit, and retrying. (NAS-135784)
			time.Sleep(500 * time.Millisecond)
			return d.createIscsiShare(dataset, readonly)
		}

		return err
	}

	return nil
}

func (d *truenas) deleteIscsiShare(dataset string) error {
	_, err := d.runIscsiCmd("delete", dataset) // implies `deactivate --wait`
	if err != nil {
		return err
	}

	return nil
}

// locateIscsiDataset locates a ZFS volume if already active. Returns devpath if activated, "" if not, or an error.
func (d *truenas) locateIscsiDataset(dataset string) (string, error) {
	reverter := revert.New()
	defer reverter.Fail()

	statusPath, err := d.runIscsiCmd("locate", "--parsable", dataset)
	if err != nil {
		return "", err
	}

	status, volDiskPath, found := strings.Cut(statusPath, "\t")
	if !found {
		// early versions of locate returned no status.
		volDiskPath = status
	}

	volDiskPath = strings.TrimSpace(volDiskPath)

	return volDiskPath, nil
}

// locateOrActivateIscsiDataset ensures a dataset is activated, and returns the dev path. Will create
// the share if necessary and returns a bool to determine if activation was required.
func (d *truenas) locateOrActivateIscsiDataset(dataset string) (bool, string, error) {
	reverter := revert.New()
	defer reverter.Fail()

	statusPath, err := d.runIscsiCmd("locate", "--create", "--parsable", dataset) // --create implies activate
	if err != nil {
		return false, "", err
	}

	reverter.Add(func() { _ = d.deactivateIscsiDataset(dataset) })

	status, volDiskPath, _ := strings.Cut(statusPath, "\t")
	didCreate := false

	// when `locate --create` has to create a share, it outputs two lines, one for the creation, a second for the activation, we need to discard the first.
	if status == "created" {
		d.logger.Debug(fmt.Sprintf("Created iscsi share for TrueNAS volume: %s", volDiskPath))
		didCreate = true
		_, statusPath, _ := strings.Cut(statusPath, "\n")
		status, volDiskPath, _ = strings.Cut(statusPath, "\t")
	}

	didActivate := status == "activated"
	volDiskPath = strings.TrimSpace(volDiskPath)

	if volDiskPath != "" {
		reverter.Success()
		return didActivate, volDiskPath, nil
	}

	if didCreate {
		return false, "", fmt.Errorf("Successfully created, but was unable to activate TrueNAS volume: %v, perhaps there is an iSCSI communication issue?", dataset)
	}

	return false, "", fmt.Errorf("Unable to create, activate or locate TrueNAS volume: %v, ", dataset)
}

// activateVolume activates a ZFS volume if not already active. Returns devpath if activated, "" if not.
func (d *truenas) activateIscsiDataset(dataset string) (string, error) { //nolint:unused
	reverter := revert.New()
	defer reverter.Fail()

	volDiskPath, err := d.runIscsiCmd("activate", "--parsable", dataset)
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

// deactivateIscsiDatasetIfActive deactivates a dataset if activated, returns true if deactivated.
func (d *truenas) deactivateIscsiDatasetIfActive(dataset string) (bool, error) {
	statusPath, err := d.runIscsiCmd("locate", "--deactivate", "--parsable", "--wait", dataset)
	if err != nil {
		return false, err
	}

	status, _, _ := strings.Cut(statusPath, "\t")

	if status == "failed" || status == "" {
		return false, nil
	}

	if status != "deactivated" {
		return false, fmt.Errorf("Unexpected status when deactivating TrueNAS volume: %v, '%s'", dataset, statusPath)
	}

	return true, nil
}

// deactivateIscsiDataset deactivates an iscsi share if active.
func (d *truenas) deactivateIscsiDataset(dataset string) error {
	_, err := d.deactivateIscsiDatasetIfActive(dataset)
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

// tryDeleteBusyDataset attempts to delete a dataset, repeating if busy until success, or the context is ended.
func (d *truenas) tryDeleteBusyDataset(ctx context.Context, dataset string, recursive bool, options ...string) error {
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("Failed to delete dataset for %q: %w", dataset, ctx.Err())
		}

		// we sometimes we receive a "busy" error when deleting... which I think is a race, although iSCSI should've finished with the zvol by the time
		// deleteIscsiShare returns, maybe it hasn't yet... so we retry... in general if incus is calling deleteDataset it shouldn't be busy.
		err := d.deleteDataset(dataset, recursive, options...)
		if err == nil {
			return nil
		}

		/*
			Error -32001
			Method call error
			[EBUSY] Failed to delete dataset: cannot destroy '<dataset>': dataset is busy)
		*/
		if !strings.Contains(err.Error(), "[EBUSY]") {
			return err
		}

		d.logger.Warn("Error while trying to delete dataset, will retry", logger.Ctx{"dataset": dataset, "err": err})

		// was busy, lets try again.
		time.Sleep(500 * time.Millisecond)
	}
}

func (d *truenas) deleteDataset(dataset string, recursive bool, options ...string) error {
	args := []string{d.getDatasetOrSnapshot(dataset), "delete"}

	if recursive {
		args = append(args, "-r")
	}

	for _, option := range options {
		args = append(args, fmt.Sprintf("--%s", option))
	}

	args = append(args, dataset)

	_, err := d.runTool(args...)
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) getDatasetProperty(dataset string, key string) (string, error) {
	output, err := d.runTool(d.getDatasetOrSnapshot(dataset), "list", "--no-headers", "--parsable", "-o", key, dataset)
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

	result, exists := response[dataset]
	if exists {
		return result, nil
	}

	return nil, nil
}

func (d *truenas) getDatasetsAndProperties(datasets []string, properties []string) (map[string]map[string]string, error) {
	propsStr := strings.Join(properties, ",")
	out, err := d.runTool(append([]string{"list", "--json", "--parsable", "-o", propsStr}, datasets...)...)
	if err != nil {
		return nil, err
	}

	var response any
	if err = json.Unmarshal([]byte(out), &response); err != nil {
		return nil, err
	}

	var resultsMap map[string]any
	responseMap, ok := response.(map[string]any)
	if ok {
		for _, v := range responseMap {
			r, ok := v.(map[string]any)
			if ok {
				resultsMap = r
				break
			}
		}
	}

	if resultsMap == nil {
		return nil, errors.New("Could not find object inside list --json response")
	}

	objectsAsMap := make(map[string]bool)
	for _, obj := range datasets {
		objectsAsMap[obj] = true
	}

	outMap := make(map[string]map[string]string)
	for k, result := range resultsMap {
		_, exists := objectsAsMap[k]
		if !exists {
			continue
		}

		r, ok := result.(map[string]any)
		if ok {
			formattedMap := make(map[string]string)
			for p, v := range r {
				var value any
				vF, ok := v.(float64)
				if ok && vF == math.Floor(vF) {
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

// renameSnapshot renames sourceSnapshot to destSnapshot.
// sourceSnapshot: <dataset>@<snap-name>.
// destSnapshot: [dataset]@<snap-name>.
func (d *truenas) renameSnapshot(sourceSnapshot string, destSnapshot string) error {
	args := []string{"snapshot", "rename", sourceSnapshot, destSnapshot}

	_, err := d.runTool(args...)
	if err != nil {
		return err
	}

	return nil
}

// renameDatasetwill rename a dataset, or snapshot. updateShares is relatively expensive if there is no possibility of there being a share.
func (d *truenas) renameDataset(sourceDataset string, destDataset string, updateShares bool) error {
	args := []string{d.getDatasetOrSnapshot(sourceDataset), "rename"}

	if updateShares {
		_ = d.deleteIscsiShare(sourceDataset) // TODO: remove this when --update-shares supports iscsi
		args = append(args, "--update-shares")
	}

	args = append(args, sourceDataset, destDataset)

	_, err := d.runTool(args...)
	if err != nil {
		return err
	}

	return nil
}

func (d *truenas) deleteDatasetRecursive(dataset string) error {
	// Locate the origin snapshot (if any).
	origin, err := d.getDatasetProperty(dataset, "origin")
	if err != nil {
		return err
	}

	err = d.deleteIscsiShare(dataset)
	if err != nil {
		return err
	}

	// Try delete the dataset (and any snapshots left), waiting up to 5 seconds if its busy
	ctx, cancel := context.WithTimeout(d.state.ShutdownCtx, 5*time.Second)
	defer cancel()
	err = d.tryDeleteBusyDataset(ctx, dataset, true)
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

	return "", errors.New("Could not determine TrueNAS driver version")
}

// setVolsize sets the volsize property of a zvol, optionally ignoring shrink errors (and warning), requires a zvol.
func (d *truenas) setVolsize(dataset string, sizeBytes int64, allowShrink bool) error {
	ignoreShrinkError := true

	volsizeProp := fmt.Sprintf("--volsize=%d", sizeBytes)
	args := []string{"dataset", "update", volsizeProp}

	if allowShrink {
		// although the middleware doesn't currently support shrinking, when it does, the tool will support it via this flag.
		args = append(args, "--allow-shrinking")
	}

	args = append(args, dataset)

	_, err := d.runTool(args...)
	if err != nil {
		if !ignoreShrinkError || !strings.Contains(err.Error(), "cannot shrink a zvol") {
			return err
		}
		// middleware currently prevents volume shrinking.
		d.logger.Warn(fmt.Sprintf("Unable to shrink zvol on TrueNAS server due to middleware restriction, use `zfs set %s %s` to change zvol size manually", volsizeProp, dataset))
	}

	return nil
}

func (d *truenas) getClones(dataset string) ([]string, error) {
	out, err := d.runTool("snapshot", "list", "--no-headers", "--parsable", "-r", "-o", "clones", dataset)
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
