package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/lxc/incus/internal/instance"
	"github.com/spf13/cobra"
)

func (g *cmdGlobal) cmpImages(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseImageServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		images, _ := resource.server.GetImages()

		for _, image := range images {
			for _, alias := range image.Aliases {
				var name string

				if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
					name = alias.Name
				} else {
					name = fmt.Sprintf("%s:%s", resource.remote, alias.Name)
				}

				results = append(results, name)
			}
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(true)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
}

func (g *cmdGlobal) cmpInstanceAllKeys() ([]string, cobra.ShellCompDirective) {
	keys := []string{}
	for k := range instance.InstanceConfigKeysAny {
		keys = append(keys, k)
	}

	return keys, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpInstanceSnapshots(instanceName string) ([]string, cobra.ShellCompDirective) {
	resources, err := g.ParseServers(instanceName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	snapshots, err := client.GetInstanceSnapshotNames(instanceName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return snapshots, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpInstances(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		instances, _ := resource.server.GetInstanceNames("container")
		vms, _ := resource.server.GetInstanceNames("virtual-machine")
		instances = append(instances, vms...)

		for _, instance := range instances {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = instance
			} else {
				name = fmt.Sprintf("%s:%s", resource.remote, instance)
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
}

func (g *cmdGlobal) cmpInstanceNamesFromRemote(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		containers, _ := resource.server.GetInstanceNames("container")
		results = append(results, containers...)
		vms, _ := resource.server.GetInstanceNames("virtual-machine")
		results = append(results, vms...)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpNetworks(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		networks, err := resource.server.GetNetworks()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		for _, network := range networks {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = network.Name
			} else {
				name = fmt.Sprintf("%s:%s", resource.remote, network.Name)
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
}

func (g *cmdGlobal) cmpNetworkConfigs(networkName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(networkName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	network, _, err := client.GetNetwork(networkName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var results []string
	for k := range network.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpNetworkInstances(networkName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(networkName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	network, _, err := client.GetNetwork(networkName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var results []string
	for _, i := range network.UsedBy {
		r := regexp.MustCompile(`/1.0/instances/(.*)`)
		match := r.FindStringSubmatch(i)

		if len(match) == 2 {
			results = append(results, match[1])
		}
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpNetworkProfiles(networkName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(networkName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	network, _, err := client.GetNetwork(networkName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var results []string
	for _, i := range network.UsedBy {
		r := regexp.MustCompile(`/1.0/profiles/(.*)`)
		match := r.FindStringSubmatch(i)

		if len(match) == 2 {
			results = append(results, match[1])
		}
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpProfileConfigs(profileName string) ([]string, cobra.ShellCompDirective) {
	resources, err := g.ParseServers(profileName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	profile, _, err := client.GetProfile(resource.name)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var configs []string
	for c := range profile.Config {
		configs = append(configs, c)
	}

	return configs, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpProfileNamesFromRemote(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		profiles, _ := resource.server.GetProfileNames()
		results = append(results, profiles...)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpProfiles(toComplete string, includeRemotes bool) ([]string, cobra.ShellCompDirective) {
	results := []string{}
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		profiles, _ := resource.server.GetProfileNames()

		for _, profile := range profiles {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = profile
			} else {
				name = fmt.Sprintf("%s:%s", resource.remote, profile)
			}

			results = append(results, name)
		}
	}

	if includeRemotes && !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
}

func (g *cmdGlobal) cmpProjectConfigs(projectName string) ([]string, cobra.ShellCompDirective) {
	resources, err := g.ParseServers(projectName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	project, _, err := client.GetProject(resource.name)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var configs []string
	for c := range project.Config {
		configs = append(configs, c)
	}

	return configs, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpProjects(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		projects, err := resource.server.GetProjectNames()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		for _, project := range projects {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = project
			} else {
				name = fmt.Sprintf("%s:%s", resource.remote, project)
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
}

func (g *cmdGlobal) cmpRemotes(includeAll bool) ([]string, cobra.ShellCompDirective) {
	results := []string{}

	for remoteName, rc := range g.conf.Remotes {
		if !includeAll && rc.Protocol != "incus" && rc.Protocol != "" {
			continue
		}

		results = append(results, fmt.Sprintf("%s:", remoteName))
	}

	return results, cobra.ShellCompDirectiveNoSpace
}

func (g *cmdGlobal) cmpRemoteNames() ([]string, cobra.ShellCompDirective) {
	results := []string{}

	for remoteName := range g.conf.Remotes {
		results = append(results, remoteName)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpStoragePoolConfigs(poolName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	if strings.Contains(poolName, ":") {
		poolName = strings.Split(poolName, ":")[1]
	}

	pool, _, err := client.GetStoragePool(poolName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var results []string
	for k := range pool.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpStoragePoolWithVolume(toComplete string) ([]string, cobra.ShellCompDirective) {
	if !strings.Contains(toComplete, "/") {
		pools, compdir := g.cmpStoragePools(toComplete)
		if compdir == cobra.ShellCompDirectiveError {
			return nil, compdir
		}

		results := []string{}
		for _, pool := range pools {
			if strings.HasSuffix(pool, ":") {
				results = append(results, pool)
			} else {
				results = append(results, fmt.Sprintf("%s/", pool))
			}
		}

		return results, cobra.ShellCompDirectiveNoSpace
	}

	pool := strings.Split(toComplete, "/")[0]
	volumes, compdir := g.cmpStoragePoolVolumes(pool)
	if compdir == cobra.ShellCompDirectiveError {
		return nil, compdir
	}

	results := []string{}
	for _, volume := range volumes {
		results = append(results, fmt.Sprintf("%s/%s", pool, volume))
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpStoragePools(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		storagePools, _ := resource.server.GetStoragePoolNames()

		for _, storage := range storagePools {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = storage
			} else {
				name = fmt.Sprintf("%s:%s", resource.remote, storage)
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, _ := g.cmpRemotes(false)
		results = append(results, remotes...)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpStoragePoolVolumeConfigs(poolName string, volumeName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	var pool = poolName
	if strings.Contains(poolName, ":") {
		pool = strings.Split(poolName, ":")[1]
	}

	volName, volType := parseVolume("custom", volumeName)

	volume, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var results []string
	for k := range volume.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpStoragePoolVolumeInstances(poolName string, volumeName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	var pool = poolName
	if strings.Contains(poolName, ":") {
		pool = strings.Split(poolName, ":")[1]
	}

	volName, volType := parseVolume("custom", volumeName)

	volume, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var results []string
	for _, i := range volume.UsedBy {
		r := regexp.MustCompile(`/1.0/instances/(.*)`)
		match := r.FindStringSubmatch(i)

		if len(match) == 2 {
			results = append(results, match[1])
		}
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpStoragePoolVolumeProfiles(poolName string, volumeName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	var pool = poolName
	if strings.Contains(poolName, ":") {
		pool = strings.Split(poolName, ":")[1]
	}

	volName, volType := parseVolume("custom", volumeName)

	volume, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var results []string
	for _, i := range volume.UsedBy {
		r := regexp.MustCompile(`/1.0/profiles/(.*)`)
		match := r.FindStringSubmatch(i)

		if len(match) == 2 {
			results = append(results, match[1])
		}
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpStoragePoolVolumeSnapshots(poolName string, volumeName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	var pool = poolName
	if strings.Contains(poolName, ":") {
		pool = strings.Split(poolName, ":")[1]
	}

	volName, volType := parseVolume("custom", volumeName)

	snapshots, err := client.GetStoragePoolVolumeSnapshotNames(pool, volType, volName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return snapshots, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpStoragePoolVolumes(poolName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	var pool = poolName
	if strings.Contains(poolName, ":") {
		pool = strings.Split(poolName, ":")[1]
	}

	volumes, err := client.GetStoragePoolVolumeNames(pool)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return volumes, cobra.ShellCompDirectiveNoFileComp
}
