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
	var remote string

	if strings.Contains(toComplete, ":") {
		remote = strings.Split(toComplete, ":")[0]
	} else {
		remote = g.conf.DefaultRemote
	}

	remoteServer, _ := g.conf.GetImageServer(remote)

	images, _ := remoteServer.GetImages()

	for _, image := range images {
		for _, alias := range image.Aliases {
			var name string

			if remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = alias.Name
			} else {
				name = fmt.Sprintf("%s:%s", remote, alias.Name)
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, _ := g.cmpRemotes(true)
		results = append(results, remotes...)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpInstanceAllKeys() ([]string, cobra.ShellCompDirective) {
	keys := []string{}
	for k := range instance.InstanceConfigKeysAny {
		keys = append(keys, k)
	}

	return keys, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpInstances(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}

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
		remotes, _ := g.cmpRemotes(false)
		results = append(results, remotes...)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

func (g *cmdGlobal) cmpNetworks(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}

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
		remotes, _ := g.cmpRemotes(false)
		results = append(results, remotes...)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
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

	return results, cobra.ShellCompDirectiveError
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

	return results, cobra.ShellCompDirectiveError
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

	return results, cobra.ShellCompDirectiveError
}

func (g *cmdGlobal) cmpProfiles(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}

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

	if !strings.Contains(toComplete, ":") {
		remotes, _ := g.cmpRemotes(false)
		results = append(results, remotes...)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
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
