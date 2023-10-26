//go:build linux

package main

import (
	"fmt"

	yaml "gopkg.in/yaml.v2"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/shared/api"
)

func (c *cmdAdminInit) RunDump(d incus.InstanceServer) error {
	currentServer, _, err := d.GetServer()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to retrieve current server configuration: %w"), err)
	}

	var config api.InitLocalPreseed
	config.Config = currentServer.Config

	// Only retrieve networks in the default project as the preseed format doesn't support creating
	// projects at this time.
	networks, err := d.UseProject(api.ProjectDefaultName).GetNetworks()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to retrieve current server network configuration for project %q: %w"), api.ProjectDefaultName, err)
	}

	for _, network := range networks {
		// Only list managed networks.
		if !network.Managed {
			continue
		}

		networksPost := api.InitNetworksProjectPost{}
		networksPost.Config = network.Config
		networksPost.Description = network.Description
		networksPost.Name = network.Name
		networksPost.Type = network.Type
		networksPost.Project = api.ProjectDefaultName

		config.Networks = append(config.Networks, networksPost)
	}

	storagePools, err := d.GetStoragePools()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to retrieve current server configuration: %w"), err)
	}

	for _, storagePool := range storagePools {
		storagePoolsPost := api.StoragePoolsPost{}
		storagePoolsPost.Config = storagePool.Config
		storagePoolsPost.Description = storagePool.Description
		storagePoolsPost.Name = storagePool.Name
		storagePoolsPost.Driver = storagePool.Driver

		config.StoragePools = append(config.StoragePools, storagePoolsPost)
	}

	profiles, err := d.GetProfiles()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to retrieve current server configuration: %w"), err)
	}

	for _, profile := range profiles {
		profilesPost := api.ProfilesPost{}
		profilesPost.Config = profile.Config
		profilesPost.Description = profile.Description
		profilesPost.Devices = profile.Devices
		profilesPost.Name = profile.Name

		config.Profiles = append(config.Profiles, profilesPost)
	}

	projects, err := d.GetProjects()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to retrieve current server configuration: %w"), err)
	}

	for _, project := range projects {
		projectsPost := api.ProjectsPost{}
		projectsPost.Config = project.Config
		projectsPost.Description = project.Description
		projectsPost.Name = project.Name

		config.Projects = append(config.Projects, projectsPost)
	}

	out, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to retrieve current server configuration: %w"), err)
	}

	fmt.Printf("%s\n", out)

	return nil
}
