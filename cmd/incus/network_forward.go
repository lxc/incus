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
)

type cmdNetworkForward struct {
	global     *cmdGlobal
	flagTarget string
}

func (c *cmdNetworkForward) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("forward")
	cmd.Short = i18n.G("Manage network forwards")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network forwards"))

	// List.
	networkForwardListCmd := cmdNetworkForwardList{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardListCmd.Command())

	// Show.
	networkForwardShowCmd := cmdNetworkForwardShow{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardShowCmd.Command())

	// Create.
	networkForwardCreateCmd := cmdNetworkForwardCreate{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardCreateCmd.Command())

	// Get.
	networkForwardGetCmd := cmdNetworkForwardGet{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardGetCmd.Command())

	// Set.
	networkForwardSetCmd := cmdNetworkForwardSet{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardSetCmd.Command())

	// Unset.
	networkForwardUnsetCmd := cmdNetworkForwardUnset{global: c.global, networkForward: c, networkForwardSet: &networkForwardSetCmd}
	cmd.AddCommand(networkForwardUnsetCmd.Command())

	// Edit.
	networkForwardEditCmd := cmdNetworkForwardEdit{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardEditCmd.Command())

	// Delete.
	networkForwardDeleteCmd := cmdNetworkForwardDelete{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardDeleteCmd.Command())

	// Port.
	networkForwardPortCmd := cmdNetworkForwardPort{global: c.global, networkForward: c}
	cmd.AddCommand(networkForwardPortCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkForwardList struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward

	flagFormat string
}

func (c *cmdNetworkForwardList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<network>"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network forwards")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available network forwards"))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

func (c *cmdNetworkForwardList) Run(cmd *cobra.Command, args []string) error {
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

	forwards, err := resource.server.GetNetworkForwards(resource.name)
	if err != nil {
		return err
	}

	clustered := resource.server.IsClustered()

	data := make([][]string, 0, len(forwards))
	for _, forward := range forwards {
		details := []string{
			forward.ListenAddress,
			forward.Description,
			forward.Config["target_address"],
			fmt.Sprintf("%d", len(forward.Ports)),
		}

		if clustered {
			details = append(details, forward.Location)
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("LISTEN ADDRESS"),
		i18n.G("DESCRIPTION"),
		i18n.G("DEFAULT TARGET ADDRESS"),
		i18n.G("PORTS"),
	}

	if clustered {
		header = append(header, i18n.G("LOCATION"))
	}

	return cli.RenderTable(c.flagFormat, header, data, forwards)
}

// Show.
type cmdNetworkForwardShow struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward
}

func (c *cmdNetworkForwardShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<network> <listen_address>"))
	cmd.Short = i18n.G("Show network forward configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show network forward configurations"))
	cmd.RunE = c.Run

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	return cmd
}

func (c *cmdNetworkForwardShow) Run(cmd *cobra.Command, args []string) error {
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

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		client = client.UseTarget(c.networkForward.flagTarget)
	}

	// Show the network forward config.
	forward, _, err := client.GetNetworkForward(resource.name, args[1])
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&forward)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Create.
type cmdNetworkForwardCreate struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward
}

func (c *cmdNetworkForwardCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<network> <listen_address> [key=value...]"))
	cmd.Short = i18n.G("Create new network forwards")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new network forwards"))
	cmd.RunE = c.Run

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	return cmd
}

func (c *cmdNetworkForwardCreate) Run(cmd *cobra.Command, args []string) error {
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
	var forwardPut api.NetworkForwardPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &forwardPut)
		if err != nil {
			return err
		}
	}

	if forwardPut.Config == nil {
		forwardPut.Config = map[string]string{}
	}

	// Get config filters from arguments.
	for i := 2; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key/value pair: %s"), args[i])
		}

		forwardPut.Config[entry[0]] = entry[1]
	}

	// Create the network forward.
	forward := api.NetworkForwardsPost{
		ListenAddress:     args[1],
		NetworkForwardPut: forwardPut,
	}

	forward.Normalise()

	client := resource.server

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		client = client.UseTarget(c.networkForward.flagTarget)
	}

	err = client.CreateNetworkForward(resource.name, forward)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network forward %s created")+"\n", forward.ListenAddress)
	}

	return nil
}

