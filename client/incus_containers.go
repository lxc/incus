package incus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/ws"
)

// Container handling functions
//
// Deprecated: Those functions are deprecated and won't be updated anymore.
// Please use the equivalent Instance function instead.

// GetContainerNames returns a list of container names.
func (r *ProtocolLXD) GetContainerNames() ([]string, error) {
	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/containers"
	_, err := r.queryStruct("GET", "/containers", nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetContainers returns a list of containers.
func (r *ProtocolLXD) GetContainers() ([]api.Container, error) {
	containers := []api.Container{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/containers?recursion=1", nil, "", &containers)
	if err != nil {
		return nil, err
	}

	return containers, nil
}

// GetContainersFull returns a list of containers including snapshots, backups and state.
func (r *ProtocolLXD) GetContainersFull() ([]api.ContainerFull, error) {
	containers := []api.ContainerFull{}

	if !r.HasExtension("container_full") {
		return nil, fmt.Errorf("The server is missing the required \"container_full\" API extension")
	}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/containers?recursion=2", nil, "", &containers)
	if err != nil {
		return nil, err
	}

	return containers, nil
}

// GetContainer returns the container entry for the provided name.
func (r *ProtocolLXD) GetContainer(name string) (*api.Container, string, error) {
	container := api.Container{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s", url.PathEscape(name)), nil, "", &container)
	if err != nil {
		return nil, "", err
	}

	return &container, etag, nil
}

// CreateContainerFromBackup is a convenience function to make it easier to
// create a container from a backup.
func (r *ProtocolLXD) CreateContainerFromBackup(args ContainerBackupArgs) (Operation, error) {
	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	if args.PoolName == "" {
		// Send the request
		op, _, err := r.queryOperation("POST", "/containers", args.BackupFile, "")
		if err != nil {
			return nil, err
		}

		return op, nil
	}

	if !r.HasExtension("container_backup_override_pool") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup_override_pool\" API extension")
	}

	// Prepare the HTTP request
	reqURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0/containers", r.httpBaseURL.String()))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", reqURL, args.BackupFile)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-LXD-pool", args.PoolName)

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	// Handle errors
	response, _, err := lxdParseResponse(resp)
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

// CreateContainer requests that LXD creates a new container.
func (r *ProtocolLXD) CreateContainer(container api.ContainersPost) (Operation, error) {
	if container.Source.ContainerOnly {
		if !r.HasExtension("container_only_migration") {
			return nil, fmt.Errorf("The server is missing the required \"container_only_migration\" API extension")
		}
	}

	// Send the request
	op, _, err := r.queryOperation("POST", "/containers", container, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

func (r *ProtocolLXD) tryCreateContainer(req api.ContainersPost, urls []string) (RemoteOperation, error) {
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

			op, err := r.CreateContainer(req)
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

				if shared.IsConnectionError(err) {
					continue
				}

				break
			}

			success = true
			break
		}

		if !success {
			rop.err = remoteOperationError("Failed container creation", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// CreateContainerFromImage is a convenience function to make it easier to create a container from an existing image.
func (r *ProtocolLXD) CreateContainerFromImage(source ImageServer, image api.Image, req api.ContainersPost) (RemoteOperation, error) {
	// Set the minimal source fields
	req.Source.Type = "image"

	// Optimization for the local image case
	if r.isSameServer(source) {
		// Always use fingerprints for local case
		req.Source.Fingerprint = image.Fingerprint
		req.Source.Alias = ""

		op, err := r.CreateContainer(req)
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

	// Minimal source fields for remote image
	req.Source.Mode = "pull"

	// If we have an alias and the image is public, use that
	if req.Source.Alias != "" && image.Public {
		req.Source.Fingerprint = ""
	} else {
		req.Source.Fingerprint = image.Fingerprint
		req.Source.Alias = ""
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	req.Source.Protocol = info.Protocol
	req.Source.Certificate = info.Certificate

	// Generate secret token if needed
	if !image.Public {
		secret, err := source.GetImageSecret(image.Fingerprint)
		if err != nil {
			return nil, err
		}

		req.Source.Secret = secret
	}

	return r.tryCreateContainer(req, info.Addresses)
}

// CopyContainer copies a container from a remote server. Additional options can be passed using ContainerCopyArgs.
func (r *ProtocolLXD) CopyContainer(source InstanceServer, container api.Container, args *ContainerCopyArgs) (RemoteOperation, error) {
	// Base request
	req := api.ContainersPost{
		Name:         container.Name,
		ContainerPut: container.Writable(),
	}

	req.Source.BaseImage = container.Config["volatile.base_image"]

	// Process the copy arguments
	if args != nil {
		// Quick checks.
		if args.ContainerOnly {
			if !r.HasExtension("container_only_migration") {
				return nil, fmt.Errorf("The target server is missing the required \"container_only_migration\" API extension")
			}

			if !source.HasExtension("container_only_migration") {
				return nil, fmt.Errorf("The source server is missing the required \"container_only_migration\" API extension")
			}
		}

		if shared.StringInSlice(args.Mode, []string{"push", "relay"}) {
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

		// Allow overriding the target name
		if args.Name != "" {
			req.Name = args.Name
		}

		req.Source.Live = args.Live
		req.Source.ContainerOnly = args.ContainerOnly
		req.Source.Refresh = args.Refresh
	}

	if req.Source.Live {
		req.Source.Live = container.StatusCode == api.Running
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
	if destInfo.URL == sourceInfo.URL && destInfo.SocketPath == sourceInfo.SocketPath && (!r.IsClustered() || container.Location == r.clusterTarget || r.HasExtension("cluster_internal_copy")) {
		// Project handling
		if destInfo.Project != sourceInfo.Project {
			if !r.HasExtension("container_copy_project") {
				return nil, fmt.Errorf("The server is missing the required \"container_copy_project\" API extension")
			}

			req.Source.Project = sourceInfo.Project
		}

		// Local copy source fields
		req.Source.Type = "copy"
		req.Source.Source = container.Name

		// Copy the container
		op, err := r.CreateContainer(req)
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
	sourceReq := api.ContainerPost{
		Migration:     true,
		Live:          req.Source.Live,
		ContainerOnly: req.Source.ContainerOnly,
	}

	// Push mode migration
	if args != nil && args.Mode == "push" {
		// Get target server connection information
		info, err := r.GetConnectionInfo()
		if err != nil {
			return nil, err
		}

		// Create the container
		req.Source.Type = "migration"
		req.Source.Mode = "push"
		req.Source.Refresh = args.Refresh

		op, err := r.CreateContainer(req)
		if err != nil {
			return nil, err
		}

		opAPI := op.Get()

		targetSecrets := map[string]string{}
		for k, v := range opAPI.Metadata {
			targetSecrets[k] = v.(string)
		}

		// Prepare the source request
		target := api.ContainerPostTarget{}
		target.Operation = opAPI.ID
		target.Websockets = targetSecrets
		target.Certificate = info.Certificate
		sourceReq.Target = &target

		return r.tryMigrateContainer(source, container.Name, sourceReq, info.Addresses)
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	op, err := source.MigrateContainer(container.Name, sourceReq)
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
		targetOp, err := r.CreateContainer(req)
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

	return r.tryCreateContainer(req, info.Addresses)
}

// UpdateContainer updates the container definition.
func (r *ProtocolLXD) UpdateContainer(name string, container api.ContainerPut, ETag string) (Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("/containers/%s", url.PathEscape(name)), container, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// RenameContainer requests that LXD renames the container.
func (r *ProtocolLXD) RenameContainer(name string, container api.ContainerPost) (Operation, error) {
	// Quick check.
	if container.Migration {
		return nil, fmt.Errorf("Can't ask for a migration through RenameContainer")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s", url.PathEscape(name)), container, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

func (r *ProtocolLXD) tryMigrateContainer(source InstanceServer, name string, req api.ContainerPost, urls []string) (RemoteOperation, error) {
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

			op, err := source.MigrateContainer(name, req)
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

				if shared.IsConnectionError(err) {
					continue
				}

				break
			}

			success = true
			break
		}

		if !success {
			rop.err = remoteOperationError("Failed container migration", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// MigrateContainer requests that LXD prepares for a container migration.
func (r *ProtocolLXD) MigrateContainer(name string, container api.ContainerPost) (Operation, error) {
	if container.ContainerOnly {
		if !r.HasExtension("container_only_migration") {
			return nil, fmt.Errorf("The server is missing the required \"container_only_migration\" API extension")
		}
	}

	// Quick check.
	if !container.Migration {
		return nil, fmt.Errorf("Can't ask for a rename through MigrateContainer")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s", url.PathEscape(name)), container, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteContainer requests that LXD deletes the container.
func (r *ProtocolLXD) DeleteContainer(name string) (Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("/containers/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// ExecContainer requests that LXD spawns a command inside the container.
func (r *ProtocolLXD) ExecContainer(containerName string, exec api.ContainerExecPost, args *ContainerExecArgs) (Operation, error) {
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

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s/exec", url.PathEscape(containerName)), exec, "")
	if err != nil {
		return nil, err
	}

	opAPI := op.Get()

	// Process additional arguments
	if args != nil {
		// Parse the fds
		fds := map[string]string{}

		value, ok := opAPI.Metadata["fds"]
		if ok {
			values := value.(map[string]any)
			for k, v := range values {
				fds[k] = v.(string)
			}
		}

		// Call the control handler with a connection to the control socket
		if args.Control != nil && fds[api.SecretNameControl] != "" {
			conn, err := r.GetOperationWebsocket(opAPI.ID, fds[api.SecretNameControl])
			if err != nil {
				return nil, err
			}

			go args.Control(conn)
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
					ws.MirrorRead(context.Background(), conn, args.Stdin)
					<-ws.MirrorWrite(context.Background(), conn, args.Stdout)
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
			dones := make(map[int]chan struct{})
			conns := []*websocket.Conn{}

			// Handle stdin
			if fds["0"] != "" {
				conn, err := r.GetOperationWebsocket(opAPI.ID, fds["0"])
				if err != nil {
					return nil, err
				}

				conns = append(conns, conn)
				dones[0] = ws.MirrorRead(context.Background(), conn, args.Stdin)
			}

			// Handle stdout
			if fds["1"] != "" {
				conn, err := r.GetOperationWebsocket(opAPI.ID, fds["1"])
				if err != nil {
					return nil, err
				}

				conns = append(conns, conn)
				dones[1] = ws.MirrorWrite(context.Background(), conn, args.Stdout)
			}

			// Handle stderr
			if fds["2"] != "" {
				conn, err := r.GetOperationWebsocket(opAPI.ID, fds["2"])
				if err != nil {
					return nil, err
				}

				conns = append(conns, conn)
				dones[2] = ws.MirrorWrite(context.Background(), conn, args.Stderr)
			}

			// Wait for everything to be done
			go func() {
				for i, chDone := range dones {
					// Skip stdin, dealing with it separately below
					if i == 0 {
						continue
					}

					<-chDone
				}

				if fds["0"] != "" {
					if args.Stdin != nil {
						_ = args.Stdin.Close()
					}

					// Empty the stdin channel but don't block on it as
					// stdin may be stuck in Read()
					go func() {
						<-dones[0]
					}()
				}

				for _, conn := range conns {
					_ = conn.Close()
				}

				if args.DataDone != nil {
					close(args.DataDone)
				}
			}()
		}
	}

	return op, nil
}

// GetContainerFile retrieves the provided path from the container.
func (r *ProtocolLXD) GetContainerFile(containerName string, path string) (io.ReadCloser, *ContainerFileResponse, error) {
	// Prepare the HTTP request
	requestURL, err := shared.URLEncode(
		fmt.Sprintf("%s/1.0/containers/%s/files", r.httpBaseURL.String(), url.PathEscape(containerName)),
		map[string]string{"path": path})
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
		_, _, err := lxdParseResponse(resp)
		if err != nil {
			return nil, nil, err
		}
	}

	// Parse the headers
	uid, gid, mode, fileType, _ := shared.ParseLXDFileHeaders(resp.Header)
	fileResp := ContainerFileResponse{
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

// CreateContainerFile tells LXD to create a file in the container.
func (r *ProtocolLXD) CreateContainerFile(containerName string, path string, args ContainerFileArgs) error {
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

	// Prepare the HTTP request
	requestURL := fmt.Sprintf("%s/1.0/containers/%s/files?path=%s", r.httpBaseURL.String(), url.PathEscape(containerName), url.QueryEscape(path))

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
		req.Header.Set("X-LXD-uid", fmt.Sprintf("%d", args.UID))
	}

	if args.GID > -1 {
		req.Header.Set("X-LXD-gid", fmt.Sprintf("%d", args.GID))
	}

	if args.Mode > -1 {
		req.Header.Set("X-LXD-mode", fmt.Sprintf("%04o", args.Mode))
	}

	if args.Type != "" {
		req.Header.Set("X-LXD-type", args.Type)
	}

	if args.WriteMode != "" {
		req.Header.Set("X-LXD-write", args.WriteMode)
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return err
	}

	// Check the return value for a cleaner error
	_, _, err = lxdParseResponse(resp)
	if err != nil {
		return err
	}

	return nil
}

// DeleteContainerFile deletes a file in the container.
func (r *ProtocolLXD) DeleteContainerFile(containerName string, path string) error {
	if !r.HasExtension("file_delete") {
		return fmt.Errorf("The server is missing the required \"file_delete\" API extension")
	}

	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/containers/%s/files?path=%s", url.PathEscape(containerName), url.QueryEscape(path)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetContainerSnapshotNames returns a list of snapshot names for the container.
func (r *ProtocolLXD) GetContainerSnapshotNames(containerName string) ([]string, error) {
	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("/containers/%s/snapshots", url.PathEscape(containerName))
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetContainerSnapshots returns a list of snapshots for the container.
func (r *ProtocolLXD) GetContainerSnapshots(containerName string) ([]api.ContainerSnapshot, error) {
	snapshots := []api.ContainerSnapshot{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s/snapshots?recursion=1", url.PathEscape(containerName)), nil, "", &snapshots)
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}

// GetContainerSnapshot returns a Snapshot struct for the provided container and snapshot names.
func (r *ProtocolLXD) GetContainerSnapshot(containerName string, name string) (*api.ContainerSnapshot, string, error) {
	snapshot := api.ContainerSnapshot{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s/snapshots/%s", url.PathEscape(containerName), url.PathEscape(name)), nil, "", &snapshot)
	if err != nil {
		return nil, "", err
	}

	return &snapshot, etag, nil
}

// CreateContainerSnapshot requests that LXD creates a new snapshot for the container.
func (r *ProtocolLXD) CreateContainerSnapshot(containerName string, snapshot api.ContainerSnapshotsPost) (Operation, error) {
	// Validate the request
	if snapshot.ExpiresAt != nil && !r.HasExtension("snapshot_expiry_creation") {
		return nil, fmt.Errorf("The server is missing the required \"snapshot_expiry_creation\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s/snapshots", url.PathEscape(containerName)), snapshot, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// CopyContainerSnapshot copies a snapshot from a remote server into a new container. Additional options can be passed using ContainerCopyArgs.
func (r *ProtocolLXD) CopyContainerSnapshot(source InstanceServer, containerName string, snapshot api.ContainerSnapshot, args *ContainerSnapshotCopyArgs) (RemoteOperation, error) {
	// Backward compatibility (with broken Name field)
	fields := strings.Split(snapshot.Name, shared.SnapshotDelimiter)
	cName := containerName
	sName := fields[len(fields)-1]

	// Base request
	req := api.ContainersPost{
		Name: cName,
		ContainerPut: api.ContainerPut{
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

		req.ContainerPut.Stateful = snapshot.Stateful
		req.Source.Live = false // Snapshots are never running and so we don't need live migration.
	}

	req.Source.BaseImage = snapshot.Config["volatile.base_image"]

	// Process the copy arguments
	if args != nil {
		// Quick checks.
		if shared.StringInSlice(args.Mode, []string{"push", "relay"}) {
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

	container, _, err := source.GetContainer(cName)
	if err != nil {
		return nil, fmt.Errorf("Failed to get container info: %w", err)
	}

	// Optimization for the local copy case
	if destInfo.URL == sourceInfo.URL && destInfo.SocketPath == sourceInfo.SocketPath && (!r.IsClustered() || container.Location == r.clusterTarget || r.HasExtension("cluster_internal_copy")) {
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

		// Copy the container
		op, err := r.CreateContainer(req)
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
	sourceReq := api.ContainerSnapshotPost{
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

		// Create the container
		req.Source.Type = "migration"
		req.Source.Mode = "push"

		op, err := r.CreateContainer(req)
		if err != nil {
			return nil, err
		}

		opAPI := op.Get()

		targetSecrets := map[string]string{}
		for k, v := range opAPI.Metadata {
			targetSecrets[k] = v.(string)
		}

		// Prepare the source request
		target := api.ContainerPostTarget{}
		target.Operation = opAPI.ID
		target.Websockets = targetSecrets
		target.Certificate = info.Certificate
		sourceReq.Target = &target

		return r.tryMigrateContainerSnapshot(source, cName, sName, sourceReq, info.Addresses)
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	op, err := source.MigrateContainerSnapshot(cName, sName, sourceReq)
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
		targetOp, err := r.CreateContainer(req)
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

	return r.tryCreateContainer(req, info.Addresses)
}

// RenameContainerSnapshot requests that LXD renames the snapshot.
func (r *ProtocolLXD) RenameContainerSnapshot(containerName string, name string, container api.ContainerSnapshotPost) (Operation, error) {
	// Quick check.
	if container.Migration {
		return nil, fmt.Errorf("Can't ask for a migration through RenameContainerSnapshot")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s/snapshots/%s", url.PathEscape(containerName), url.PathEscape(name)), container, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// tryMigrateContainerSnapshot attempts to migrate a container snapshot from the source instance server to one of the target URLs.
// It runs the migration asynchronously and returns a RemoteOperation to track the migration status and any errors.
func (r *ProtocolLXD) tryMigrateContainerSnapshot(source InstanceServer, containerName string, name string, req api.ContainerSnapshotPost, urls []string) (RemoteOperation, error) {
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

			op, err := source.MigrateContainerSnapshot(containerName, name, req)
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

				if shared.IsConnectionError(err) {
					continue
				}

				break
			}

			success = true
			break
		}

		if !success {
			rop.err = remoteOperationError("Failed container migration", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// MigrateContainerSnapshot requests that LXD prepares for a snapshot migration.
func (r *ProtocolLXD) MigrateContainerSnapshot(containerName string, name string, container api.ContainerSnapshotPost) (Operation, error) {
	// Quick check.
	if !container.Migration {
		return nil, fmt.Errorf("Can't ask for a rename through MigrateContainerSnapshot")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s/snapshots/%s", url.PathEscape(containerName), url.PathEscape(name)), container, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteContainerSnapshot requests that LXD deletes the container snapshot.
func (r *ProtocolLXD) DeleteContainerSnapshot(containerName string, name string) (Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("/containers/%s/snapshots/%s", url.PathEscape(containerName), url.PathEscape(name)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateContainerSnapshot requests that LXD updates the container snapshot.
func (r *ProtocolLXD) UpdateContainerSnapshot(containerName string, name string, container api.ContainerSnapshotPut, ETag string) (Operation, error) {
	if !r.HasExtension("snapshot_expiry") {
		return nil, fmt.Errorf("The server is missing the required \"snapshot_expiry\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("/containers/%s/snapshots/%s",
		url.PathEscape(containerName), url.PathEscape(name)), container, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetContainerState returns a ContainerState entry for the provided container name.
func (r *ProtocolLXD) GetContainerState(name string) (*api.ContainerState, string, error) {
	state := api.ContainerState{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s/state", url.PathEscape(name)), nil, "", &state)
	if err != nil {
		return nil, "", err
	}

	return &state, etag, nil
}

// UpdateContainerState updates the container to match the requested state.
func (r *ProtocolLXD) UpdateContainerState(name string, state api.ContainerStatePut, ETag string) (Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("/containers/%s/state", url.PathEscape(name)), state, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetContainerLogfiles returns a list of logfiles for the container.
func (r *ProtocolLXD) GetContainerLogfiles(name string) ([]string, error) {
	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("/containers/%s/logs", url.PathEscape(name))
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetContainerLogfile returns the content of the requested logfile
//
// Note that it's the caller's responsibility to close the returned ReadCloser.
func (r *ProtocolLXD) GetContainerLogfile(name string, filename string) (io.ReadCloser, error) {
	// Prepare the HTTP request
	url := fmt.Sprintf("%s/1.0/containers/%s/logs/%s", r.httpBaseURL.String(), url.PathEscape(name), url.PathEscape(filename))

	url, err := r.setQueryAttributes(url)
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
		_, _, err := lxdParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	return resp.Body, err
}

// DeleteContainerLogfile deletes the requested logfile.
func (r *ProtocolLXD) DeleteContainerLogfile(name string, filename string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/containers/%s/logs/%s", url.PathEscape(name), url.PathEscape(filename)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetContainerMetadata returns container metadata.
func (r *ProtocolLXD) GetContainerMetadata(name string) (*api.ImageMetadata, string, error) {
	if !r.HasExtension("container_edit_metadata") {
		return nil, "", fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	metadata := api.ImageMetadata{}

	url := fmt.Sprintf("/containers/%s/metadata", url.PathEscape(name))
	etag, err := r.queryStruct("GET", url, nil, "", &metadata)
	if err != nil {
		return nil, "", err
	}

	return &metadata, etag, err
}

// SetContainerMetadata sets the content of the container metadata file.
func (r *ProtocolLXD) SetContainerMetadata(name string, metadata api.ImageMetadata, ETag string) error {
	if !r.HasExtension("container_edit_metadata") {
		return fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	url := fmt.Sprintf("/containers/%s/metadata", url.PathEscape(name))
	_, _, err := r.query("PUT", url, metadata, ETag)
	if err != nil {
		return err
	}

	return nil
}

// GetContainerTemplateFiles returns the list of names of template files for a container.
func (r *ProtocolLXD) GetContainerTemplateFiles(containerName string) ([]string, error) {
	if !r.HasExtension("container_edit_metadata") {
		return nil, fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	templates := []string{}

	url := fmt.Sprintf("/containers/%s/metadata/templates", url.PathEscape(containerName))
	_, err := r.queryStruct("GET", url, nil, "", &templates)
	if err != nil {
		return nil, err
	}

	return templates, nil
}

// GetContainerTemplateFile returns the content of a template file for a container.
func (r *ProtocolLXD) GetContainerTemplateFile(containerName string, templateName string) (io.ReadCloser, error) {
	if !r.HasExtension("container_edit_metadata") {
		return nil, fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	url := fmt.Sprintf("%s/1.0/containers/%s/metadata/templates?path=%s", r.httpBaseURL.String(), url.PathEscape(containerName), url.QueryEscape(templateName))

	url, err := r.setQueryAttributes(url)
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
		_, _, err := lxdParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	return resp.Body, err
}

// CreateContainerTemplateFile creates an a template for a container.
func (r *ProtocolLXD) CreateContainerTemplateFile(containerName string, templateName string, content io.ReadSeeker) error {
	if !r.HasExtension("container_edit_metadata") {
		return fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	url := fmt.Sprintf("%s/1.0/containers/%s/metadata/templates?path=%s", r.httpBaseURL.String(), url.PathEscape(containerName), url.QueryEscape(templateName))

	url, err := r.setQueryAttributes(url)
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
		_, _, err := lxdParseResponse(resp)
		if err != nil {
			return err
		}
	}
	return err
}

// UpdateContainerTemplateFile updates the content for a container template file.
func (r *ProtocolLXD) UpdateContainerTemplateFile(containerName string, templateName string, content io.ReadSeeker) error {
	return r.CreateContainerTemplateFile(containerName, templateName, content)
}

// DeleteContainerTemplateFile deletes a template file for a container.
func (r *ProtocolLXD) DeleteContainerTemplateFile(name string, templateName string) error {
	if !r.HasExtension("container_edit_metadata") {
		return fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	_, _, err := r.query("DELETE", fmt.Sprintf("/containers/%s/metadata/templates?path=%s", url.PathEscape(name), url.QueryEscape(templateName)), nil, "")
	return err
}

// ConsoleContainer requests that LXD attaches to the console device of a container.
func (r *ProtocolLXD) ConsoleContainer(containerName string, console api.ContainerConsolePost, args *ContainerConsoleArgs) (Operation, error) {
	if !r.HasExtension("console") {
		return nil, fmt.Errorf("The server is missing the required \"console\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s/console", url.PathEscape(containerName)), console, "")
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
		_, writeDone := ws.Mirror(context.Background(), conn, args.Terminal)
		<-writeDone
		_ = conn.Close()
	}()

	return op, nil
}

// GetContainerConsoleLog requests that LXD attaches to the console device of a container.
//
// Note that it's the caller's responsibility to close the returned ReadCloser.
func (r *ProtocolLXD) GetContainerConsoleLog(containerName string, args *ContainerConsoleLogArgs) (io.ReadCloser, error) {
	if !r.HasExtension("console") {
		return nil, fmt.Errorf("The server is missing the required \"console\" API extension")
	}

	// Prepare the HTTP request
	url := fmt.Sprintf("%s/1.0/containers/%s/console", r.httpBaseURL.String(), url.PathEscape(containerName))

	url, err := r.setQueryAttributes(url)
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
		_, _, err := lxdParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	return resp.Body, err
}

// DeleteContainerConsoleLog deletes the requested container's console log.
func (r *ProtocolLXD) DeleteContainerConsoleLog(containerName string, args *ContainerConsoleLogArgs) error {
	if !r.HasExtension("console") {
		return fmt.Errorf("The server is missing the required \"console\" API extension")
	}

	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/containers/%s/console", url.PathEscape(containerName)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetContainerBackupNames returns a list of backup names for the container.
func (r *ProtocolLXD) GetContainerBackupNames(containerName string) ([]string, error) {
	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("/containers/%s/backups", url.PathEscape(containerName))
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetContainerBackups returns a list of backups for the container.
func (r *ProtocolLXD) GetContainerBackups(containerName string) ([]api.ContainerBackup, error) {
	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Fetch the raw value
	backups := []api.ContainerBackup{}

	_, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s/backups?recursion=1", url.PathEscape(containerName)), nil, "", &backups)
	if err != nil {
		return nil, err
	}

	return backups, nil
}

// GetContainerBackup returns a Backup struct for the provided container and backup names.
func (r *ProtocolLXD) GetContainerBackup(containerName string, name string) (*api.ContainerBackup, string, error) {
	if !r.HasExtension("container_backup") {
		return nil, "", fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Fetch the raw value
	backup := api.ContainerBackup{}
	etag, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s/backups/%s", url.PathEscape(containerName), url.PathEscape(name)), nil, "", &backup)
	if err != nil {
		return nil, "", err
	}

	return &backup, etag, nil
}

// CreateContainerBackup requests that LXD creates a new backup for the container.
func (r *ProtocolLXD) CreateContainerBackup(containerName string, backup api.ContainerBackupsPost) (Operation, error) {
	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s/backups",
		url.PathEscape(containerName)), backup, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// RenameContainerBackup requests that LXD renames the backup.
func (r *ProtocolLXD) RenameContainerBackup(containerName string, name string, backup api.ContainerBackupPost) (Operation, error) {
	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s/backups/%s",
		url.PathEscape(containerName), url.PathEscape(name)), backup, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteContainerBackup requests that LXD deletes the container backup.
func (r *ProtocolLXD) DeleteContainerBackup(containerName string, name string) (Operation, error) {
	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("/containers/%s/backups/%s",
		url.PathEscape(containerName), url.PathEscape(name)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetContainerBackupFile requests the container backup content.
func (r *ProtocolLXD) GetContainerBackupFile(containerName string, name string, req *BackupFileRequest) (*BackupFileResponse, error) {
	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Build the URL
	uri := fmt.Sprintf("%s/1.0/containers/%s/backups/%s/export", r.httpBaseURL.String(),
		url.PathEscape(containerName), url.PathEscape(name))
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
		_, _, err := lxdParseResponse(response)
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
