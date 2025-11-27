package incus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/pkg/sftp"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cancel"
	"github.com/lxc/incus/v6/shared/ioprogress"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/units"
)

// Storage volumes handling function

// GetStoragePoolVolumeNames returns the names of all volumes in a pool.
func (r *ProtocolIncus) GetStoragePoolVolumeNames(pool string) ([]string, error) {
	if !r.HasExtension("storage") {
		return nil, errors.New("The server is missing the required \"storage\" API extension")
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("/storage-pools/%s/volumes", url.PathEscape(pool))
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetStoragePoolVolumeNamesAllProjects returns the names of all volumes in a pool for all projects.
func (r *ProtocolIncus) GetStoragePoolVolumeNamesAllProjects(pool string) (map[string][]string, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	err = r.CheckExtension("storage_volumes_all_projects")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("storage-pools", pool, "volumes").WithQuery("all-projects", "true")
	_, err = r.queryStruct("GET", u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	names := make(map[string][]string)
	for _, urlString := range urls {
		resourceURL, err := url.Parse(urlString)
		if err != nil {
			return nil, fmt.Errorf("Could not parse unexpected URL %q: %w", urlString, err)
		}

		project := resourceURL.Query().Get("project")
		if project == "" {
			project = api.ProjectDefaultName
		}

		_, after, found := strings.Cut(resourceURL.Path, fmt.Sprintf("%s/", u.URL.Path))
		if !found {
			return nil, fmt.Errorf("Unexpected URL path %q", resourceURL)
		}

		names[project] = append(names[project], after)
	}

	return names, nil
}

// GetStoragePoolVolumes returns a list of StorageVolume entries for the provided pool.
func (r *ProtocolIncus) GetStoragePoolVolumes(pool string) ([]api.StorageVolume, error) {
	if !r.HasExtension("storage") {
		return nil, errors.New("The server is missing the required \"storage\" API extension")
	}

	volumes := []api.StorageVolume{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes?recursion=1", url.PathEscape(pool)), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolumesAllProjects returns a list of StorageVolume entries for the provided pool for all projects.
func (r *ProtocolIncus) GetStoragePoolVolumesAllProjects(pool string) ([]api.StorageVolume, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	err = r.CheckExtension("storage_volumes_all_projects")
	if err != nil {
		return nil, err
	}

	volumes := []api.StorageVolume{}

	uri := api.NewURL().Path("storage-pools", pool, "volumes").
		WithQuery("recursion", "1").
		WithQuery("all-projects", "true")

	// Fetch the raw value.
	_, err = r.queryStruct("GET", uri.String(), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolumesWithFilter returns a filtered list of StorageVolume entries for the provided pool.
func (r *ProtocolIncus) GetStoragePoolVolumesWithFilter(pool string, filters []string) ([]api.StorageVolume, error) {
	if !r.HasExtension("storage") {
		return nil, errors.New("The server is missing the required \"storage\" API extension")
	}

	volumes := []api.StorageVolume{}

	v := url.Values{}
	v.Set("recursion", "1")
	v.Set("filter", parseFilters(filters))
	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes?%s", url.PathEscape(pool), v.Encode()), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolumesWithFilterAllProjects returns a filtered list of StorageVolume entries for the provided pool for all projects.
func (r *ProtocolIncus) GetStoragePoolVolumesWithFilterAllProjects(pool string, filters []string) ([]api.StorageVolume, error) {
	err := r.CheckExtension("storage")
	if err != nil {
		return nil, err
	}

	err = r.CheckExtension("storage_volumes_all_projects")
	if err != nil {
		return nil, err
	}

	volumes := []api.StorageVolume{}

	uri := api.NewURL().Path("storage-pools", pool, "volumes").
		WithQuery("recursion", "1").
		WithQuery("filter", parseFilters(filters)).
		WithQuery("all-projects", "true")

	// Fetch the raw value.
	_, err = r.queryStruct("GET", uri.String(), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolumesFull returns a list of StorageVolume entries for the provided pool (full struct).
func (r *ProtocolIncus) GetStoragePoolVolumesFull(pool string) ([]api.StorageVolumeFull, error) {
	if !r.HasExtension("storage_volume_full") {
		return nil, errors.New("The server is missing the required \"storage_volume_full\" API extension")
	}

	volumes := []api.StorageVolumeFull{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes?recursion=2", url.PathEscape(pool)), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolumesFullAllProjects returns a list of StorageVolume entries for the provided pool for all projects (full struct).
func (r *ProtocolIncus) GetStoragePoolVolumesFullAllProjects(pool string) ([]api.StorageVolumeFull, error) {
	err := r.CheckExtension("storage_volume_full")
	if err != nil {
		return nil, err
	}

	volumes := []api.StorageVolumeFull{}

	uri := api.NewURL().Path("storage-pools", pool, "volumes").
		WithQuery("recursion", "2").
		WithQuery("all-projects", "true")

	// Fetch the raw value.
	_, err = r.queryStruct("GET", uri.String(), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolumesFullWithFilter returns a filtered list of StorageVolume entries for the provided pool (full struct).
func (r *ProtocolIncus) GetStoragePoolVolumesFullWithFilter(pool string, filters []string) ([]api.StorageVolumeFull, error) {
	if !r.HasExtension("storage_volume_full") {
		return nil, errors.New("The server is missing the required \"storage_volume_full\" API extension")
	}

	volumes := []api.StorageVolumeFull{}

	v := url.Values{}
	v.Set("recursion", "2")
	v.Set("filter", parseFilters(filters))
	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes?%s", url.PathEscape(pool), v.Encode()), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolumesFullWithFilterAllProjects returns a filtered list of StorageVolume entries for the provided pool for all projects (full struct).
func (r *ProtocolIncus) GetStoragePoolVolumesFullWithFilterAllProjects(pool string, filters []string) ([]api.StorageVolumeFull, error) {
	err := r.CheckExtension("storage_volume_full")
	if err != nil {
		return nil, err
	}

	volumes := []api.StorageVolumeFull{}

	uri := api.NewURL().Path("storage-pools", pool, "volumes").
		WithQuery("recursion", "2").
		WithQuery("filter", parseFilters(filters)).
		WithQuery("all-projects", "true")

	// Fetch the raw value.
	_, err = r.queryStruct("GET", uri.String(), nil, "", &volumes)
	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// GetStoragePoolVolume returns a StorageVolume entry for the provided pool and volume name.
func (r *ProtocolIncus) GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error) {
	if !r.HasExtension("storage") {
		return nil, "", errors.New("The server is missing the required \"storage\" API extension")
	}

	volume := api.StorageVolume{}

	// Fetch the raw value
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.PathEscape(pool), url.PathEscape(volType), url.PathEscape(name))
	etag, err := r.queryStruct("GET", path, nil, "", &volume)
	if err != nil {
		return nil, "", err
	}

	return &volume, etag, nil
}

// GetStoragePoolVolumeFull returns a StorageVolumeFull entry for the provided pool and volume name.
func (r *ProtocolIncus) GetStoragePoolVolumeFull(pool string, volType string, name string) (*api.StorageVolumeFull, string, error) {
	if !r.HasExtension("storage_volume_full") {
		return nil, "", errors.New("The server is missing the required \"storage_volume_full\" API extension")
	}

	volume := api.StorageVolumeFull{}

	// Fetch the raw value
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s?recursion=1", url.PathEscape(pool), url.PathEscape(volType), url.PathEscape(name))
	etag, err := r.queryStruct("GET", path, nil, "", &volume)
	if err != nil {
		return nil, "", err
	}

	return &volume, etag, nil
}

// GetStoragePoolVolumeState returns a StorageVolumeState entry for the provided pool and volume name.
func (r *ProtocolIncus) GetStoragePoolVolumeState(pool string, volType string, name string) (*api.StorageVolumeState, error) {
	if !r.HasExtension("storage_volume_state") {
		return nil, errors.New("The server is missing the required \"storage_volume_state\" API extension")
	}

	// Fetch the raw value
	state := api.StorageVolumeState{}
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/state", url.PathEscape(pool), url.PathEscape(volType), url.PathEscape(name))
	_, err := r.queryStruct("GET", path, nil, "", &state)
	if err != nil {
		return nil, err
	}

	return &state, nil
}

// CreateStoragePoolVolume defines a new storage volume.
func (r *ProtocolIncus) CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) error {
	if !r.HasExtension("storage") {
		return errors.New("The server is missing the required \"storage\" API extension")
	}

	// Send the request
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.PathEscape(pool), url.PathEscape(volume.Type))
	_, _, err := r.query("POST", path, volume, "")
	if err != nil {
		return err
	}

	return nil
}

// CreateStoragePoolVolumeSnapshot defines a new storage volume.
func (r *ProtocolIncus) CreateStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshot api.StorageVolumeSnapshotsPost) (Operation, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, errors.New("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	// Send the request
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots",
		url.PathEscape(pool),
		url.PathEscape(volumeType),
		url.PathEscape(volumeName))
	op, _, err := r.queryOperation("POST", path, snapshot, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetStoragePoolVolumeSnapshotNames returns a list of snapshot names for the
// storage volume.
func (r *ProtocolIncus) GetStoragePoolVolumeSnapshotNames(pool string, volumeType string, volumeName string) ([]string, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, errors.New("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots", url.PathEscape(pool), url.PathEscape(volumeType), url.PathEscape(volumeName))
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetStoragePoolVolumeSnapshots returns a list of snapshots for the storage
// volume.
func (r *ProtocolIncus) GetStoragePoolVolumeSnapshots(pool string, volumeType string, volumeName string) ([]api.StorageVolumeSnapshot, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, errors.New("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	snapshots := []api.StorageVolumeSnapshot{}

	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots?recursion=1",
		url.PathEscape(pool),
		url.PathEscape(volumeType),
		url.PathEscape(volumeName))
	_, err := r.queryStruct("GET", path, nil, "", &snapshots)
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}

// GetStoragePoolVolumeSnapshot returns a snapshots for the storage volume.
func (r *ProtocolIncus) GetStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string) (*api.StorageVolumeSnapshot, string, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, "", errors.New("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	snapshot := api.StorageVolumeSnapshot{}

	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots/%s",
		url.PathEscape(pool),
		url.PathEscape(volumeType),
		url.PathEscape(volumeName),
		url.PathEscape(snapshotName))
	etag, err := r.queryStruct("GET", path, nil, "", &snapshot)
	if err != nil {
		return nil, "", err
	}

	return &snapshot, etag, nil
}

// RenameStoragePoolVolumeSnapshot renames a storage volume snapshot.
func (r *ProtocolIncus) RenameStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string, snapshot api.StorageVolumeSnapshotPost) (Operation, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, errors.New("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots/%s", url.PathEscape(pool), url.PathEscape(volumeType), url.PathEscape(volumeName), url.PathEscape(snapshotName))
	// Send the request
	op, _, err := r.queryOperation("POST", path, snapshot, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStoragePoolVolumeSnapshot deletes a storage volume snapshot.
func (r *ProtocolIncus) DeleteStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string) (Operation, error) {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return nil, errors.New("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	// Send the request
	path := fmt.Sprintf(
		"/storage-pools/%s/volumes/%s/%s/snapshots/%s",
		url.PathEscape(pool), url.PathEscape(volumeType), url.PathEscape(volumeName), url.PathEscape(snapshotName))

	op, _, err := r.queryOperation("DELETE", path, nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateStoragePoolVolumeSnapshot updates the volume to match the provided StoragePoolVolume struct.
func (r *ProtocolIncus) UpdateStoragePoolVolumeSnapshot(pool string, volumeType string, volumeName string, snapshotName string, volume api.StorageVolumeSnapshotPut, ETag string) error {
	if !r.HasExtension("storage_api_volume_snapshots") {
		return errors.New("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	// Send the request
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s/snapshots/%s", url.PathEscape(pool), url.PathEscape(volumeType), url.PathEscape(volumeName), url.PathEscape(snapshotName))
	_, _, err := r.queryOperation("PUT", path, volume, ETag)
	if err != nil {
		return err
	}

	return nil
}

// MigrateStoragePoolVolume requests that Incus prepares for a storage volume migration.
func (r *ProtocolIncus) MigrateStoragePoolVolume(pool string, volume api.StorageVolumePost) (Operation, error) {
	if !r.HasExtension("storage_api_remote_volume_handling") {
		return nil, errors.New("The server is missing the required \"storage_api_remote_volume_handling\" API extension")
	}

	// Quick check.
	if !volume.Migration {
		return nil, errors.New("Can't ask for a rename through MigrateStoragePoolVolume")
	}

	var req any
	var path string

	srcVolParentName, srcVolSnapName, srcIsSnapshot := api.GetParentAndSnapshotName(volume.Name)
	if srcIsSnapshot {
		err := r.CheckExtension("storage_api_remote_volume_snapshot_copy")
		if err != nil {
			return nil, err
		}

		// Set the actual name of the snapshot without delimiter.
		req = api.StorageVolumeSnapshotPost{
			Name:      srcVolSnapName,
			Migration: volume.Migration,
			Target:    volume.Target,
		}

		path = api.NewURL().Path("storage-pools", pool, "volumes", "custom", srcVolParentName, "snapshots", srcVolSnapName).String()
	} else {
		req = volume
		path = api.NewURL().Path("storage-pools", pool, "volumes", "custom", volume.Name).String()
	}

	// Send the request
	op, _, err := r.queryOperation("POST", path, req, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

func (r *ProtocolIncus) tryMigrateStoragePoolVolume(source InstanceServer, pool string, req api.StorageVolumePost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, errors.New("The source server isn't listening on the network")
	}

	rop := remoteOperation{
		chDone: make(chan bool),
	}

	operation := req.Target.Operation

	// Forward targetOp to remote op
	go func() {
		success := false
		var errors []remoteOperationResult
		for _, serverURL := range urls {
			req.Target.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, url.PathEscape(operation))

			// Send the request
			top, err := source.MigrateStoragePoolVolume(pool, req)
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})
				continue
			}

			rop := remoteOperation{
				targetOp: top,
				chDone:   make(chan bool),
			}

			for _, handler := range rop.handlers {
				_, _ = rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})

				if localtls.IsConnectionError(err) {
					continue
				}

				break
			}

			success = true
			break
		}

		if !success {
			rop.err = remoteOperationError("Failed storage volume creation", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// tryCreateStoragePoolVolume attempts to create a storage volume in the specified storage pool.
// It will try to do this on every server in the provided list of urls, and waits for the creation to be complete.
func (r *ProtocolIncus) tryCreateStoragePoolVolume(pool string, req api.StorageVolumesPost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, errors.New("The source server isn't listening on the network")
	}

	rop := remoteOperation{
		chDone: make(chan bool),
	}

	operation := req.Source.Operation

	// Forward targetOp to remote op
	go func() {
		success := false
		var errors []remoteOperationResult
		for _, serverURL := range urls {
			req.Source.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, url.PathEscape(operation))

			// Send the request
			path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.PathEscape(pool), url.PathEscape(req.Type))
			top, _, err := r.queryOperation("POST", path, req, "")
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})
				continue
			}

			rop := remoteOperation{
				targetOp: top,
				chDone:   make(chan bool),
			}

			for _, handler := range rop.handlers {
				_, _ = rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})

				if localtls.IsConnectionError(err) {
					continue
				}

				break
			}

			success = true
			break
		}

		if !success {
			rop.err = remoteOperationError("Failed storage volume creation", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// CopyStoragePoolVolume copies an existing storage volume.
func (r *ProtocolIncus) CopyStoragePoolVolume(pool string, source InstanceServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeCopyArgs) (RemoteOperation, error) {
	if !r.HasExtension("storage_api_local_volume_handling") {
		return nil, errors.New("The server is missing the required \"storage_api_local_volume_handling\" API extension")
	}

	if args != nil && args.VolumeOnly && !r.HasExtension("storage_api_volume_snapshots") {
		return nil, errors.New("The target server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	if args != nil && args.Refresh && !r.HasExtension("custom_volume_refresh") {
		return nil, errors.New("The target server is missing the required \"custom_volume_refresh\" API extension")
	}

	if args != nil && args.RefreshExcludeOlder && !r.HasExtension("custom_volume_refresh_exclude_older_snapshots") {
		return nil, errors.New("The target server is missing the required \"custom_volume_refresh_exclude_older_snapshots\" API extension")
	}

	req := api.StorageVolumesPost{
		Name: args.Name,
		Type: volume.Type,
		Source: api.StorageVolumeSource{
			Name:                volume.Name,
			Type:                "copy",
			Pool:                sourcePool,
			VolumeOnly:          args.VolumeOnly,
			Refresh:             args.Refresh,
			RefreshExcludeOlder: args.RefreshExcludeOlder,
		},
	}

	req.Config = volume.Config
	req.Description = volume.Description
	req.ContentType = volume.ContentType

	sourceInfo, err := source.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get source connection info: %w", err)
	}

	destInfo, err := r.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get destination connection info: %w", err)
	}

	clusterInternalVolumeCopy := r.CheckExtension("cluster_internal_custom_volume_copy") == nil

	// Copy the storage pool volume locally.
	if destInfo.URL == sourceInfo.URL && destInfo.SocketPath == sourceInfo.SocketPath && (volume.Location == r.clusterTarget || (volume.Location == "none" && r.clusterTarget == "") || clusterInternalVolumeCopy) {
		// Project handling
		if destInfo.Project != sourceInfo.Project {
			if !r.HasExtension("storage_api_project") {
				return nil, errors.New("The server is missing the required \"storage_api_project\" API extension")
			}

			req.Source.Project = sourceInfo.Project
		}

		if clusterInternalVolumeCopy {
			req.Source.Location = sourceInfo.Target
		}

		// Send the request
		op, _, err := r.queryOperation("POST", fmt.Sprintf("/storage-pools/%s/volumes/%s", url.PathEscape(pool), url.PathEscape(volume.Type)), req, "")
		if err != nil {
			return nil, err
		}

		rop := remoteOperation{
			targetOp: op,
			chDone:   make(chan bool),
		}

		// Forward targetOp to remote op
		go func() {
			rop.err = rop.targetOp.Wait()
			close(rop.chDone)
		}()

		return &rop, nil
	}

	if !r.HasExtension("storage_api_remote_volume_handling") {
		return nil, errors.New("The server is missing the required \"storage_api_remote_volume_handling\" API extension")
	}

	sourceReq := api.StorageVolumePost{
		Migration: true,
		Name:      volume.Name,
		Pool:      sourcePool,
	}

	if args != nil {
		sourceReq.VolumeOnly = args.VolumeOnly
	}

	// Push mode migration
	if args != nil && args.Mode == "push" {
		// Get target server connection information
		info, err := r.GetConnectionInfo()
		if err != nil {
			return nil, err
		}

		// Set the source type and direction
		req.Source.Type = "migration"
		req.Source.Mode = "push"

		// Send the request
		path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.PathEscape(pool), url.PathEscape(volume.Type))

		// Send the request
		op, _, err := r.queryOperation("POST", path, req, "")
		if err != nil {
			return nil, err
		}

		opAPI := op.Get()

		targetSecrets := map[string]string{}
		for k, v := range opAPI.Metadata {
			val, ok := v.(string)
			if ok {
				targetSecrets[k] = val
			}
		}

		// Prepare the source request
		target := api.StorageVolumePostTarget{}
		target.Operation = opAPI.ID
		target.Websockets = targetSecrets
		target.Certificate = info.Certificate
		sourceReq.Target = &target

		return r.tryMigrateStoragePoolVolume(source, sourcePool, sourceReq, info.Addresses)
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	// Get secrets from source server
	op, err := source.MigrateStoragePoolVolume(sourcePool, sourceReq)
	if err != nil {
		return nil, err
	}

	opAPI := op.Get()

	// Prepare source server secrets for remote
	sourceSecrets := map[string]string{}
	for k, v := range opAPI.Metadata {
		val, ok := v.(string)
		if ok {
			sourceSecrets[k] = val
		}
	}

	// Relay mode migration
	if args != nil && args.Mode == "relay" {
		// Push copy source fields
		req.Source.Type = "migration"
		req.Source.Mode = "push"

		// Send the request
		path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.PathEscape(pool), url.PathEscape(volume.Type))

		// Send the request
		targetOp, _, err := r.queryOperation("POST", path, req, "")
		if err != nil {
			return nil, err
		}

		targetOpAPI := targetOp.Get()

		// Extract the websockets
		targetSecrets := map[string]string{}
		for k, v := range targetOpAPI.Metadata {
			val, ok := v.(string)
			if ok {
				targetSecrets[k] = val
			}
		}

		// Launch the relay
		err = r.proxyMigration(targetOp.(*operation), targetSecrets, source, op.(*operation), sourceSecrets)
		if err != nil {
			return nil, err
		}

		// Prepare a tracking operation
		rop := remoteOperation{
			targetOp: targetOp,
			chDone:   make(chan bool),
		}

		// Forward targetOp to remote op
		go func() {
			rop.err = rop.targetOp.Wait()
			close(rop.chDone)
		}()

		return &rop, nil
	}

	// Pull mode migration
	req.Source.Type = "migration"
	req.Source.Mode = "pull"
	req.Source.Operation = opAPI.ID
	req.Source.Websockets = sourceSecrets
	req.Source.Certificate = info.Certificate

	return r.tryCreateStoragePoolVolume(pool, req, info.Addresses)
}

// MoveStoragePoolVolume renames or moves an existing storage volume.
func (r *ProtocolIncus) MoveStoragePoolVolume(pool string, source InstanceServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeMoveArgs) (RemoteOperation, error) {
	if !r.HasExtension("storage_api_local_volume_handling") {
		return nil, errors.New("The server is missing the required \"storage_api_local_volume_handling\" API extension")
	}

	if r != source {
		return nil, errors.New("Moving storage volumes between remotes is not implemented")
	}

	req := api.StorageVolumePost{
		Name: args.Name,
		Pool: pool,
	}

	if args.Project != "" {
		if !r.HasExtension("storage_volume_project_move") {
			return nil, errors.New("The server is missing the required \"storage_volume_project_move\" API extension")
		}

		req.Project = args.Project
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.PathEscape(sourcePool), url.PathEscape(volume.Type), volume.Name), req, "")
	if err != nil {
		return nil, err
	}

	rop := remoteOperation{
		targetOp: op,
		chDone:   make(chan bool),
	}

	// Forward targetOp to remote op
	go func() {
		rop.err = rop.targetOp.Wait()
		close(rop.chDone)
	}()

	return &rop, nil
}

// UpdateStoragePoolVolume updates the volume to match the provided StoragePoolVolume struct.
func (r *ProtocolIncus) UpdateStoragePoolVolume(pool string, volType string, name string, volume api.StorageVolumePut, ETag string) error {
	if !r.HasExtension("storage") {
		return errors.New("The server is missing the required \"storage\" API extension")
	}

	if volume.Restore != "" && !r.HasExtension("storage_api_volume_snapshots") {
		return errors.New("The server is missing the required \"storage_api_volume_snapshots\" API extension")
	}

	// Send the request
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.PathEscape(pool), url.PathEscape(volType), url.PathEscape(name))
	_, _, err := r.query("PUT", path, volume, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteStoragePoolVolume deletes a storage pool.
func (r *ProtocolIncus) DeleteStoragePoolVolume(pool string, volType string, name string) error {
	if !r.HasExtension("storage") {
		return errors.New("The server is missing the required \"storage\" API extension")
	}

	// Send the request
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.PathEscape(pool), url.PathEscape(volType), url.PathEscape(name))
	_, _, err := r.query("DELETE", path, nil, "")
	if err != nil {
		return err
	}

	return nil
}

// RenameStoragePoolVolume renames a storage volume.
func (r *ProtocolIncus) RenameStoragePoolVolume(pool string, volType string, name string, volume api.StorageVolumePost) error {
	if !r.HasExtension("storage_api_volume_rename") {
		return errors.New("The server is missing the required \"storage_api_volume_rename\" API extension")
	}

	path := fmt.Sprintf("/storage-pools/%s/volumes/%s/%s", url.PathEscape(pool), url.PathEscape(volType), url.PathEscape(name))

	// Send the request
	_, _, err := r.query("POST", path, volume, "")
	if err != nil {
		return err
	}

	return nil
}

// GetStorageVolumeBackupNames returns a list of volume backup names.
func (r *ProtocolIncus) GetStorageVolumeBackupNames(pool string, volName string) ([]string, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, errors.New("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups", url.PathEscape(pool), url.PathEscape(volName))
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetStorageVolumeBackups returns a list of custom volume backups.
func (r *ProtocolIncus) GetStorageVolumeBackups(pool string, volName string) ([]api.StorageVolumeBackup, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, errors.New("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Fetch the raw value
	backups := []api.StorageVolumeBackup{}

	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups?recursion=1", url.PathEscape(pool), url.PathEscape(volName)), nil, "", &backups)
	if err != nil {
		return nil, err
	}

	return backups, nil
}

// GetStorageVolumeBackup returns a custom volume backup.
func (r *ProtocolIncus) GetStorageVolumeBackup(pool string, volName string, name string) (*api.StorageVolumeBackup, string, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, "", errors.New("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Fetch the raw value
	backup := api.StorageVolumeBackup{}
	etag, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups/%s", url.PathEscape(pool), url.PathEscape(volName), url.PathEscape(name)), nil, "", &backup)
	if err != nil {
		return nil, "", err
	}

	return &backup, etag, nil
}

// CreateStorageVolumeBackup creates new custom volume backup.
func (r *ProtocolIncus) CreateStorageVolumeBackup(pool string, volName string, backup api.StorageVolumeBackupsPost) (Operation, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, errors.New("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups", url.PathEscape(pool), url.PathEscape(volName)), backup, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// RenameStorageVolumeBackup renames a custom volume backup.
func (r *ProtocolIncus) RenameStorageVolumeBackup(pool string, volName string, name string, backup api.StorageVolumeBackupPost) (Operation, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, errors.New("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups/%s", url.PathEscape(pool), url.PathEscape(volName), url.PathEscape(name)), backup, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStorageVolumeBackup deletes a custom volume backup.
func (r *ProtocolIncus) DeleteStorageVolumeBackup(pool string, volName string, name string) (Operation, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, errors.New("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("/storage-pools/%s/volumes/custom/%s/backups/%s", url.PathEscape(pool), url.PathEscape(volName), url.PathEscape(name)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetStorageVolumeBackupFile requests the custom volume backup content.
func (r *ProtocolIncus) GetStorageVolumeBackupFile(pool string, volName string, name string, req *BackupFileRequest) (*BackupFileResponse, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, errors.New("The server is missing the required \"custom_volume_backup\" API extension")
	}

	// Build the URL
	uri := fmt.Sprintf("%s/1.0/storage-pools/%s/volumes/custom/%s/backups/%s/export", r.httpBaseURL.String(), url.PathEscape(pool), url.PathEscape(volName), url.PathEscape(name))

	// Add project/target
	uri, err := r.setQueryAttributes(uri)
	if err != nil {
		return nil, err
	}

	// Prepare the download request
	request, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}

	if r.httpUserAgent != "" {
		request.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Start the request
	response, doneCh, err := cancel.CancelableDownload(req.Canceler, r.DoHTTP, request)
	if err != nil {
		return nil, err
	}

	defer func() { _ = response.Body.Close() }()
	defer close(doneCh)

	if response.StatusCode != http.StatusOK {
		_, _, err := incusParseResponse(response)
		if err != nil {
			return nil, err
		}
	}

	// Handle the data
	body := response.Body
	if req.ProgressHandler != nil {
		body = &ioprogress.ProgressReader{
			ReadCloser: response.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length: response.ContentLength,
				Handler: func(percent int64, speed int64) {
					req.ProgressHandler(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
				},
			},
		}
	}

	size, err := io.Copy(req.BackupFile, body)
	if err != nil {
		return nil, err
	}

	resp := BackupFileResponse{}
	resp.Size = size

	return &resp, nil
}

// CreateStoragePoolVolumeFromMigration defines a new storage volume.
// In contrast to CreateStoragePoolVolume, it also returns an operation object.
func (r *ProtocolIncus) CreateStoragePoolVolumeFromMigration(pool string, volume api.StorageVolumesPost) (Operation, error) {
	// Send the request
	path := fmt.Sprintf("/storage-pools/%s/volumes/%s", url.PathEscape(pool), url.PathEscape(volume.Type))
	op, _, err := r.queryOperation("POST", path, volume, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// CreateStoragePoolVolumeFromISO creates a custom volume from an ISO file.
func (r *ProtocolIncus) CreateStoragePoolVolumeFromISO(pool string, args StorageVolumeBackupArgs) (Operation, error) {
	err := r.CheckExtension("custom_volume_iso")
	if err != nil {
		return nil, err
	}

	if args.Name == "" {
		return nil, errors.New("Missing volume name")
	}

	path := fmt.Sprintf("/storage-pools/%s/volumes/custom", url.PathEscape(pool))

	// Prepare the HTTP request.
	reqURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0%s", r.httpBaseURL.String(), path))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", reqURL, args.BackupFile)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Incus-name", args.Name)
	req.Header.Set("X-Incus-type", "iso")

	// Send the request.
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	// Handle errors.
	response, _, err := incusParseResponse(resp)
	if err != nil {
		return nil, err
	}

	// Get to the operation.
	respOperation, err := response.MetadataAsOperation()
	if err != nil {
		return nil, err
	}

	// Setup an Operation wrapper.
	op := operation{
		Operation: *respOperation,
		r:         r,
		chActive:  make(chan bool),
	}

	return &op, nil
}

// CreateStoragePoolVolumeFromBackup creates a custom volume from a backup file.
func (r *ProtocolIncus) CreateStoragePoolVolumeFromBackup(pool string, args StorageVolumeBackupArgs) (Operation, error) {
	if !r.HasExtension("custom_volume_backup") {
		return nil, errors.New(`The server is missing the required "custom_volume_backup" API extension`)
	}

	if args.Name != "" && !r.HasExtension("backup_override_name") {
		return nil, errors.New(`The server is missing the required "backup_override_name" API extension`)
	}

	path := fmt.Sprintf("/storage-pools/%s/volumes/custom", url.PathEscape(pool))

	// Prepare the HTTP request.
	reqURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0%s", r.httpBaseURL.String(), path))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", reqURL, args.BackupFile)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	if args.Name != "" {
		req.Header.Set("X-Incus-name", args.Name)
	}

	// Send the request.
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	// Handle errors.
	response, _, err := incusParseResponse(resp)
	if err != nil {
		return nil, err
	}

	// Get to the operation.
	respOperation, err := response.MetadataAsOperation()
	if err != nil {
		return nil, err
	}

	// Setup an Operation wrapper.
	op := operation{
		Operation: *respOperation,
		r:         r,
		chActive:  make(chan bool),
	}

	return &op, nil
}

// GetStoragePoolVolumeFileSFTPConn returns a connection to the volume's SFTP endpoint.
func (r *ProtocolIncus) GetStoragePoolVolumeFileSFTPConn(pool string, volType string, volName string) (net.Conn, error) {
	if !r.HasExtension("custom_volume_sftp") {
		return nil, errors.New(`The server is missing the required "custom_volume_sftp" API extension`)
	}

	u := api.NewURL()
	u.URL = r.httpBaseURL // Preload the URL with the client base URL.
	u.Path("1.0", "storage-pools", pool, "volumes", volType, volName, "sftp")
	r.setURLQueryAttributes(&u.URL)

	return r.rawSFTPConn(&u.URL)
}

// GetStoragePoolVolumeFileSFTP returns an SFTP connection to the volume.
func (r *ProtocolIncus) GetStoragePoolVolumeFileSFTP(pool string, volType string, volName string) (*sftp.Client, error) {
	if !r.HasExtension("custom_volume_sftp") {
		return nil, errors.New(`The server is missing the required "custom_volume_sftp" API extension`)
	}

	conn, err := r.GetStoragePoolVolumeFileSFTPConn(pool, volType, volName)
	if err != nil {
		return nil, err
	}

	// Get a SFTP client.
	client, err := sftp.NewClientPipe(conn, conn, sftp.MaxPacketUnchecked(128*1024))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	go func() {
		// Wait for the client to be done before closing the connection.
		_ = client.Wait()
		_ = conn.Close()
	}()

	return client, nil
}

// GetStorageVolumeFile retrieves the provided path from the storage volume.
func (r *ProtocolIncus) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *InstanceFileResponse, error) {
	// Send the request
	path := fmt.Sprintf(
		"/storage-pools/%s/volumes/%s/%s/files?path=%s",
		url.PathEscape(pool), url.PathEscape(volumeType), url.PathEscape(volumeName), url.QueryEscape(filePath),
	)

	requestURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0%s", r.httpBaseURL.String(), path))
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, nil, err
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := incusParseResponse(resp)
		if err != nil {
			return nil, nil, err
		}
	}

	// Parse the headers
	uid, gid, mode, fileType, _ := api.ParseFileHeaders(resp.Header)
	fileResp := InstanceFileResponse{
		UID:  uid,
		GID:  gid,
		Mode: mode,
		Type: fileType,
	}

	if fileResp.Type == "directory" {
		// Decode the response
		response := api.Response{}
		decoder := json.NewDecoder(resp.Body)

		err = decoder.Decode(&response)
		if err != nil {
			return nil, nil, err
		}

		// Get the file list
		entries := []string{}
		err = response.MetadataAsStruct(&entries)
		if err != nil {
			return nil, nil, err
		}

		fileResp.Entries = entries

		return nil, &fileResp, err
	}

	return resp.Body, &fileResp, err
}

// CreateStorageVolumeFile tells Incus to create a file in the storage volume.
func (r *ProtocolIncus) CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args InstanceFileArgs) error {
	// Send the request
	path := fmt.Sprintf(
		"/storage-pools/%s/volumes/%s/%s/files?path=%s",
		url.PathEscape(pool), url.PathEscape(volumeType), url.PathEscape(volumeName), url.QueryEscape(filePath),
	)

	requestURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0%s", r.httpBaseURL.String(), path))
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", requestURL, args.Content)
	if err != nil {
		return err
	}

	req.GetBody = func() (io.ReadCloser, error) {
		_, err := args.Content.Seek(0, 0)
		if err != nil {
			return nil, err
		}

		return io.NopCloser(args.Content), nil
	}

	// Set the various headers
	if args.UID > -1 {
		req.Header.Set("X-Incus-uid", fmt.Sprintf("%d", args.UID))
	}

	if args.GID > -1 {
		req.Header.Set("X-Incus-gid", fmt.Sprintf("%d", args.GID))
	}

	if args.Mode > -1 {
		req.Header.Set("X-Incus-mode", fmt.Sprintf("%04o", args.Mode))
	}

	if args.Type != "" {
		req.Header.Set("X-Incus-type", args.Type)
	}

	if args.WriteMode != "" {
		req.Header.Set("X-Incus-write", args.WriteMode)
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return err
	}

	// Check the return value for a cleaner error
	_, _, err = incusParseResponse(resp)
	if err != nil {
		return err
	}

	return nil
}

// DeleteStorageVolumeFile deletes a file in the storage volume.
func (r *ProtocolIncus) DeleteStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) error {
	// Send the request
	path := fmt.Sprintf(
		"/storage-pools/%s/volumes/%s/%s/files?path=%s",
		url.PathEscape(pool), url.PathEscape(volumeType), url.PathEscape(volumeName), url.QueryEscape(filePath),
	)

	requestURL, err := r.setQueryAttributes(path)
	if err != nil {
		return err
	}

	// Send the request
	_, _, err = r.query("DELETE", requestURL, nil, "")
	if err != nil {
		return err
	}

	return nil
}
