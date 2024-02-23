package incus

import (
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/shared/api"
	localtls "github.com/lxc/incus/shared/tls"
)

// Server handling functions

// GetServer returns the server status as a Server struct.
func (r *ProtocolIncus) GetServer() (*api.Server, string, error) {
	server := api.Server{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", "", nil, "", &server)
	if err != nil {
		return nil, "", err
	}

	// Fill in certificate fingerprint if not provided
	if server.Environment.CertificateFingerprint == "" && server.Environment.Certificate != "" {
		var err error
		server.Environment.CertificateFingerprint, err = localtls.CertFingerprintStr(server.Environment.Certificate)
		if err != nil {
			return nil, "", err
		}
	}

	if !server.Public && len(server.AuthMethods) == 0 {
		// TLS is always available for Incus servers
		server.AuthMethods = []string{api.AuthenticationMethodTLS}
	}

	// Add the value to the cache
	r.server = &server

	return &server, etag, nil
}

// UpdateServer updates the server status to match the provided Server struct.
func (r *ProtocolIncus) UpdateServer(server api.ServerPut, ETag string) error {
	// Send the request
	_, _, err := r.query("PUT", "", server, ETag)
	if err != nil {
		return err
	}

	return nil
}

// HasExtension returns true if the server supports a given API extension.
// Deprecated: Use CheckExtension instead.
func (r *ProtocolIncus) HasExtension(extension string) bool {
	// If no cached API information, just assume we're good
	// This is needed for those rare cases where we must avoid a GetServer call
	if r.server == nil {
		return true
	}

	return slices.Contains(r.server.APIExtensions, extension)
}

// CheckExtension checks if the server has the specified extension.
func (r *ProtocolIncus) CheckExtension(extensionName string) error {
	if !r.HasExtension(extensionName) {
		return fmt.Errorf("The server is missing the required %q API extension", extensionName)
	}

	return nil
}

// IsClustered returns true if the server is part of an Incus cluster.
func (r *ProtocolIncus) IsClustered() bool {
	return r.server.Environment.ServerClustered
}

// GetServerResources returns the resources available to a given Incus server.
func (r *ProtocolIncus) GetServerResources() (*api.Resources, error) {
	if !r.HasExtension("resources") {
		return nil, fmt.Errorf("The server is missing the required \"resources\" API extension")
	}

	resources := api.Resources{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/resources", nil, "", &resources)
	if err != nil {
		return nil, err
	}

	return &resources, nil
}

// UseProject returns a client that will use a specific project.
func (r *ProtocolIncus) UseProject(name string) InstanceServer {
	return &ProtocolIncus{
		ctx:                  r.ctx,
		ctxConnected:         r.ctxConnected,
		ctxConnectedCancel:   r.ctxConnectedCancel,
		server:               r.server,
		http:                 r.http,
		httpCertificate:      r.httpCertificate,
		httpBaseURL:          r.httpBaseURL,
		httpProtocol:         r.httpProtocol,
		httpUserAgent:        r.httpUserAgent,
		httpUnixPath:         r.httpUnixPath,
		requireAuthenticated: r.requireAuthenticated,
		clusterTarget:        r.clusterTarget,
		project:              name,
		eventConns:           make(map[string]*websocket.Conn),  // New project specific listener conns.
		eventListeners:       make(map[string][]*EventListener), // New project specific listeners.
		oidcClient:           r.oidcClient,
	}
}

// UseTarget returns a client that will target a specific cluster member.
// Use this member-specific operations such as specific container
// placement, preparing a new storage pool or network, ...
func (r *ProtocolIncus) UseTarget(name string) InstanceServer {
	return &ProtocolIncus{
		ctx:                  r.ctx,
		ctxConnected:         r.ctxConnected,
		ctxConnectedCancel:   r.ctxConnectedCancel,
		server:               r.server,
		http:                 r.http,
		httpCertificate:      r.httpCertificate,
		httpBaseURL:          r.httpBaseURL,
		httpProtocol:         r.httpProtocol,
		httpUserAgent:        r.httpUserAgent,
		httpUnixPath:         r.httpUnixPath,
		requireAuthenticated: r.requireAuthenticated,
		project:              r.project,
		eventConns:           make(map[string]*websocket.Conn),  // New target specific listener conns.
		eventListeners:       make(map[string][]*EventListener), // New target specific listeners.
		oidcClient:           r.oidcClient,
		clusterTarget:        name,
	}
}

// IsAgent returns true if the server is an Incus agent.
func (r *ProtocolIncus) IsAgent() bool {
	return r.server != nil && r.server.Environment.Server == "incus-agent"
}

// GetMetrics returns the text OpenMetrics data.
func (r *ProtocolIncus) GetMetrics() (string, error) {
	// Check that the server supports it.
	if !r.HasExtension("metrics") {
		return "", fmt.Errorf("The server is missing the required \"metrics\" API extension")
	}

	// Prepare the request.
	requestURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0/metrics", r.httpBaseURL.String()))
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return "", err
	}

	// Send the request.
	resp, err := r.DoHTTP(req)
	if err != nil {
		return "", err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Bad HTTP status: %d", resp.StatusCode)
	}

	// Get the content.
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// ApplyServerPreseed configures a target Incus server with the provided server and cluster configuration.
func (r *ProtocolIncus) ApplyServerPreseed(config api.InitPreseed) error {
	// Apply server configuration.
	if config.Server.Config != nil && len(config.Server.Config) > 0 {
		// Get current config.
		server, etag, err := r.GetServer()
		if err != nil {
			return fmt.Errorf("Failed to retrieve current server configuration: %w", err)
		}

		for k, v := range config.Server.Config {
			server.Config[k] = fmt.Sprintf("%v", v)
		}

		// Apply it.
		err = r.UpdateServer(server.Writable(), etag)
		if err != nil {
			return fmt.Errorf("Failed to update server configuration: %w", err)
		}
	}

	// Apply storage configuration.
	if config.Server.StoragePools != nil && len(config.Server.StoragePools) > 0 {
		// Get the list of storagePools.
		storagePoolNames, err := r.GetStoragePoolNames()
		if err != nil {
			return fmt.Errorf("Failed to retrieve list of storage pools: %w", err)
		}

		// StoragePool creator
		createStoragePool := func(storagePool api.StoragePoolsPost) error {
			// Create the storagePool if doesn't exist.
			err := r.CreateStoragePool(storagePool)
			if err != nil {
				return fmt.Errorf("Failed to create storage pool %q: %w", storagePool.Name, err)
			}

			return nil
		}

		// StoragePool updater.
		updateStoragePool := func(target api.StoragePoolsPost) error {
			// Get the current storagePool.
			storagePool, etag, err := r.GetStoragePool(target.Name)
			if err != nil {
				return fmt.Errorf("Failed to retrieve current storage pool %q: %w", target.Name, err)
			}

			// Quick check.
			if storagePool.Driver != target.Driver {
				return fmt.Errorf("Storage pool %q is of type %q instead of %q", storagePool.Name, storagePool.Driver, target.Driver)
			}

			// Description override.
			if target.Description != "" {
				storagePool.Description = target.Description
			}

			// Config overrides.
			for k, v := range target.Config {
				storagePool.Config[k] = fmt.Sprintf("%v", v)
			}

			// Apply it.
			err = r.UpdateStoragePool(target.Name, storagePool.Writable(), etag)
			if err != nil {
				return fmt.Errorf("Failed to update storage pool %q: %w", target.Name, err)
			}

			return nil
		}

		for _, storagePool := range config.Server.StoragePools {
			// New storagePool.
			if !slices.Contains(storagePoolNames, storagePool.Name) {
				err := createStoragePool(storagePool)
				if err != nil {
					return err
				}

				continue
			}

			// Existing storagePool.
			err := updateStoragePool(storagePool)
			if err != nil {
				return err
			}
		}
	}

	// Apply network configuration function.
	applyNetwork := func(target api.InitNetworksProjectPost) error {
		network, etag, err := r.UseProject(target.Project).GetNetwork(target.Name)
		if err != nil {
			// Create the network if doesn't exist.
			err := r.UseProject(target.Project).CreateNetwork(target.NetworksPost)
			if err != nil {
				return fmt.Errorf("Failed to create local member network %q in project %q: %w", target.Name, target.Project, err)
			}
		} else {
			// Description override.
			if target.Description != "" {
				network.Description = target.Description
			}

			// Config overrides.
			for k, v := range target.Config {
				network.Config[k] = fmt.Sprintf("%v", v)
			}

			// Apply it.
			err = r.UseProject(target.Project).UpdateNetwork(target.Name, network.Writable(), etag)
			if err != nil {
				return fmt.Errorf("Failed to update local member network %q in project %q: %w", target.Name, target.Project, err)
			}
		}

		return nil
	}

	// Apply networks in the default project before other projects config applied (so that if the projects
	// depend on a network in the default project they can have their config applied successfully).
	for i := range config.Server.Networks {
		// Populate default project if not specified for backwards compatbility with earlier
		// preseed dump files.
		if config.Server.Networks[i].Project == "" {
			config.Server.Networks[i].Project = api.ProjectDefaultName
		}

		if config.Server.Networks[i].Project != api.ProjectDefaultName {
			continue
		}

		err := applyNetwork(config.Server.Networks[i])
		if err != nil {
			return err
		}
	}

	// Apply project configuration.
	if config.Server.Projects != nil && len(config.Server.Projects) > 0 {
		// Get the list of projects.
		projectNames, err := r.GetProjectNames()
		if err != nil {
			return fmt.Errorf("Failed to retrieve list of projects: %w", err)
		}

		// Project creator.
		createProject := func(project api.ProjectsPost) error {
			// Create the project if doesn't exist.
			err := r.CreateProject(project)
			if err != nil {
				return fmt.Errorf("Failed to create local member project %q: %w", project.Name, err)
			}

			return nil
		}

		// Project updater.
		updateProject := func(target api.ProjectsPost) error {
			// Get the current project.
			project, etag, err := r.GetProject(target.Name)
			if err != nil {
				return fmt.Errorf("Failed to retrieve current project %q: %w", target.Name, err)
			}

			// Description override.
			if target.Description != "" {
				project.Description = target.Description
			}

			// Config overrides.
			for k, v := range target.Config {
				project.Config[k] = fmt.Sprintf("%v", v)
			}

			// Apply it.
			err = r.UpdateProject(target.Name, project.Writable(), etag)
			if err != nil {
				return fmt.Errorf("Failed to update local member project %q: %w", target.Name, err)
			}

			return nil
		}

		for _, project := range config.Server.Projects {
			// New project.
			if !slices.Contains(projectNames, project.Name) {
				err := createProject(project)
				if err != nil {
					return err
				}

				continue
			}

			// Existing project.
			err := updateProject(project)
			if err != nil {
				return err
			}
		}
	}

	// Apply networks in non-default projects after project config applied (so that their projects exist).
	for i := range config.Server.Networks {
		if config.Server.Networks[i].Project == api.ProjectDefaultName {
			continue
		}

		err := applyNetwork(config.Server.Networks[i])
		if err != nil {
			return err
		}
	}

	// Apply profile configuration.
	if config.Server.Profiles != nil && len(config.Server.Profiles) > 0 {
		// Get the list of profiles.
		profileNames, err := r.GetProfileNames()
		if err != nil {
			return fmt.Errorf("Failed to retrieve list of profiles: %w", err)
		}

		// Profile creator.
		createProfile := func(profile api.ProfilesPost) error {
			// Create the profile if doesn't exist.
			err := r.CreateProfile(profile)
			if err != nil {
				return fmt.Errorf("Failed to create profile %q: %w", profile.Name, err)
			}

			return nil
		}

		// Profile updater.
		updateProfile := func(target api.ProfilesPost) error {
			// Get the current profile.
			profile, etag, err := r.GetProfile(target.Name)
			if err != nil {
				return fmt.Errorf("Failed to retrieve current profile %q: %w", target.Name, err)
			}

			// Description override.
			if target.Description != "" {
				profile.Description = target.Description
			}

			// Config overrides.
			for k, v := range target.Config {
				profile.Config[k] = fmt.Sprintf("%v", v)
			}

			// Device overrides.
			for k, v := range target.Devices {
				// New device.
				_, ok := profile.Devices[k]
				if !ok {
					profile.Devices[k] = v
					continue
				}

				// Existing device.
				for configKey, configValue := range v {
					profile.Devices[k][configKey] = fmt.Sprintf("%v", configValue)
				}
			}

			// Apply it.
			err = r.UpdateProfile(target.Name, profile.Writable(), etag)
			if err != nil {
				return fmt.Errorf("Failed to update profile %q: %w", target.Name, err)
			}

			return nil
		}

		for _, profile := range config.Server.Profiles {
			// New profile.
			if !slices.Contains(profileNames, profile.Name) {
				err := createProfile(profile)
				if err != nil {
					return err
				}

				continue
			}

			// Existing profile.
			err := updateProfile(profile)
			if err != nil {
				return err
			}
		}
	}

	// Cluster configuration.
	if config.Cluster != nil && config.Cluster.Enabled {
		// Get the current cluster configuration
		currentCluster, etag, err := r.GetCluster()
		if err != nil {
			return fmt.Errorf("Failed to retrieve current cluster config: %w", err)
		}

		// Check if already enabled
		if !currentCluster.Enabled {
			// Configure the cluster
			op, err := r.UpdateCluster(config.Cluster.ClusterPut, etag)
			if err != nil {
				return fmt.Errorf("Failed to configure cluster: %w", err)
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf("Failed to configure cluster: %w", err)
			}
		}
	}

	return nil
}