// Get.
type cmdNetworkForwardGet struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward

	flagIsProperty bool
}

func (c *cmdNetworkForwardGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<network> <listen_address> <key>"))
	cmd.Short = i18n.G("Get values for network forward configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Get values for network forward configuration keys"))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network forward property"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkForwardGet) Run(cmd *cobra.Command, args []string) error {
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
	forward, _, err := client.GetNetworkForward(resource.name, args[1])
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := forward.Writable()
		res, err := getFieldByJsonTag(&w, args[2])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the network forward %q: %v"), args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		for k, v := range forward.Config {
			if k == args[2] {
				fmt.Printf("%s\n", v)
			}
		}
	}

	return nil
}

// Set.
type cmdNetworkForwardSet struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward

	flagIsProperty bool
}

func (c *cmdNetworkForwardSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<network> <listen_address> <key>=<value>..."))
	cmd.Short = i18n.G("Set network forward keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set network forward keys

For backward compatibility, a single configuration key may still be set with:
    incus network set [<remote>:]<network> <listen_address> <key> <value>`))
	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network forward property"))
	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	return cmd
}

func (c *cmdNetworkForwardSet) Run(cmd *cobra.Command, args []string) error {
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

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		client = client.UseTarget(c.networkForward.flagTarget)
	}

	// Get the current config.
	forward, etag, err := client.GetNetworkForward(resource.name, args[1])
	if err != nil {
		return err
	}

	if forward.Config == nil {
		forward.Config = map[string]string{}
	}

	// Set the keys.
	keys, err := getConfig(args[2:]...)
	if err != nil {
		return err
	}

	writable := forward.Writable()
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

	return client.UpdateNetworkForward(resource.name, forward.ListenAddress, writable, etag)
}

// Unset.
type cmdNetworkForwardUnset struct {
	global            *cmdGlobal
	networkForward    *cmdNetworkForward
	networkForwardSet *cmdNetworkForwardSet

	flagIsProperty bool
}

func (c *cmdNetworkForwardUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<network> <listen_address> <key>"))
	cmd.Short = i18n.G("Unset network forward configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset network forward keys"))
	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network forward property"))
	return cmd
}

func (c *cmdNetworkForwardUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	c.networkForwardSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.networkForwardSet.Run(cmd, args)
}

// Edit.
type cmdNetworkForwardEdit struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward
}

func (c *cmdNetworkForwardEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<network> <listen_address>"))
	cmd.Short = i18n.G("Edit network forward configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit network forward configurations as YAML"))
	cmd.RunE = c.Run

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	return cmd
}

func (c *cmdNetworkForwardEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network forward.
### Any line starting with a '# will be ignored.
###
### A network forward consists of a default target address and optional set of port forwards for a listen address.
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

func (c *cmdNetworkForwardEdit) Run(cmd *cobra.Command, args []string) error {
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

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		client = client.UseTarget(c.networkForward.flagTarget)
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `incus network forward show` command to be passed in here, but only take the
		// contents of the NetworkForwardPut fields when updating. The other fields are silently discarded.
		newData := api.NetworkForward{}
		err = yaml.UnmarshalStrict(contents, &newData)
		if err != nil {
			return err
		}

		newData.Normalise()

		return client.UpdateNetworkForward(resource.name, args[1], newData.NetworkForwardPut, "")
	}

	// Get the current config.
	forward, etag, err := client.GetNetworkForward(resource.name, args[1])
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&forward)
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
		newData := api.NetworkForward{} // We show the full info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newData)
		if err == nil {
			newData.Normalise()
			err = client.UpdateNetworkForward(resource.name, args[1], newData.Writable(), etag)
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
type cmdNetworkForwardDelete struct {
	global         *cmdGlobal
	networkForward *cmdNetworkForward
}

func (c *cmdNetworkForwardDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<network> <listen_address>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network forwards")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete network forwards"))
	cmd.RunE = c.Run

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	return cmd
}

func (c *cmdNetworkForwardDelete) Run(cmd *cobra.Command, args []string) error {
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

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		client = client.UseTarget(c.networkForward.flagTarget)
	}

	// Delete the network forward.
	err = client.DeleteNetworkForward(resource.name, args[1])
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network forward %s deleted")+"\n", args[1])
	}

	return nil
}

// Add/Remove Port.
type cmdNetworkForwardPort struct {
	global          *cmdGlobal
	networkForward  *cmdNetworkForward
	flagRemoveForce bool
}

func (c *cmdNetworkForwardPort) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("port")
	cmd.Short = i18n.G("Manage network forward ports")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network forward ports"))

	// Port Add.
	cmd.AddCommand(c.CommandAdd())

	// Port Remove.
	cmd.AddCommand(c.CommandRemove())

	return cmd
}

func (c *cmdNetworkForwardPort) CommandAdd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<network> <listen_address> <protocol> <listen_port(s)> <target_address> [<target_port(s)>]"))
	cmd.Short = i18n.G("Add ports to a forward")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Add ports to a forward"))
	cmd.RunE = c.RunAdd

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	return cmd
}

func (c *cmdNetworkForwardPort) RunAdd(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 5, 6)
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

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		client = client.UseTarget(c.networkForward.flagTarget)
	}

	// Get the network forward.
	forward, etag, err := client.GetNetworkForward(resource.name, args[1])
	if err != nil {
		return err
	}

	port := api.NetworkForwardPort{
		Protocol:      args[2],
		ListenPort:    args[3],
		TargetAddress: args[4],
	}

	if len(args) > 5 {
		port.TargetPort = args[5]
	}

	forward.Ports = append(forward.Ports, port)

	forward.Normalise()

	return client.UpdateNetworkForward(resource.name, forward.ListenAddress, forward.Writable(), etag)
}

func (c *cmdNetworkForwardPort) CommandRemove() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<network> <listen_address> [<protocol>] [<listen_port(s)>]"))
	cmd.Short = i18n.G("Remove ports from a forward")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Remove ports from a forward"))
	cmd.Flags().BoolVar(&c.flagRemoveForce, "force", false, i18n.G("Remove all ports that match"))
	cmd.RunE = c.RunRemove

	cmd.Flags().StringVar(&c.networkForward.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	return cmd
}

func (c *cmdNetworkForwardPort) RunRemove(cmd *cobra.Command, args []string) error {
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

	// If a target was specified, create the forward on the given member.
	if c.networkForward.flagTarget != "" {
		client = client.UseTarget(c.networkForward.flagTarget)
	}

	// Get the network forward.
	forward, etag, err := client.GetNetworkForward(resource.name, args[1])
	if err != nil {
		return err
	}

	// isFilterMatch returns whether the supplied port has matching field values in the filterArgs supplied.
	// If no filterArgs are supplied, then the rule is considered to have matched.
	isFilterMatch := func(port *api.NetworkForwardPort, filterArgs []string) bool {
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
	removeFromRules := func(ports []api.NetworkForwardPort, filterArgs []string) ([]api.NetworkForwardPort, error) {
		removed := false
		newPorts := make([]api.NetworkForwardPort, 0, len(ports))

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

	ports, err := removeFromRules(forward.Ports, args[1:])
	if err != nil {
		return err
	}

	forward.Ports = ports

	forward.Normalise()

	return client.UpdateNetworkForward(resource.name, forward.ListenAddress, forward.Writable(), etag)
}
