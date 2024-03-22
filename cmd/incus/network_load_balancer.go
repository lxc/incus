package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/termios"
	"github.com/lxc/incus/shared/util"
)

type cmdNetworkLoadBalancer struct {
	global     *cmdGlobal
	flagTarget string
}

func (c *cmdNetworkLoadBalancer) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("load-balancer")
	cmd.Short = i18n.G("Manage network load balancers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network load balancers"))

	// List.
	networkLoadBalancerListCmd := cmdNetworkLoadBalancerList{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerListCmd.Command())

	// Show.
	networkLoadBalancerShowCmd := cmdNetworkLoadBalancerShow{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerShowCmd.Command())

	// Create.
	networkLoadBalancerCreateCmd := cmdNetworkLoadBalancerCreate{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerCreateCmd.Command())

	// Get.
	networkLoadBalancerGetCmd := cmdNetworkLoadBalancerGet{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerGetCmd.Command())

	// Set.
	networkLoadBalancerSetCmd := cmdNetworkLoadBalancerSet{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerSetCmd.Command())

	// Unset.
	networkLoadBalancerUnsetCmd := cmdNetworkLoadBalancerUnset{global: c.global, networkLoadBalancer: c, networkLoadBalancerSet: &networkLoadBalancerSetCmd}
	cmd.AddCommand(networkLoadBalancerUnsetCmd.Command())

	// Edit.
	networkLoadBalancerEditCmd := cmdNetworkLoadBalancerEdit{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerEditCmd.Command())

	// Delete.
	networkLoadBalancerDeleteCmd := cmdNetworkLoadBalancerDelete{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerDeleteCmd.Command())

	// Backend.
	networkLoadBalancerBackendCmd := cmdNetworkLoadBalancerBackend{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerBackendCmd.Command())

	// Port.
	networkLoadBalancerPortCmd := cmdNetworkLoadBalancerPort{global: c.global, networkLoadBalancer: c}
	cmd.AddCommand(networkLoadBalancerPortCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkLoadBalancerList struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer

	flagFormat string
}

func (c *cmdNetworkLoadBalancerList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<network>"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network load balancers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available network load balancers"))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	loadBalancers, err := resource.server.GetNetworkLoadBalancers(resource.name)
	if err != nil {
		return err
	}

	clustered := resource.server.IsClustered()

	data := make([][]string, 0, len(loadBalancers))
	for _, loadBalancer := range loadBalancers {
		details := []string{
			loadBalancer.ListenAddress,
			loadBalancer.Description,
			fmt.Sprintf("%d", len(loadBalancer.Ports)),
		}

		if clustered {
			details = append(details, loadBalancer.Location)
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("LISTEN ADDRESS"),
		i18n.G("DESCRIPTION"),
		i18n.G("PORTS"),
	}

	if clustered {
		header = append(header, i18n.G("LOCATION"))
	}

	return cli.RenderTable(c.flagFormat, header, data, loadBalancers)
}

// Show.
type cmdNetworkLoadBalancerShow struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
}

func (c *cmdNetworkLoadBalancerShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<network> <listen_address>"))
	cmd.Short = i18n.G("Show network load balancer configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show network load balancer configurations"))
	cmd.RunE = c.Run

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing listen address"))
	}

	client := resource.server

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		client = client.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Show the network load balancer config.
	loadBalancer, _, err := client.GetNetworkLoadBalancer(resource.name, args[1])
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&loadBalancer)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Create.
type cmdNetworkLoadBalancerCreate struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
}

func (c *cmdNetworkLoadBalancerCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<network> <listen_address> [key=value...]"))
	cmd.Short = i18n.G("Create new network load balancers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new network load balancers"))
	cmd.RunE = c.Run

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	return cmd
}

func (c *cmdNetworkLoadBalancerCreate) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing listen address"))
	}

	// If stdin isn't a terminal, read yaml from it.
	var loadBalancerPut api.NetworkLoadBalancerPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &loadBalancerPut)
		if err != nil {
			return err
		}
	}

	if loadBalancerPut.Config == nil {
		loadBalancerPut.Config = map[string]string{}
	}

	// Get config filters from arguments.
	for i := 2; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key/value pair: %s"), args[i])
		}

		loadBalancerPut.Config[entry[0]] = entry[1]
	}

	// Create the network load balancer.
	loadBalancer := api.NetworkLoadBalancersPost{
		ListenAddress:          args[1],
		NetworkLoadBalancerPut: loadBalancerPut,
	}

	loadBalancer.Normalise()

	client := resource.server

	// If a target was specified, create the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		client = client.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	err = client.CreateNetworkLoadBalancer(resource.name, loadBalancer)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network load balancer %s created")+"\n", loadBalancer.ListenAddress)
	}

	return nil
}

