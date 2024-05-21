package incus

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/pkg/sftp"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cancel"
	"github.com/lxc/incus/v6/shared/ioprogress"
	"github.com/lxc/incus/v6/shared/tcp"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/ws"
)

// Instance handling functions.

// instanceTypeToPath converts the instance type to a URL path prefix and query string values.
func (r *ProtocolIncus) instanceTypeToPath(instanceType api.InstanceType) (string, url.Values, error) {
	v := url.Values{}

	// If a specific instance type has been requested, add the instance-type filter parameter
	// to the returned URL values so that it can be used in the final URL if needed to filter
	// the result set being returned.
	if instanceType != api.InstanceTypeAny {
		v.Set("instance-type", string(instanceType))
	}

	return "/instances", v, nil
}

// GetInstanceNames returns a list of instance names.
func (r *ProtocolIncus) GetInstanceNames(instanceType api.InstanceType) ([]string, error) {
	baseURL, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", baseURL, v.Encode()), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetInstanceNamesAllProjects returns a list of instance names from all projects.
func (r *ProtocolIncus) GetInstanceNamesAllProjects(instanceType api.InstanceType) (map[string][]string, error) {
	instances := []api.Instance{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	v.Set("recursion", "1")
	v.Set("all-projects", "true")

	// Fetch the raw URL values.
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	names := map[string][]string{}
	for _, instance := range instances {
		names[instance.Project] = append(names[instance.Project], instance.Name)
	}

	return names, nil
}

// GetInstances returns a list of instances.
func (r *ProtocolIncus) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	instances := []api.Instance{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	v.Set("recursion", "1")

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// GetInstancesWithFilter returns a filtered list of instances.
func (r *ProtocolIncus) GetInstancesWithFilter(instanceType api.InstanceType, filters []string) ([]api.Instance, error) {
	if !r.HasExtension("api_filtering") {
		return nil, fmt.Errorf("The server is missing the required \"api_filtering\" API extension")
	}

	instances := []api.Instance{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	v.Set("recursion", "1")
	v.Set("filter", parseFilters(filters))

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// GetInstancesAllProjects returns a list of instances from all projects.
func (r *ProtocolIncus) GetInstancesAllProjects(instanceType api.InstanceType) ([]api.Instance, error) {
	instances := []api.Instance{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	v.Set("recursion", "1")
	v.Set("all-projects", "true")

	if !r.HasExtension("instance_all_projects") {
		return nil, fmt.Errorf("The server is missing the required \"instance_all_projects\" API extension")
	}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// GetInstancesAllProjectsWithFilter returns a filtered list of instances from all projects.
func (r *ProtocolIncus) GetInstancesAllProjectsWithFilter(instanceType api.InstanceType, filters []string) ([]api.Instance, error) {
	if !r.HasExtension("api_filtering") {
		return nil, fmt.Errorf("The server is missing the required \"api_filtering\" API extension")
	}

	instances := []api.Instance{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	v.Set("recursion", "1")
	v.Set("all-projects", "true")
	v.Set("filter", parseFilters(filters))

	if !r.HasExtension("instance_all_projects") {
		return nil, fmt.Errorf("The server is missing the required \"instance_all_projects\" API extension")
	}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// UpdateInstances updates all instances to match the requested state.
func (r *ProtocolIncus) UpdateInstances(state api.InstancesPut, ETag string) (Operation, error) {
	path, v, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("%s?%s", path, v.Encode()), state, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// rebuildInstance initiates a rebuild of a given instance on the Incus Protocol server and returns the corresponding operation or an error.
func (r *ProtocolIncus) rebuildInstance(instanceName string, instance api.InstanceRebuildPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/rebuild", path, url.PathEscape(instanceName)), instance, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// tryRebuildInstance attempts to rebuild a specific instance on multiple target servers identified by their URLs.
// It runs the rebuild process asynchronously and returns a RemoteOperation to monitor the progress and any errors.
func (r *ProtocolIncus) tryRebuildInstance(instanceName string, req api.InstanceRebuildPost, urls []string, op Operation) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("The source server isn't listening on the network")
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
			if operation == "" {
				req.Source.Server = serverURL
			} else {
				req.Source.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, url.PathEscape(operation))
			}

			op, err := r.rebuildInstance(instanceName, req)
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})
				continue
			}

			rop.handlerLock.Lock()
			rop.targetOp = op
			rop.handlerLock.Unlock()

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
			rop.err = remoteOperationError("Failed instance rebuild", errors)
			if op != nil {
				_ = op.Cancel()
			}
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// RebuildInstanceFromImage rebuilds an instance from an image.
func (r *ProtocolIncus) RebuildInstanceFromImage(source ImageServer, image api.Image, instanceName string, req api.InstanceRebuildPost) (RemoteOperation, error) {
	err := r.CheckExtension("instances_rebuild")
	if err != nil {
		return nil, err
	}

	info, err := r.getSourceImageConnectionInfo(source, image, &req.Source)
	if err != nil {
		return nil, err
	}

	if info == nil {
		op, err := r.rebuildInstance(instanceName, req)
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

	return r.tryRebuildInstance(instanceName, req, info.Addresses, nil)
}

// RebuildInstance rebuilds an instance as empty.
func (r *ProtocolIncus) RebuildInstance(instanceName string, instance api.InstanceRebuildPost) (op Operation, err error) {
	err = r.CheckExtension("instances_rebuild")
	if err != nil {
		return nil, err
	}

	return r.rebuildInstance(instanceName, instance)
}

// GetInstancesFull returns a list of instances including snapshots, backups and state.
func (r *ProtocolIncus) GetInstancesFull(instanceType api.InstanceType) ([]api.InstanceFull, error) {
	instances := []api.InstanceFull{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	v.Set("recursion", "2")

	if !r.HasExtension("container_full") {
		return nil, fmt.Errorf("The server is missing the required \"container_full\" API extension")
	}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// GetInstancesFullWithFilter returns a filtered list of instances including snapshots, backups and state.
func (r *ProtocolIncus) GetInstancesFullWithFilter(instanceType api.InstanceType, filters []string) ([]api.InstanceFull, error) {
	if !r.HasExtension("api_filtering") {
		return nil, fmt.Errorf("The server is missing the required \"api_filtering\" API extension")
	}

	instances := []api.InstanceFull{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	v.Set("recursion", "2")
	v.Set("filter", parseFilters(filters))

	if !r.HasExtension("container_full") {
		return nil, fmt.Errorf("The server is missing the required \"container_full\" API extension")
	}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// GetInstancesFullAllProjects returns a list of instances including snapshots, backups and state from all projects.
func (r *ProtocolIncus) GetInstancesFullAllProjects(instanceType api.InstanceType) ([]api.InstanceFull, error) {
	instances := []api.InstanceFull{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	v.Set("recursion", "2")
	v.Set("all-projects", "true")

	if !r.HasExtension("container_full") {
		return nil, fmt.Errorf("The server is missing the required \"container_full\" API extension")
	}

	if !r.HasExtension("instance_all_projects") {
		return nil, fmt.Errorf("The server is missing the required \"instance_all_projects\" API extension")
	}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// GetInstancesFullAllProjectsWithFilter returns a filtered list of instances including snapshots, backups and state from all projects.
func (r *ProtocolIncus) GetInstancesFullAllProjectsWithFilter(instanceType api.InstanceType, filters []string) ([]api.InstanceFull, error) {
	if !r.HasExtension("api_filtering") {
		return nil, fmt.Errorf("The server is missing the required \"api_filtering\" API extension")
	}

	instances := []api.InstanceFull{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	v.Set("recursion", "2")
	v.Set("all-projects", "true")
	v.Set("filter", parseFilters(filters))

	if !r.HasExtension("container_full") {
		return nil, fmt.Errorf("The server is missing the required \"container_full\" API extension")
	}

	if !r.HasExtension("instance_all_projects") {
		return nil, fmt.Errorf("The server is missing the required \"instance_all_projects\" API extension")
	}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// GetInstance returns the instance entry for the provided name.
func (r *ProtocolIncus) GetInstance(name string) (*api.Instance, string, error) {
	instance := api.Instance{}

	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, "", err
	}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("%s/%s", path, url.PathEscape(name)), nil, "", &instance)
	if err != nil {
		return nil, "", err
	}

	return &instance, etag, nil
}

// GetInstanceFull returns the instance entry for the provided name along with snapshot information.
func (r *ProtocolIncus) GetInstanceFull(name string) (*api.InstanceFull, string, error) {
	instance := api.InstanceFull{}

	if !r.HasExtension("instance_get_full") {
		// Backware compatibility.
		ct, _, err := r.GetInstance(name)
		if err != nil {
			return nil, "", err
		}

		cs, _, err := r.GetInstanceState(name)
		if err != nil {
			return nil, "", err
		}

		snaps, err := r.GetInstanceSnapshots(name)
		if err != nil {
			return nil, "", err
		}

		backups, err := r.GetInstanceBackups(name)
		if err != nil {
			return nil, "", err
		}

		instance.Instance = *ct
		instance.State = cs
		instance.Snapshots = snaps
		instance.Backups = backups

		return &instance, "", nil
	}

	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, "", err
	}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("%s/%s?recursion=1", path, url.PathEscape(name)), nil, "", &instance)
	if err != nil {
		return nil, "", err
	}

	return &instance, etag, nil
}

// CreateInstanceFromBackup is a convenience function to make it easier to
// create a instance from a backup.
func (r *ProtocolIncus) CreateInstanceFromBackup(args InstanceBackupArgs) (Operation, error) {
	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if args.PoolName == "" && args.Name == "" {
		// Send the request
		op, _, err := r.queryOperation("POST", path, args.BackupFile, "")
		if err != nil {
			return nil, err
		}

		return op, nil
	}

	if args.PoolName != "" && !r.HasExtension("container_backup_override_pool") {
		return nil, fmt.Errorf(`The server is missing the required "container_backup_override_pool" API extension`)
	}

	if args.Name != "" && !r.HasExtension("backup_override_name") {
		return nil, fmt.Errorf(`The server is missing the required "backup_override_name" API extension`)
	}

	// Prepare the HTTP request
	reqURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0%s", r.httpBaseURL.String(), path))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", reqURL, args.BackupFile)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	if args.PoolName != "" {
		req.Header.Set("X-Incus-pool", args.PoolName)
	}

	if args.Name != "" {
		req.Header.Set("X-Incus-name", args.Name)
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	// Handle errors
	response, _, err := incusParseResponse(resp)
	if err != nil {
		return nil, err
	}

	// Get to the operation
	respOperation, err := response.MetadataAsOperation()
	if err != nil {
		return nil, err
	}

	// Setup an Operation wrapper
	op := operation{
		Operation: *respOperation,
		r:         r,
		chActive:  make(chan bool),
	}

	return &op, nil
}

// CreateInstance requests that Incus creates a new instance.
func (r *ProtocolIncus) CreateInstance(instance api.InstancesPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(instance.Type)
	if err != nil {
		return nil, err
	}

	if instance.Source.InstanceOnly {
		if !r.HasExtension("container_only_migration") {
			return nil, fmt.Errorf("The server is missing the required \"container_only_migration\" API extension")
		}
	}

	// Send the request
	op, _, err := r.queryOperation("POST", path, instance, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// tryCreateInstance attempts to create a new instance on multiple target servers specified by their URLs.
// It runs the instance creation asynchronously and returns a RemoteOperation to monitor the progress and any errors.
func (r *ProtocolIncus) tryCreateInstance(req api.InstancesPost, urls []string, op Operation) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("The source server isn't listening on the network")
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
			if operation == "" {
				req.Source.Server = serverURL
			} else {
				req.Source.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, url.PathEscape(operation))
			}

			op, err := r.CreateInstance(req)
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})
				continue
			}

			rop.handlerLock.Lock()
			rop.targetOp = op
			rop.handlerLock.Unlock()

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
			rop.err = remoteOperationError("Failed instance creation", errors)
			if op != nil {
				_ = op.Cancel()
			}
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// CreateInstanceFromImage is a convenience function to make it easier to create a instance from an existing image.
func (r *ProtocolIncus) CreateInstanceFromImage(source ImageServer, image api.Image, req api.InstancesPost) (RemoteOperation, error) {
	info, err := r.getSourceImageConnectionInfo(source, image, &req.Source)
	if err != nil {
		return nil, err
	}

	// If the source server is the same as the target server, create the instance directly.
	if info == nil {
		op, err := r.CreateInstance(req)
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

	return r.tryCreateInstance(req, info.Addresses, nil)
}

// CopyInstance copies a instance from a remote server. Additional options can be passed using InstanceCopyArgs.
func (r *ProtocolIncus) CopyInstance(source InstanceServer, instance api.Instance, args *InstanceCopyArgs) (RemoteOperation, error) {
	// Base request
	req := api.InstancesPost{
		Name:        instance.Name,
		InstancePut: instance.Writable(),
		Type:        api.InstanceType(instance.Type),
	}

	req.Source.BaseImage = instance.Config["volatile.base_image"]

	// Process the copy arguments
	if args != nil {
		// Quick checks.
		if args.InstanceOnly {
			if !r.HasExtension("container_only_migration") {
				return nil, fmt.Errorf("The target server is missing the required \"container_only_migration\" API extension")
			}

			if !source.HasExtension("container_only_migration") {
				return nil, fmt.Errorf("The source server is missing the required \"container_only_migration\" API extension")
			}
		}

		if slices.Contains([]string{"push", "relay"}, args.Mode) {
			if !r.HasExtension("container_push") {
				return nil, fmt.Errorf("The target server is missing the required \"container_push\" API extension")
			}

			if !source.HasExtension("container_push") {
				return nil, fmt.Errorf("The source server is missing the required \"container_push\" API extension")
			}
		}

		if args.Mode == "push" && !source.HasExtension("container_push_target") {
			return nil, fmt.Errorf("The source server is missing the required \"container_push_target\" API extension")
		}

		if args.Refresh {
			if !r.HasExtension("container_incremental_copy") {
				return nil, fmt.Errorf("The target server is missing the required \"container_incremental_copy\" API extension")
			}

			if !source.HasExtension("container_incremental_copy") {
				return nil, fmt.Errorf("The source server is missing the required \"container_incremental_copy\" API extension")
			}
		}

		if args.AllowInconsistent {
			if !r.HasExtension("instance_allow_inconsistent_copy") {
				return nil, fmt.Errorf("The source server is missing the required \"instance_allow_inconsistent_copy\" API extension")
			}
		}

		// Allow overriding the target name
		if args.Name != "" {
			req.Name = args.Name
		}

		req.Source.Live = args.Live
		req.Source.InstanceOnly = args.InstanceOnly
		req.Source.Refresh = args.Refresh
		req.Source.AllowInconsistent = args.AllowInconsistent
	}

	if req.Source.Live {
		req.Source.Live = instance.StatusCode == api.Running
	}

	sourceInfo, err := source.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get source connection info: %w", err)
	}

	destInfo, err := r.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get destination connection info: %w", err)
	}

	// Optimization for the local copy case
	if destInfo.URL == sourceInfo.URL && destInfo.SocketPath == sourceInfo.SocketPath && (!r.IsClustered() || instance.Location == r.clusterTarget || r.HasExtension("cluster_internal_copy")) {
		// Project handling
		if destInfo.Project != sourceInfo.Project {
			if !r.HasExtension("container_copy_project") {
				return nil, fmt.Errorf("The server is missing the required \"container_copy_project\" API extension")
			}

			req.Source.Project = sourceInfo.Project
		}

		// Local copy source fields
		req.Source.Type = "copy"
		req.Source.Source = instance.Name

		// Copy the instance
		op, err := r.CreateInstance(req)
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

	// Source request
	sourceReq := api.InstancePost{
		Migration:         true,
		Live:              req.Source.Live,
		InstanceOnly:      req.Source.InstanceOnly,
		AllowInconsistent: req.Source.AllowInconsistent,
	}

	// Push mode migration
	if args != nil && args.Mode == "push" {
		// Get target server connection information
		info, err := r.GetConnectionInfo()
		if err != nil {
			return nil, err
		}

		// Create the instance
		req.Source.Type = "migration"
		req.Source.Mode = "push"
		req.Source.Refresh = args.Refresh

		op, err := r.CreateInstance(req)
		if err != nil {
			return nil, err
		}

		opAPI := op.Get()

		targetSecrets := map[string]string{}
		for k, v := range opAPI.Metadata {
			targetSecrets[k] = v.(string)
		}

		// Prepare the source request
		target := api.InstancePostTarget{}
		target.Operation = opAPI.ID
		target.Websockets = targetSecrets
		target.Certificate = info.Certificate
		sourceReq.Target = &target

		return r.tryMigrateInstance(source, instance.Name, sourceReq, info.Addresses)
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	op, err := source.MigrateInstance(instance.Name, sourceReq)
	if err != nil {
		return nil, err
	}

	opAPI := op.Get()

	sourceSecrets := map[string]string{}
	for k, v := range opAPI.Metadata {
		sourceSecrets[k] = v.(string)
	}

	// Relay mode migration
	if args != nil && args.Mode == "relay" {
		// Push copy source fields
		req.Source.Type = "migration"
		req.Source.Mode = "push"

		// Start the process
		targetOp, err := r.CreateInstance(req)
		if err != nil {
			return nil, err
		}

		targetOpAPI := targetOp.Get()

		// Extract the websockets
		targetSecrets := map[string]string{}
		for k, v := range targetOpAPI.Metadata {
			targetSecrets[k] = v.(string)
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

	return r.tryCreateInstance(req, info.Addresses, op)
}

// UpdateInstance updates the instance definition.
func (r *ProtocolIncus) UpdateInstance(name string, instance api.InstancePut, ETag string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("%s/%s", path, url.PathEscape(name)), instance, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// RenameInstance requests that Incus renames the instance.
func (r *ProtocolIncus) RenameInstance(name string, instance api.InstancePost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Quick check.
	if instance.Migration {
		return nil, fmt.Errorf("Can't ask for a migration through RenameInstance")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s", path, url.PathEscape(name)), instance, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// tryMigrateInstance attempts to migrate a specific instance from a source server to one of the target URLs.
// The function runs the migration operation asynchronously and returns a RemoteOperation to track the progress and handle any errors.
func (r *ProtocolIncus) tryMigrateInstance(source InstanceServer, name string, req api.InstancePost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("The target server isn't listening on the network")
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

			op, err := source.MigrateInstance(name, req)
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})
				continue
			}

			rop.targetOp = op

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
			rop.err = remoteOperationError("Failed instance migration", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// MigrateInstance requests that Incus prepares for a instance migration.
func (r *ProtocolIncus) MigrateInstance(name string, instance api.InstancePost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if instance.InstanceOnly {
		if !r.HasExtension("container_only_migration") {
			return nil, fmt.Errorf("The server is missing the required \"container_only_migration\" API extension")
		}
	}

	if instance.Pool != "" && !r.HasExtension("instance_pool_move") {
		return nil, fmt.Errorf("The server is missing the required \"instance_pool_move\" API extension")
	}

	if instance.Project != "" && !r.HasExtension("instance_project_move") {
		return nil, fmt.Errorf("The server is missing the required \"instance_project_move\" API extension")
	}

	if instance.AllowInconsistent && !r.HasExtension("cluster_migration_inconsistent_copy") {
		return nil, fmt.Errorf("The server is missing the required \"cluster_migration_inconsistent_copy\" API extension")
	}

	// Quick check.
	if !instance.Migration {
		return nil, fmt.Errorf("Can't ask for a rename through MigrateInstance")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s", path, url.PathEscape(name)), instance, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteInstance requests that Incus deletes the instance.
func (r *ProtocolIncus) DeleteInstance(name string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("%s/%s", path, url.PathEscape(name)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// ExecInstance requests that Incus spawns a command inside the instance.
func (r *ProtocolIncus) ExecInstance(instanceName string, exec api.InstanceExecPost, args *InstanceExecArgs) (Operation, error) {
	// Ensure args are equivalent to empty InstanceExecArgs.
	if args == nil {
		args = &InstanceExecArgs{}
	}

	if exec.RecordOutput {
		if !r.HasExtension("container_exec_recording") {
			return nil, fmt.Errorf("The server is missing the required \"container_exec_recording\" API extension")
		}
	}

	if exec.User > 0 || exec.Group > 0 || exec.Cwd != "" {
		if !r.HasExtension("container_exec_user_group_cwd") {
			return nil, fmt.Errorf("The server is missing the required \"container_exec_user_group_cwd\" API extension")
		}
	}

	var uri string

	if r.IsAgent() {
		uri = "/exec"
	} else {
		path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
		if err != nil {
			return nil, err
		}

		uri = fmt.Sprintf("%s/%s/exec", path, url.PathEscape(instanceName))
	}

	// Send the request
	op, _, err := r.queryOperation("POST", uri, exec, "")
	if err != nil {
		return nil, err
	}

	opAPI := op.Get()

	// Process additional arguments

	// Parse the fds
	fds := map[string]string{}

	value, ok := opAPI.Metadata["fds"]
	if ok {
		values := value.(map[string]any)
		for k, v := range values {
			fds[k] = v.(string)
		}
	}

	if exec.RecordOutput && (args.Stdout != nil || args.Stderr != nil) {
		err = op.Wait()
		if err != nil {
			return nil, err
		}

		opAPI = op.Get()
		outputFiles := map[string]string{}
		outputs, ok := opAPI.Metadata["output"].(map[string]any)
		if ok {
			for k, v := range outputs {
				outputFiles[k] = v.(string)
			}
		}

		if outputFiles["1"] != "" {
			reader, _ := r.getInstanceExecOutputLogFile(instanceName, filepath.Base(outputFiles["1"]))
			if args.Stdout != nil {
				_, errCopy := io.Copy(args.Stdout, reader)
				// Regardless of errCopy value, we want to delete the file after a copy operation
				errDelete := r.deleteInstanceExecOutputLogFile(instanceName, filepath.Base(outputFiles["1"]))
				if errDelete != nil {
					return nil, errDelete
				}

				if errCopy != nil {
					return nil, fmt.Errorf("Could not copy the content of the exec output log file to stdout: %w", err)
				}
			}

			err = r.deleteInstanceExecOutputLogFile(instanceName, filepath.Base(outputFiles["1"]))
			if err != nil {
				return nil, err
			}
		}

		if outputFiles["2"] != "" {
			reader, _ := r.getInstanceExecOutputLogFile(instanceName, filepath.Base(outputFiles["2"]))
			if args.Stderr != nil {
				_, errCopy := io.Copy(args.Stderr, reader)
				errDelete := r.deleteInstanceExecOutputLogFile(instanceName, filepath.Base(outputFiles["1"]))
				if errDelete != nil {
					return nil, errDelete
				}

				if errCopy != nil {
					return nil, fmt.Errorf("Could not copy the content of the exec output log file to stderr: %w", err)
				}
			}

			err = r.deleteInstanceExecOutputLogFile(instanceName, filepath.Base(outputFiles["2"]))
			if err != nil {
				return nil, err
			}
		}
	}

	if fds[api.SecretNameControl] != "" {
		conn, err := r.GetOperationWebsocket(opAPI.ID, fds[api.SecretNameControl])
		if err != nil {
			return nil, err
		}

		go func() {
			_, _, _ = conn.ReadMessage() // Consume pings from server.
		}()

		if args.Control != nil {
			// Call the control handler with a connection to the control socket
			go args.Control(conn)
		}
	}

	if exec.Interactive {
		// Handle interactive sections
		if args.Stdin != nil && args.Stdout != nil {
			// Connect to the websocket
			conn, err := r.GetOperationWebsocket(opAPI.ID, fds["0"])
			if err != nil {
				return nil, err
			}

			// And attach stdin and stdout to it
			go func() {
				ws.MirrorRead(conn, args.Stdin)
				<-ws.MirrorWrite(conn, args.Stdout)
				_ = conn.Close()

				if args.DataDone != nil {
					close(args.DataDone)
				}
			}()
		} else {
			if args.DataDone != nil {
				close(args.DataDone)
			}
		}
	} else {
		// Handle non-interactive sessions
		dones := make(map[int]chan error)
		conns := []*websocket.Conn{}

		// Handle stdin
		if fds["0"] != "" {
			conn, err := r.GetOperationWebsocket(opAPI.ID, fds["0"])
			if err != nil {
				return nil, err
			}

			go func() {
				_, _, _ = conn.ReadMessage() // Consume pings from server.
			}()

			conns = append(conns, conn)
			dones[0] = ws.MirrorRead(conn, args.Stdin)
		}

		waitConns := 0 // Used for keeping track of when stdout and stderr have finished.

		// Handle stdout
		if fds["1"] != "" {
			conn, err := r.GetOperationWebsocket(opAPI.ID, fds["1"])
			if err != nil {
				return nil, err
			}

			// Discard Stdout from remote command if output writer not supplied.
			if args.Stdout == nil {
				args.Stdout = io.Discard
			}

			conns = append(conns, conn)
			dones[1] = ws.MirrorWrite(conn, args.Stdout)
			waitConns++
		}

		// Handle stderr
		if fds["2"] != "" {
			conn, err := r.GetOperationWebsocket(opAPI.ID, fds["2"])
			if err != nil {
				return nil, err
			}

			// Discard Stderr from remote command if output writer not supplied.
			if args.Stderr == nil {
				args.Stderr = io.Discard
			}

			conns = append(conns, conn)
			dones[2] = ws.MirrorWrite(conn, args.Stderr)
			waitConns++
		}

		// Wait for everything to be done
		go func() {
			for {
				select {
				case <-dones[0]:
					// Handle stdin finish, but don't wait for it if output channels
					// have all finished.
					dones[0] = nil
					_ = conns[0].Close()
				case <-dones[1]:
					dones[1] = nil
					_ = conns[1].Close()
					waitConns--
				case <-dones[2]:
					dones[2] = nil
					_ = conns[2].Close()
					waitConns--
				}

				if waitConns <= 0 {
					// Close stdin websocket if defined and not already closed.
					if dones[0] != nil {
						conns[0].Close()
					}

					break
				}
			}

			if args.DataDone != nil {
				close(args.DataDone)
			}
		}()
	}

	return op, nil
}

// GetInstanceFile retrieves the provided path from the instance.
func (r *ProtocolIncus) GetInstanceFile(instanceName string, filePath string) (io.ReadCloser, *InstanceFileResponse, error) {
	var err error
	var requestURL string

	urlEncode := func(path string, query map[string]string) (string, error) {
		u, err := url.Parse(path)
		if err != nil {
			return "", err
		}

		params := url.Values{}
		for key, value := range query {
			params.Add(key, value)
		}

		u.RawQuery = params.Encode()
		return u.String(), nil
	}

	if r.IsAgent() {
		requestURL, err = urlEncode(
			fmt.Sprintf("%s/1.0/files", r.httpBaseURL.String()),
			map[string]string{"path": filePath})
	} else {
		var path string

		path, _, err = r.instanceTypeToPath(api.InstanceTypeAny)
		if err != nil {
			return nil, nil, err
		}

		// Prepare the HTTP request
		requestURL, err = urlEncode(
			fmt.Sprintf("%s/1.0%s/%s/files", r.httpBaseURL.String(), path, url.PathEscape(instanceName)),
			map[string]string{"path": filePath})
	}

	if err != nil {
		return nil, nil, err
	}

	requestURL, err = r.setQueryAttributes(requestURL)
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

// CreateInstanceFile tells Incus to create a file in the instance.
func (r *ProtocolIncus) CreateInstanceFile(instanceName string, filePath string, args InstanceFileArgs) error {
	if args.Type == "directory" {
		if !r.HasExtension("directory_manipulation") {
			return fmt.Errorf("The server is missing the required \"directory_manipulation\" API extension")
		}
	}

	if args.Type == "symlink" {
		if !r.HasExtension("file_symlinks") {
			return fmt.Errorf("The server is missing the required \"file_symlinks\" API extension")
		}
	}

	if args.WriteMode == "append" {
		if !r.HasExtension("file_append") {
			return fmt.Errorf("The server is missing the required \"file_append\" API extension")
		}
	}

	var requestURL string

	if r.IsAgent() {
		requestURL = fmt.Sprintf("%s/1.0/files?path=%s", r.httpBaseURL.String(), url.QueryEscape(filePath))
	} else {
		path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
		if err != nil {
			return err
		}

		// Prepare the HTTP request
		requestURL = fmt.Sprintf("%s/1.0%s/%s/files?path=%s", r.httpBaseURL.String(), path, url.PathEscape(instanceName), url.QueryEscape(filePath))
	}

	requestURL, err := r.setQueryAttributes(requestURL)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", requestURL, args.Content)
	if err != nil {
		return err
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

// DeleteInstanceFile deletes a file in the instance.
func (r *ProtocolIncus) DeleteInstanceFile(instanceName string, filePath string) error {
	if !r.HasExtension("file_delete") {
		return fmt.Errorf("The server is missing the required \"file_delete\" API extension")
	}

	var requestURL string

	if r.IsAgent() {
		requestURL = fmt.Sprintf("/files?path=%s", url.QueryEscape(filePath))
	} else {
		path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
		if err != nil {
			return err
		}

		// Prepare the HTTP request
		requestURL = fmt.Sprintf("%s/%s/files?path=%s", path, url.PathEscape(instanceName), url.QueryEscape(filePath))
	}

	requestURL, err := r.setQueryAttributes(requestURL)
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

// rawSFTPConn connects to the apiURL, upgrades to an SFTP raw connection and returns it.
func (r *ProtocolIncus) rawSFTPConn(apiURL *url.URL) (net.Conn, error) {
	// Get the HTTP transport.
	httpTransport, err := r.getUnderlyingHTTPTransport()
	if err != nil {
		return nil, err
	}

	req := &http.Request{
		Method:     http.MethodGet,
		URL:        apiURL,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       apiURL.Host,
	}

	req.Header["Upgrade"] = []string{"sftp"}
	req.Header["Connection"] = []string{"Upgrade"}

	r.addClientHeaders(req)

	// Establish the connection.
	var conn net.Conn

	if httpTransport.TLSClientConfig != nil {
		conn, err = httpTransport.DialTLSContext(context.Background(), "tcp", apiURL.Host)
	} else {
		conn, err = httpTransport.DialContext(context.Background(), "tcp", apiURL.Host)
	}

	if err != nil {
		return nil, err
	}

	remoteTCP, _ := tcp.ExtractConn(conn)
	if remoteTCP != nil {
		err = tcp.SetTimeouts(remoteTCP, 0)
		if err != nil {
			return nil, err
		}
	}

	err = req.Write(conn)
	if err != nil {
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		_, _, err := incusParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	if resp.Header.Get("Upgrade") != "sftp" {
		return nil, fmt.Errorf("Missing or unexpected Upgrade header in response")
	}

	return conn, err
}

// GetInstanceFileSFTPConn returns a connection to the instance's SFTP endpoint.
func (r *ProtocolIncus) GetInstanceFileSFTPConn(instanceName string) (net.Conn, error) {
	apiURL := api.NewURL()
	apiURL.URL = r.httpBaseURL // Preload the URL with the client base URL.
	apiURL.Path("1.0", "instances", instanceName, "sftp")
	r.setURLQueryAttributes(&apiURL.URL)

	return r.rawSFTPConn(&apiURL.URL)
}

// GetInstanceFileSFTP returns an SFTP connection to the instance.
func (r *ProtocolIncus) GetInstanceFileSFTP(instanceName string) (*sftp.Client, error) {
	conn, err := r.GetInstanceFileSFTPConn(instanceName)
	if err != nil {
		return nil, err
	}

	// Get a SFTP client.
	client, err := sftp.NewClientPipe(conn, conn)
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

// GetInstanceSnapshotNames returns a list of snapshot names for the instance.
func (r *ProtocolIncus) GetInstanceSnapshotNames(instanceName string) ([]string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("%s/%s/snapshots", path, url.PathEscape(instanceName))
	_, err = r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetInstanceSnapshots returns a list of snapshots for the instance.
func (r *ProtocolIncus) GetInstanceSnapshots(instanceName string) ([]api.InstanceSnapshot, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	snapshots := []api.InstanceSnapshot{}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s/%s/snapshots?recursion=1", path, url.PathEscape(instanceName)), nil, "", &snapshots)
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}

// GetInstanceSnapshot returns a Snapshot struct for the provided instance and snapshot names.
func (r *ProtocolIncus) GetInstanceSnapshot(instanceName string, name string) (*api.InstanceSnapshot, string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, "", err
	}

	snapshot := api.InstanceSnapshot{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("%s/%s/snapshots/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), nil, "", &snapshot)
	if err != nil {
		return nil, "", err
	}

	return &snapshot, etag, nil
}

// CreateInstanceSnapshot requests that Incus creates a new snapshot for the instance.
func (r *ProtocolIncus) CreateInstanceSnapshot(instanceName string, snapshot api.InstanceSnapshotsPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Validate the request
	if snapshot.ExpiresAt != nil && !r.HasExtension("snapshot_expiry_creation") {
		return nil, fmt.Errorf("The server is missing the required \"snapshot_expiry_creation\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/snapshots", path, url.PathEscape(instanceName)), snapshot, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// CopyInstanceSnapshot copies a snapshot from a remote server into a new instance. Additional options can be passed using InstanceCopyArgs.
func (r *ProtocolIncus) CopyInstanceSnapshot(source InstanceServer, instanceName string, snapshot api.InstanceSnapshot, args *InstanceSnapshotCopyArgs) (RemoteOperation, error) {
	// Backward compatibility (with broken Name field)
	fields := strings.Split(snapshot.Name, "/")
	cName := instanceName
	sName := fields[len(fields)-1]

	// Base request
	req := api.InstancesPost{
		Name: cName,
		InstancePut: api.InstancePut{
			Architecture: snapshot.Architecture,
			Config:       snapshot.Config,
			Devices:      snapshot.Devices,
			Ephemeral:    snapshot.Ephemeral,
			Profiles:     snapshot.Profiles,
		},
	}

	if snapshot.Stateful && args.Live {
		if !r.HasExtension("container_snapshot_stateful_migration") {
			return nil, fmt.Errorf("The server is missing the required \"container_snapshot_stateful_migration\" API extension")
		}

		req.InstancePut.Stateful = snapshot.Stateful
		req.Source.Live = false // Snapshots are never running and so we don't need live migration.
	}

	req.Source.BaseImage = snapshot.Config["volatile.base_image"]

	// Process the copy arguments
	if args != nil {
		// Quick checks.
		if slices.Contains([]string{"push", "relay"}, args.Mode) {
			if !r.HasExtension("container_push") {
				return nil, fmt.Errorf("The target server is missing the required \"container_push\" API extension")
			}

			if !source.HasExtension("container_push") {
				return nil, fmt.Errorf("The source server is missing the required \"container_push\" API extension")
			}
		}

		if args.Mode == "push" && !source.HasExtension("container_push_target") {
			return nil, fmt.Errorf("The source server is missing the required \"container_push_target\" API extension")
		}

		// Allow overriding the target name
		if args.Name != "" {
			req.Name = args.Name
		}
	}

	sourceInfo, err := source.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get source connection info: %w", err)
	}

	destInfo, err := r.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get destination connection info: %w", err)
	}

	instance, _, err := source.GetInstance(cName)
	if err != nil {
		return nil, fmt.Errorf("Failed to get instance info: %w", err)
	}

	// Optimization for the local copy case
	if destInfo.URL == sourceInfo.URL && destInfo.SocketPath == sourceInfo.SocketPath && (!r.IsClustered() || instance.Location == r.clusterTarget || r.HasExtension("cluster_internal_copy")) {
		// Project handling
		if destInfo.Project != sourceInfo.Project {
			if !r.HasExtension("container_copy_project") {
				return nil, fmt.Errorf("The server is missing the required \"container_copy_project\" API extension")
			}

			req.Source.Project = sourceInfo.Project
		}

		// Local copy source fields
		req.Source.Type = "copy"
		req.Source.Source = fmt.Sprintf("%s/%s", cName, sName)

		// Copy the instance
		op, err := r.CreateInstance(req)
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

	// If deadling with migration, we need to set the type.
	if source.HasExtension("virtual-machines") {
		inst, _, err := source.GetInstance(instanceName)
		if err != nil {
			return nil, err
		}

		req.Type = api.InstanceType(inst.Type)
	}

	// Source request
	sourceReq := api.InstanceSnapshotPost{
		Migration: true,
		Name:      args.Name,
	}

	if snapshot.Stateful && args.Live {
		sourceReq.Live = args.Live
	}

	// Push mode migration
	if args != nil && args.Mode == "push" {
		// Get target server connection information
		info, err := r.GetConnectionInfo()
		if err != nil {
			return nil, err
		}

		// Create the instance
		req.Source.Type = "migration"
		req.Source.Mode = "push"

		op, err := r.CreateInstance(req)
		if err != nil {
			return nil, err
		}

		opAPI := op.Get()

		targetSecrets := map[string]string{}
		for k, v := range opAPI.Metadata {
			targetSecrets[k] = v.(string)
		}

		// Prepare the source request
		target := api.InstancePostTarget{}
		target.Operation = opAPI.ID
		target.Websockets = targetSecrets
		target.Certificate = info.Certificate
		sourceReq.Target = &target

		return r.tryMigrateInstanceSnapshot(source, cName, sName, sourceReq, info.Addresses)
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	op, err := source.MigrateInstanceSnapshot(cName, sName, sourceReq)
	if err != nil {
		return nil, err
	}

	opAPI := op.Get()

	sourceSecrets := map[string]string{}
	for k, v := range opAPI.Metadata {
		sourceSecrets[k] = v.(string)
	}

	// Relay mode migration
	if args != nil && args.Mode == "relay" {
		// Push copy source fields
		req.Source.Type = "migration"
		req.Source.Mode = "push"

		// Start the process
		targetOp, err := r.CreateInstance(req)
		if err != nil {
			return nil, err
		}

		targetOpAPI := targetOp.Get()

		// Extract the websockets
		targetSecrets := map[string]string{}
		for k, v := range targetOpAPI.Metadata {
			targetSecrets[k] = v.(string)
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

	return r.tryCreateInstance(req, info.Addresses, op)
}

// RenameInstanceSnapshot requests that Incus renames the snapshot.
func (r *ProtocolIncus) RenameInstanceSnapshot(instanceName string, name string, instance api.InstanceSnapshotPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Quick check.
	if instance.Migration {
		return nil, fmt.Errorf("Can't ask for a migration through RenameInstanceSnapshot")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/snapshots/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), instance, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

func (r *ProtocolIncus) tryMigrateInstanceSnapshot(source InstanceServer, instanceName string, name string, req api.InstanceSnapshotPost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("The target server isn't listening on the network")
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

			op, err := source.MigrateInstanceSnapshot(instanceName, name, req)
			if err != nil {
				errors = append(errors, remoteOperationResult{URL: serverURL, Error: err})
				continue
			}

			rop.targetOp = op

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
			rop.err = remoteOperationError("Failed instance migration", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// MigrateInstanceSnapshot requests that Incus prepares for a snapshot migration.
func (r *ProtocolIncus) MigrateInstanceSnapshot(instanceName string, name string, instance api.InstanceSnapshotPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Quick check.
	if !instance.Migration {
		return nil, fmt.Errorf("Can't ask for a rename through MigrateInstanceSnapshot")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/snapshots/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), instance, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteInstanceSnapshot requests that Incus deletes the instance snapshot.
func (r *ProtocolIncus) DeleteInstanceSnapshot(instanceName string, name string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("%s/%s/snapshots/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateInstanceSnapshot requests that Incus updates the instance snapshot.
func (r *ProtocolIncus) UpdateInstanceSnapshot(instanceName string, name string, instance api.InstanceSnapshotPut, ETag string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("snapshot_expiry") {
		return nil, fmt.Errorf("The server is missing the required \"snapshot_expiry\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("%s/%s/snapshots/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), instance, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetInstanceState returns a InstanceState entry for the provided instance name.
func (r *ProtocolIncus) GetInstanceState(name string) (*api.InstanceState, string, error) {
	var uri string

	if r.IsAgent() {
		uri = "/state"
	} else {
		path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
		if err != nil {
			return nil, "", err
		}

		uri = fmt.Sprintf("%s/%s/state", path, url.PathEscape(name))
	}

	state := api.InstanceState{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", uri, nil, "", &state)
	if err != nil {
		return nil, "", err
	}

	return &state, etag, nil
}

// UpdateInstanceState updates the instance to match the requested state.
func (r *ProtocolIncus) UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("%s/%s/state", path, url.PathEscape(name)), state, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetInstanceAccess returns an Access entry for the provided instance name.
func (r *ProtocolIncus) GetInstanceAccess(name string) (api.Access, error) {
	access := api.Access{}

	if !r.HasExtension("instance_access") {
		return nil, fmt.Errorf("The server is missing the required \"instance_access\" API extension")
	}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/instances/%s/access", url.PathEscape(name)), nil, "", &access)
	if err != nil {
		return nil, err
	}

	return access, nil
}

// GetInstanceLogfiles returns a list of logfiles for the instance.
func (r *ProtocolIncus) GetInstanceLogfiles(name string) ([]string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("%s/%s/logs", path, url.PathEscape(name))
	_, err = r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetInstanceLogfile returns the content of the requested logfile.
//
// Note that it's the caller's responsibility to close the returned ReadCloser.
func (r *ProtocolIncus) GetInstanceLogfile(name string, filename string) (io.ReadCloser, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Prepare the HTTP request
	url := fmt.Sprintf("%s/1.0%s/%s/logs/%s", r.httpBaseURL.String(), path, url.PathEscape(name), url.PathEscape(filename))

	url, err = r.setQueryAttributes(url)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := incusParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	return resp.Body, err
}

// DeleteInstanceLogfile deletes the requested logfile.
func (r *ProtocolIncus) DeleteInstanceLogfile(name string, filename string) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	// Send the request
	_, _, err = r.query("DELETE", fmt.Sprintf("%s/%s/logs/%s", path, url.PathEscape(name), url.PathEscape(filename)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// getInstanceExecOutputLogFile returns the content of the requested exec logfile.
//
// Note that it's the caller's responsibility to close the returned ReadCloser.
func (r *ProtocolIncus) getInstanceExecOutputLogFile(name string, filename string) (io.ReadCloser, error) {
	err := r.CheckExtension("container_exec_recording")
	if err != nil {
		return nil, err
	}

	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Prepare the HTTP request
	url := fmt.Sprintf("%s/1.0%s/%s/logs/exec-output/%s", r.httpBaseURL.String(), path, url.PathEscape(name), url.PathEscape(filename))

	url, err = r.setQueryAttributes(url)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := incusParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	return resp.Body, nil
}

// deleteInstanceExecOutputLogFiles deletes the requested exec logfile.
func (r *ProtocolIncus) deleteInstanceExecOutputLogFile(instanceName string, filename string) error {
	err := r.CheckExtension("container_exec_recording")
	if err != nil {
		return err
	}

	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	// Send the request
	_, _, err = r.query("DELETE", fmt.Sprintf("%s/%s/logs/exec-output/%s", path, url.PathEscape(instanceName), url.PathEscape(filename)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetInstanceMetadata returns instance metadata.
func (r *ProtocolIncus) GetInstanceMetadata(name string) (*api.ImageMetadata, string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, "", err
	}

	if !r.HasExtension("container_edit_metadata") {
		return nil, "", fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	metadata := api.ImageMetadata{}

	url := fmt.Sprintf("%s/%s/metadata", path, url.PathEscape(name))
	etag, err := r.queryStruct("GET", url, nil, "", &metadata)
	if err != nil {
		return nil, "", err
	}

	return &metadata, etag, err
}

// UpdateInstanceMetadata sets the content of the instance metadata file.
func (r *ProtocolIncus) UpdateInstanceMetadata(name string, metadata api.ImageMetadata, ETag string) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	if !r.HasExtension("container_edit_metadata") {
		return fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	url := fmt.Sprintf("%s/%s/metadata", path, url.PathEscape(name))
	_, _, err = r.query("PUT", url, metadata, ETag)
	if err != nil {
		return err
	}

	return nil
}

// GetInstanceTemplateFiles returns the list of names of template files for a instance.
func (r *ProtocolIncus) GetInstanceTemplateFiles(instanceName string) ([]string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_edit_metadata") {
		return nil, fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	templates := []string{}

	url := fmt.Sprintf("%s/%s/metadata/templates", path, url.PathEscape(instanceName))
	_, err = r.queryStruct("GET", url, nil, "", &templates)
	if err != nil {
		return nil, err
	}

	return templates, nil
}

// GetInstanceTemplateFile returns the content of a template file for a instance.
func (r *ProtocolIncus) GetInstanceTemplateFile(instanceName string, templateName string) (io.ReadCloser, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_edit_metadata") {
		return nil, fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	url := fmt.Sprintf("%s/1.0%s/%s/metadata/templates?path=%s", r.httpBaseURL.String(), path, url.PathEscape(instanceName), url.QueryEscape(templateName))

	url, err = r.setQueryAttributes(url)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := incusParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	return resp.Body, err
}

// CreateInstanceTemplateFile creates an a template for a instance.
func (r *ProtocolIncus) CreateInstanceTemplateFile(instanceName string, templateName string, content io.ReadSeeker) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	if !r.HasExtension("container_edit_metadata") {
		return fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	url := fmt.Sprintf("%s/1.0%s/%s/metadata/templates?path=%s", r.httpBaseURL.String(), path, url.PathEscape(instanceName), url.QueryEscape(templateName))

	url, err = r.setQueryAttributes(url)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, content)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	// Send the request
	resp, err := r.DoHTTP(req)
	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := incusParseResponse(resp)
		if err != nil {
			return err
		}
	}
	return err
}

// DeleteInstanceTemplateFile deletes a template file for a instance.
func (r *ProtocolIncus) DeleteInstanceTemplateFile(name string, templateName string) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	if !r.HasExtension("container_edit_metadata") {
		return fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	_, _, err = r.query("DELETE", fmt.Sprintf("%s/%s/metadata/templates?path=%s", path, url.PathEscape(name), url.QueryEscape(templateName)), nil, "")
	return err
}

// ConsoleInstance requests that Incus attaches to the console device of a instance.
func (r *ProtocolIncus) ConsoleInstance(instanceName string, console api.InstanceConsolePost, args *InstanceConsoleArgs) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("console") {
		return nil, fmt.Errorf("The server is missing the required \"console\" API extension")
	}

	if console.Type == "" {
		console.Type = "console"
	}

	if console.Type == "vga" && !r.HasExtension("console_vga_type") {
		return nil, fmt.Errorf("The server is missing the required \"console_vga_type\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/console", path, url.PathEscape(instanceName)), console, "")
	if err != nil {
		return nil, err
	}

	opAPI := op.Get()

	if args == nil || args.Terminal == nil {
		return nil, fmt.Errorf("A terminal must be set")
	}

	if args.Control == nil {
		return nil, fmt.Errorf("A control channel must be set")
	}

	// Parse the fds
	fds := map[string]string{}

	value, ok := opAPI.Metadata["fds"]
	if ok {
		values := value.(map[string]any)
		for k, v := range values {
			fds[k] = v.(string)
		}
	}

	var controlConn *websocket.Conn
	// Call the control handler with a connection to the control socket
	if fds[api.SecretNameControl] == "" {
		return nil, fmt.Errorf("Did not receive a file descriptor for the control channel")
	}

	controlConn, err = r.GetOperationWebsocket(opAPI.ID, fds[api.SecretNameControl])
	if err != nil {
		return nil, err
	}

	go args.Control(controlConn)

	// Connect to the websocket
	conn, err := r.GetOperationWebsocket(opAPI.ID, fds["0"])
	if err != nil {
		return nil, err
	}

	// Detach from console.
	go func(consoleDisconnect <-chan bool) {
		<-consoleDisconnect
		msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Detaching from console")
		// We don't care if this fails. This is just for convenience.
		_ = controlConn.WriteMessage(websocket.CloseMessage, msg)
		_ = controlConn.Close()
	}(args.ConsoleDisconnect)

	// And attach stdin and stdout to it
	go func() {
		_, writeDone := ws.Mirror(conn, args.Terminal)
		<-writeDone
		_ = conn.Close()
	}()

	return op, nil
}

// ConsoleInstanceDynamic requests that Incus attaches to the console device of a
// instance with the possibility of opening multiple connections to it.
//
// Every time the returned 'console' function is called, a new connection will
// be established and proxied to the given io.ReadWriteCloser.
func (r *ProtocolIncus) ConsoleInstanceDynamic(instanceName string, console api.InstanceConsolePost, args *InstanceConsoleArgs) (Operation, func(io.ReadWriteCloser) error, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, nil, err
	}

	if !r.HasExtension("console") {
		return nil, nil, fmt.Errorf("The server is missing the required \"console\" API extension")
	}

	if console.Type == "" {
		console.Type = "console"
	}

	if console.Type == "vga" && !r.HasExtension("console_vga_type") {
		return nil, nil, fmt.Errorf("The server is missing the required \"console_vga_type\" API extension")
	}

	// Send the request.
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/console", path, url.PathEscape(instanceName)), console, "")
	if err != nil {
		return nil, nil, err
	}

	opAPI := op.Get()

	if args == nil {
		return nil, nil, fmt.Errorf("No arguments provided")
	}

	if args.Control == nil {
		return nil, nil, fmt.Errorf("A control channel must be set")
	}

	// Parse the fds.
	fds := map[string]string{}

	value, ok := opAPI.Metadata["fds"]
	if ok {
		values := value.(map[string]any)
		for k, v := range values {
			fds[k] = v.(string)
		}
	}

	// Call the control handler with a connection to the control socket.
	if fds[api.SecretNameControl] == "" {
		return nil, nil, fmt.Errorf("Did not receive a file descriptor for the control channel")
	}

	controlConn, err := r.GetOperationWebsocket(opAPI.ID, fds[api.SecretNameControl])
	if err != nil {
		return nil, nil, err
	}

	go args.Control(controlConn)

	// Handle main disconnect.
	go func(consoleDisconnect <-chan bool) {
		<-consoleDisconnect
		msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Detaching from console")
		// We don't care if this fails. This is just for convenience.
		_ = controlConn.WriteMessage(websocket.CloseMessage, msg)
		_ = controlConn.Close()
	}(args.ConsoleDisconnect)

	f := func(rwc io.ReadWriteCloser) error {
		// Connect to the websocket.
		conn, err := r.GetOperationWebsocket(opAPI.ID, fds["0"])
		if err != nil {
			return err
		}

		// Attach reader/writer.
		_, writeDone := ws.Mirror(conn, rwc)
		<-writeDone
		_ = conn.Close()

		return nil
	}

	return op, f, nil
}

// GetInstanceConsoleLog requests that Incus attaches to the console device of a instance.
//
// Note that it's the caller's responsibility to close the returned ReadCloser.
func (r *ProtocolIncus) GetInstanceConsoleLog(instanceName string, args *InstanceConsoleLogArgs) (io.ReadCloser, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("console") {
		return nil, fmt.Errorf("The server is missing the required \"console\" API extension")
	}

	// Prepare the HTTP request
	url := fmt.Sprintf("%s/1.0%s/%s/console", r.httpBaseURL.String(), path, url.PathEscape(instanceName))

	url, err = r.setQueryAttributes(url)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := incusParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	return resp.Body, err
}

// DeleteInstanceConsoleLog deletes the requested instance's console log.
func (r *ProtocolIncus) DeleteInstanceConsoleLog(instanceName string, args *InstanceConsoleLogArgs) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	if !r.HasExtension("console") {
		return fmt.Errorf("The server is missing the required \"console\" API extension")
	}

	// Send the request
	_, _, err = r.query("DELETE", fmt.Sprintf("%s/%s/console", path, url.PathEscape(instanceName)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetInstanceBackupNames returns a list of backup names for the instance.
func (r *ProtocolIncus) GetInstanceBackupNames(instanceName string) ([]string, error) {
	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("%s/%s/backups", path, url.PathEscape(instanceName))
	_, err = r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetInstanceBackups returns a list of backups for the instance.
func (r *ProtocolIncus) GetInstanceBackups(instanceName string) ([]api.InstanceBackup, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Fetch the raw value
	backups := []api.InstanceBackup{}

	_, err = r.queryStruct("GET", fmt.Sprintf("%s/%s/backups?recursion=1", path, url.PathEscape(instanceName)), nil, "", &backups)
	if err != nil {
		return nil, err
	}

	return backups, nil
}

// GetInstanceBackup returns a Backup struct for the provided instance and backup names.
func (r *ProtocolIncus) GetInstanceBackup(instanceName string, name string) (*api.InstanceBackup, string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, "", err
	}

	if !r.HasExtension("container_backup") {
		return nil, "", fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Fetch the raw value
	backup := api.InstanceBackup{}
	etag, err := r.queryStruct("GET", fmt.Sprintf("%s/%s/backups/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), nil, "", &backup)
	if err != nil {
		return nil, "", err
	}

	return &backup, etag, nil
}

// CreateInstanceBackup requests that Incus creates a new backup for the instance.
func (r *ProtocolIncus) CreateInstanceBackup(instanceName string, backup api.InstanceBackupsPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/backups", path, url.PathEscape(instanceName)), backup, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// RenameInstanceBackup requests that Incus renames the backup.
func (r *ProtocolIncus) RenameInstanceBackup(instanceName string, name string, backup api.InstanceBackupPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/backups/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), backup, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteInstanceBackup requests that Incus deletes the instance backup.
func (r *ProtocolIncus) DeleteInstanceBackup(instanceName string, name string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("%s/%s/backups/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetInstanceBackupFile requests the instance backup content.
func (r *ProtocolIncus) GetInstanceBackupFile(instanceName string, name string, req *BackupFileRequest) (*BackupFileResponse, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Build the URL
	uri := fmt.Sprintf("%s/1.0%s/%s/backups/%s/export", r.httpBaseURL.String(), path, url.PathEscape(instanceName), url.PathEscape(name))
	if r.project != "" {
		uri += fmt.Sprintf("?project=%s", url.QueryEscape(r.project))
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

func (r *ProtocolIncus) proxyMigration(targetOp *operation, targetSecrets map[string]string, source InstanceServer, sourceOp *operation, sourceSecrets map[string]string) error {
	// Quick checks.
	for n := range targetSecrets {
		_, ok := sourceSecrets[n]
		if !ok {
			return fmt.Errorf("Migration target expects the \"%s\" socket but source isn't providing it", n)
		}
	}

	if targetSecrets[api.SecretNameControl] == "" {
		return fmt.Errorf("Migration target didn't setup the required \"control\" socket")
	}

	// Struct used to hold everything together
	type proxy struct {
		done       chan struct{}
		sourceConn *websocket.Conn
		targetConn *websocket.Conn
	}

	proxies := map[string]*proxy{}

	// Connect the control socket
	sourceConn, err := source.GetOperationWebsocket(sourceOp.ID, sourceSecrets[api.SecretNameControl])
	if err != nil {
		return err
	}

	targetConn, err := r.GetOperationWebsocket(targetOp.ID, targetSecrets[api.SecretNameControl])
	if err != nil {
		return err
	}

	proxies[api.SecretNameControl] = &proxy{
		done:       ws.Proxy(sourceConn, targetConn),
		sourceConn: sourceConn,
		targetConn: targetConn,
	}

	// Connect the data sockets
	for name := range sourceSecrets {
		if name == api.SecretNameControl {
			continue
		}

		// Handle resets (used for multiple objects)
		sourceConn, err := source.GetOperationWebsocket(sourceOp.ID, sourceSecrets[name])
		if err != nil {
			break
		}

		targetConn, err := r.GetOperationWebsocket(targetOp.ID, targetSecrets[name])
		if err != nil {
			break
		}

		proxies[name] = &proxy{
			sourceConn: sourceConn,
			targetConn: targetConn,
			done:       ws.Proxy(sourceConn, targetConn),
		}
	}

	// Cleanup once everything is done
	go func() {
		// Wait for control socket
		<-proxies[api.SecretNameControl].done
		_ = proxies[api.SecretNameControl].sourceConn.Close()
		_ = proxies[api.SecretNameControl].targetConn.Close()

		// Then deal with the others
		for name, proxy := range proxies {
			if name == api.SecretNameControl {
				continue
			}

			<-proxy.done
			_ = proxy.sourceConn.Close()
			_ = proxy.targetConn.Close()
		}
	}()

	return nil
}
