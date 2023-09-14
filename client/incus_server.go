package incus

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/shared"
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
		server.AuthMethods = []string{"tls"}
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
func (r *ProtocolIncus) HasExtension(extension string) bool {
	// If no cached API information, just assume we're good
	// This is needed for those rare cases where we must avoid a GetServer call
	if r.server == nil {
		return true
	}

	for _, entry := range r.server.APIExtensions {
		if entry == extension {
			return true
		}
	}

	return false
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
		currentServer, etag, err := r.GetServer()
		if err != nil {
			return fmt.Errorf("Failed to retrieve current server configuration: %w", err)
		}

		// Prepare the update.
		newServer := api.ServerPut{}
		err = shared.DeepCopy(currentServer.Writable(), &newServer)
		if err != nil {
			return fmt.Errorf("Failed to copy server configuration: %w", err)
		}

		for k, v := range config.Server.Config {
			newServer.Config[k] = fmt.Sprintf("%v", v)
		}

		// Apply it.
		err = r.UpdateServer(newServer, etag)
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
		updateStoragePool := func(storagePool api.StoragePoolsPost) error {
			// Get the current storagePool.
			currentStoragePool, etag, err := r.GetStoragePool(storagePool.Name)
			if err != nil {
				return fmt.Errorf("Failed to retrieve current storage pool %q: %w", storagePool.Name, err)
			}

			// Quick check.
			if currentStoragePool.Driver != storagePool.Driver {
				return fmt.Errorf("Storage pool %q is of type %q instead of %q", currentStoragePool.Name, currentStoragePool.Driver, storagePool.Driver)
			}

			// Prepare the update.
			newStoragePool := api.StoragePoolPut{}
			err = shared.DeepCopy(currentStoragePool.Writable(), &newStoragePool)
			if err != nil {
				return fmt.Errorf("Failed to copy configuration of storage pool %q: %w", storagePool.Name, err)
			}

			// Description override.
			if storagePool.Description != "" {
				newStoragePool.Description = storagePool.Description
			}

			// Config overrides.
			for k, v := range storagePool.Config {
				newStoragePool.Config[k] = fmt.Sprintf("%v", v)
			}

			// Apply it.
			err = r.UpdateStoragePool(currentStoragePool.Name, newStoragePool, etag)
			if err != nil {
				return fmt.Errorf("Failed to update storage pool %q: %w", storagePool.Name, err)
			}

			return nil
		}

		for _, storagePool := range config.Server.StoragePools {
			// New storagePool.
			if !shared.StringInSlice(storagePool.Name, storagePoolNames) {
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
	applyNetwork := func(network api.InitNetworksProjectPost) error {
		currentNetwork, etag, err := r.UseProject(network.Project).GetNetwork(network.Name)
		if err != nil {
			// Create the network if doesn't exist.
			err := r.UseProject(network.Project).CreateNetwork(network.NetworksPost)
			if err != nil {
				return fmt.Errorf("Failed to create local member network %q in project %q: %w", network.Name, network.Project, err)
			}
		} else {
			// Prepare the update.
			newNetwork := api.NetworkPut{}
			err = shared.DeepCopy(currentNetwork.Writable(), &newNetwork)
			if err != nil {
				return fmt.Errorf("Failed to copy configuration of network %q in project %q: %w", network.Name, network.Project, err)
			}

			// Description override.
			if network.Description != "" {
				newNetwork.Description = network.Description
			}

			// Config overrides.
			for k, v := range network.Config {
				newNetwork.Config[k] = fmt.Sprintf("%v", v)
			}

			// Apply it.
			err = r.UseProject(network.Project).UpdateNetwork(currentNetwork.Name, newNetwork, etag)
			if err != nil {
				return fmt.Errorf("Failed to update local member network %q in project %q: %w", network.Name, network.Project, err)
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
			config.Server.Networks[i].Project = "default"
		}

		if config.Server.Networks[i].Project != "default" {
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
		updateProject := func(project api.ProjectsPost) error {
			// Get the current project.
			currentProject, etag, err := r.GetProject(project.Name)
			if err != nil {
				return fmt.Errorf("Failed to retrieve current project %q: %w", project.Name, err)
			}

			// Prepare the update.
			newProject := api.ProjectPut{}
			err = shared.DeepCopy(currentProject.Writable(), &newProject)
			if err != nil {
				return fmt.Errorf("Failed to copy configuration of project %q: %w", project.Name, err)
			}

			// Description override.
			if project.Description != "" {
				newProject.Description = project.Description
			}

			// Config overrides.
			for k, v := range project.Config {
				newProject.Config[k] = fmt.Sprintf("%v", v)
			}

			// Apply it.
			err = r.UpdateProject(currentProject.Name, newProject, etag)
			if err != nil {
				return fmt.Errorf("Failed to update local member project %q: %w", project.Name, err)
			}

			return nil
		}

		for _, project := range config.Server.Projects {
			// New project.
			if !shared.StringInSlice(project.Name, projectNames) {
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
		if config.Server.Networks[i].Project == "default" {
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
		updateProfile := func(profile api.ProfilesPost) error {
			// Get the current profile.
			currentProfile, etag, err := r.GetProfile(profile.Name)
			if err != nil {
				return fmt.Errorf("Failed to retrieve current profile %q: %w", profile.Name, err)
			}

			// Prepare the update.
			newProfile := api.ProfilePut{}
			err = shared.DeepCopy(currentProfile.Writable(), &newProfile)
			if err != nil {
				return fmt.Errorf("Failed to copy configuration of profile %q: %w", profile.Name, err)
			}

			// Description override.
			if profile.Description != "" {
				newProfile.Description = profile.Description
			}

			// Config overrides.
			for k, v := range profile.Config {
				newProfile.Config[k] = fmt.Sprintf("%v", v)
			}

			// Device overrides.
			for k, v := range profile.Devices {
				// New device.
				_, ok := newProfile.Devices[k]
				if !ok {
					newProfile.Devices[k] = v
					continue
				}

				// Existing device.
				for configKey, configValue := range v {
					newProfile.Devices[k][configKey] = fmt.Sprintf("%v", configValue)
				}
			}

			// Apply it.
			err = r.UpdateProfile(currentProfile.Name, newProfile, etag)
			if err != nil {
				return fmt.Errorf("Failed to update profile %q: %w", profile.Name, err)
			}

			return nil
		}

		for _, profile := range config.Server.Profiles {
			// New profile.
			if !shared.StringInSlice(profile.Name, profileNames) {
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