// Get.
type cmdNetworkLoadBalancerGet struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer

	flagIsProperty bool
}

func (c *cmdNetworkLoadBalancerGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<network> <listen_address> <key>"))
	cmd.Short = i18n.G("Get values for network load balancer configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Get values for network load balancer configuration keys"))
	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network load balancer property"))
	return cmd
}

func (c *cmdNetworkLoadBalancerGet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing listen address"))
	}

	// Get the current config.
	loadBalancer, _, err := client.GetNetworkLoadBalancer(resource.name, args[1])
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := loadBalancer.Writable()
		res, err := getFieldByJsonTag(&w, args[2])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the load balancer %q: %v"), args[2], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		for k, v := range loadBalancer.Config {
			if k == args[2] {
				fmt.Printf("%s\n", v)
			}
		}
	}

	return nil
}

// Set.
type cmdNetworkLoadBalancerSet struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer

	flagIsProperty bool
}

func (c *cmdNetworkLoadBalancerSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<network> <listen_address> <key>=<value>..."))
	cmd.Short = i18n.G("Set network load balancer keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set network load balancer keys

For backward compatibility, a single configuration key may still be set with:
    incus network set [<remote>:]<network> <listen_address> <key> <value>`))
	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network load balancer property"))
	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerSet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, -1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing listen address"))
	}

	client := resource.server

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		client = client.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Get the current config.
	loadBalancer, etag, err := client.GetNetworkLoadBalancer(resource.name, args[1])
	if err != nil {
		return err
	}

	if loadBalancer.Config == nil {
		loadBalancer.Config = map[string]string{}
	}

	// Set the keys.
	keys, err := getConfig(args[2:]...)
	if err != nil {
		return err
	}

	writable := loadBalancer.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJsonTag(&writable, k)
				if err != nil {
					return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
			}
		}
	} else {
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	writable.Normalise()

	return client.UpdateNetworkLoadBalancer(resource.name, loadBalancer.ListenAddress, writable, etag)
}

// Unset.
type cmdNetworkLoadBalancerUnset struct {
	global                 *cmdGlobal
	networkLoadBalancer    *cmdNetworkLoadBalancer
	networkLoadBalancerSet *cmdNetworkLoadBalancerSet

	flagIsProperty bool
}

func (c *cmdNetworkLoadBalancerUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<network> <listen_address> <key>"))
	cmd.Short = i18n.G("Unset network load balancer configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset network load balancer keys"))
	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network load balancer property"))
	return cmd
}

func (c *cmdNetworkLoadBalancerUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	c.networkLoadBalancerSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.networkLoadBalancerSet.Run(cmd, args)
}

// Edit.
type cmdNetworkLoadBalancerEdit struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
}

func (c *cmdNetworkLoadBalancerEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<network> <listen_address>"))
	cmd.Short = i18n.G("Edit network load balancer configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit network load balancer configurations as YAML"))
	cmd.RunE = c.Run

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network load balancer.
### Any line starting with a '# will be ignored.
###
### A network load balancer consists of a set of target backends and port forwards for a listen address.
###
### An example would look like:
### listen_address: 192.0.2.1
### config:
###   target_address: 198.51.100.2
### description: test desc
### ports:
### - description: port forward
###   protocol: tcp
###   listen_port: 80,81,8080-8090
###   target_address: 198.51.100.3
###   target_port: 80,81,8080-8090
### location: server01
###
### Note that the listen_address and location cannot be changed.`)
}

func (c *cmdNetworkLoadBalancerEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing listen address"))
	}

	client := resource.server

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		client = client.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `incus network load-balancer show` command to be passed in here, but only take the
		// contents of the NetworkLoadBalancerPut fields when updating.
		// The other fields are silently discarded.
		newData := api.NetworkLoadBalancer{}
		err = yaml.UnmarshalStrict(contents, &newData)
		if err != nil {
			return err
		}

		newData.Normalise()

		return client.UpdateNetworkLoadBalancer(resource.name, args[1], newData.NetworkLoadBalancerPut, "")
	}

	// Get the current config.
	loadBalancer, etag, err := client.GetNetworkLoadBalancer(resource.name, args[1])
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&loadBalancer)
	if err != nil {
		return err
	}

	// Spawn the editor.
	content, err := textEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor.
		newData := api.NetworkLoadBalancer{} // We show the full info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newData)
		if err == nil {
			newData.Normalise()
			err = client.UpdateNetworkLoadBalancer(resource.name, args[1], newData.Writable(), etag)
		}

		// Respawn the editor.
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = textEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// Delete.
type cmdNetworkLoadBalancerDelete struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
}

func (c *cmdNetworkLoadBalancerDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<network> <listen_address>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network load balancers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete network load balancers"))
	cmd.RunE = c.Run

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing listen address"))
	}

	client := resource.server

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		client = client.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Delete the network load balancer.
	err = client.DeleteNetworkLoadBalancer(resource.name, args[1])
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network load balancer %s deleted")+"\n", args[1])
	}

	return nil
}

// Add/Remove Backend.
type cmdNetworkLoadBalancerBackend struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
}

func (c *cmdNetworkLoadBalancerBackend) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("backend")
	cmd.Short = i18n.G("Manage network load balancer backends")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network load balancer backends"))

	// Backend Add.
	cmd.AddCommand(c.CommandAdd())

	// Backend Remove.
	cmd.AddCommand(c.CommandRemove())

	return cmd
}

func (c *cmdNetworkLoadBalancerBackend) CommandAdd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<network> <listen_address> <backend_name> <target_address> [<target_port(s)>]"))
	cmd.Short = i18n.G("Add backends to a load balancer")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Add backend to a load balancer"))
	cmd.RunE = c.RunAdd

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerBackend) RunAdd(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 4, 5)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing listen address"))
	}

	client := resource.server

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		client = client.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Get the network load balancer.
	loadBalancer, etag, err := client.GetNetworkLoadBalancer(resource.name, args[1])
	if err != nil {
		return err
	}

	backend := api.NetworkLoadBalancerBackend{
		Name:          args[2],
		TargetAddress: args[3],
	}

	if len(args) >= 5 {
		backend.TargetPort = args[4]
	}

	loadBalancer.Backends = append(loadBalancer.Backends, backend)

	loadBalancer.Normalise()

	return client.UpdateNetworkLoadBalancer(resource.name, loadBalancer.ListenAddress, loadBalancer.Writable(), etag)
}

func (c *cmdNetworkLoadBalancerBackend) CommandRemove() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<network> <listen_address> <backend_name>"))
	cmd.Short = i18n.G("Remove backends from a load balancer")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Remove backend from a load balancer"))
	cmd.RunE = c.RunRemove

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerBackend) RunRemove(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing listen address"))
	}

	client := resource.server

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		client = client.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Get the network load balancer.
	loadBalancer, etag, err := client.GetNetworkLoadBalancer(resource.name, args[1])
	if err != nil {
		return err
	}

	// removeBackend removes a single backend that matches the filterArgs supplied.
	removeBackend := func(backends []api.NetworkLoadBalancerBackend, removeName string) ([]api.NetworkLoadBalancerBackend, error) {
		removed := false
		newBackends := make([]api.NetworkLoadBalancerBackend, 0, len(backends))

		for _, backend := range backends {
			if backend.Name == removeName {
				removed = true
				continue // Don't add removed backend to newBackends.
			}

			newBackends = append(newBackends, backend)
		}

		if !removed {
			return nil, fmt.Errorf(i18n.G("No matching backend found"))
		}

		return newBackends, nil
	}

	loadBalancer.Backends, err = removeBackend(loadBalancer.Backends, args[2])
	if err != nil {
		return err
	}

	loadBalancer.Normalise()

	return client.UpdateNetworkLoadBalancer(resource.name, loadBalancer.ListenAddress, loadBalancer.Writable(), etag)
}

// Add/Remove Port.
type cmdNetworkLoadBalancerPort struct {
	global              *cmdGlobal
	networkLoadBalancer *cmdNetworkLoadBalancer
	flagRemoveForce     bool
}

func (c *cmdNetworkLoadBalancerPort) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("port")
	cmd.Short = i18n.G("Manage network load balancer ports")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network load balancer ports"))

	// Port Add.
	cmd.AddCommand(c.CommandAdd())

	// Port Remove.
	cmd.AddCommand(c.CommandRemove())

	return cmd
}

func (c *cmdNetworkLoadBalancerPort) CommandAdd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<network> <listen_address> <protocol> <listen_port(s)> <backend_name>[,<backend_name>...]"))
	cmd.Short = i18n.G("Add ports to a load balancer")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Add ports to a load balancer"))
	cmd.RunE = c.RunAdd

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerPort) RunAdd(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 5, 5)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing listen address"))
	}

	client := resource.server

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		client = client.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Get the network load balancer.
	loadBalancer, etag, err := client.GetNetworkLoadBalancer(resource.name, args[1])
	if err != nil {
		return err
	}

	port := api.NetworkLoadBalancerPort{
		Protocol:      args[2],
		ListenPort:    args[3],
		TargetBackend: util.SplitNTrimSpace(args[4], ",", -1, false),
	}

	loadBalancer.Ports = append(loadBalancer.Ports, port)

	loadBalancer.Normalise()

	return client.UpdateNetworkLoadBalancer(resource.name, loadBalancer.ListenAddress, loadBalancer.Writable(), etag)
}

func (c *cmdNetworkLoadBalancerPort) CommandRemove() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<network> <listen_address> [<protocol>] [<listen_port(s)>]"))
	cmd.Short = i18n.G("Remove ports from a load balancer")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Remove ports from a load balancer"))
	cmd.Flags().BoolVar(&c.flagRemoveForce, "force", false, i18n.G("Remove all ports that match"))
	cmd.RunE = c.RunRemove

	cmd.Flags().StringVar(&c.networkLoadBalancer.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpNetworks(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkLoadBalancers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkLoadBalancerPort) RunRemove(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 4)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing listen address"))
	}

	client := resource.server

	// If a target was specified, use the load balancer on the given member.
	if c.networkLoadBalancer.flagTarget != "" {
		client = client.UseTarget(c.networkLoadBalancer.flagTarget)
	}

	// Get the network load balancer.
	loadBalancer, etag, err := client.GetNetworkLoadBalancer(resource.name, args[1])
	if err != nil {
		return err
	}

	// isFilterMatch returns whether the supplied port has matching field values in the filterArgs supplied.
	// If no filterArgs are supplied, then the rule is considered to have matched.
	isFilterMatch := func(port *api.NetworkLoadBalancerPort, filterArgs []string) bool {
		switch len(filterArgs) {
		case 3:
			if port.ListenPort != filterArgs[2] {
				return false
			}

			fallthrough
		case 2:
			if port.Protocol != filterArgs[1] {
				return false
			}
		}

		return true // Match found as all struct fields match the supplied filter values.
	}

	// removeFromRules removes a single port that matches the filterArgs supplied. If multiple ports match then
	// an error is returned unless c.flagRemoveForce is true, in which case all matching ports are removed.
	removeFromRules := func(ports []api.NetworkLoadBalancerPort, filterArgs []string) ([]api.NetworkLoadBalancerPort, error) {
		removed := false
		newPorts := make([]api.NetworkLoadBalancerPort, 0, len(ports))

		for _, port := range ports {
			if isFilterMatch(&port, filterArgs) {
				if removed && !c.flagRemoveForce {
					return nil, fmt.Errorf(i18n.G("Multiple ports match. Use --force to remove them all"))
				}

				removed = true
				continue // Don't add removed port to newPorts.
			}

			newPorts = append(newPorts, port)
		}

		if !removed {
			return nil, fmt.Errorf(i18n.G("No matching port(s) found"))
		}

		return newPorts, nil
	}

	loadBalancer.Ports, err = removeFromRules(loadBalancer.Ports, args[1:])
	if err != nil {
		return err
	}

	loadBalancer.Normalise()

	return client.UpdateNetworkLoadBalancer(resource.name, loadBalancer.ListenAddress, loadBalancer.Writable(), etag)
}
