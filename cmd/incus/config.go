package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/termios"
)

type cmdConfig struct {
	global *cmdGlobal

	flagTarget string
}

func (c *cmdConfig) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("config")
	cmd.Short = i18n.G("Manage instance and server configuration options")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Manage instance and server configuration options`))

	// Device
	configDeviceCmd := cmdConfigDevice{global: c.global, config: c}
	cmd.AddCommand(configDeviceCmd.command())

	// Edit
	configEditCmd := cmdConfigEdit{global: c.global, config: c}
	cmd.AddCommand(configEditCmd.command())

	// Get
	configGetCmd := cmdConfigGet{global: c.global, config: c}
	cmd.AddCommand(configGetCmd.command())

	// Metadata
	configMetadataCmd := cmdConfigMetadata{global: c.global, config: c}
	cmd.AddCommand(configMetadataCmd.command())

	// Profile
	configProfileCmd := cmdProfile{global: c.global}
	profileCmd := configProfileCmd.command()
	profileCmd.Hidden = true
	profileCmd.Deprecated = i18n.G("please use `incus profile`")
	cmd.AddCommand(profileCmd)

	// Set
	configSetCmd := cmdConfigSet{global: c.global, config: c}
	cmd.AddCommand(configSetCmd.command())

	// Show
	configShowCmd := cmdConfigShow{global: c.global, config: c}
	cmd.AddCommand(configShowCmd.command())

	// Template
	configTemplateCmd := cmdConfigTemplate{global: c.global, config: c}
	cmd.AddCommand(configTemplateCmd.command())

	// Trust
	configTrustCmd := cmdConfigTrust{global: c.global, config: c}
	cmd.AddCommand(configTrustCmd.command())

	// Unset
	configUnsetCmd := cmdConfigUnset{global: c.global, config: c, configSet: &configSetCmd}
	cmd.AddCommand(configUnsetCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Edit.
type cmdConfigEdit struct {
	global *cmdGlobal
	config *cmdConfig
}

var cmdConfigEditUsage = u.Usage{u.MakePath(u.Instance, u.Snapshot.Optional()).Optional().Remote()}

func (c *cmdConfigEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdConfigEditUsage...)
	cmd.Short = i18n.G("Edit instance or server configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Edit instance or server configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus config edit <instance> < instance.yaml
    Update the instance configuration from config.yaml.`))

	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// helpTemplate returns a sample YAML configuration and guidelines for editing instance configurations.
func (c *cmdConfigEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the configuration.
### Any line starting with a '# will be ignored.
###
### A sample configuration looks like:
### name: instance1
### profiles:
### - default
### config:
###   volatile.eth0.hwaddr: 10:66:6a:e9:f8:7f
### devices:
###   homedir:
###     path: /extra
###     source: /home/user
###     type: disk
### ephemeral: false
###
### Note that the name is shown but cannot be changed`)
}

func (c *cmdConfigEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdConfigEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	parsedPath := parsed[0].RemoteObject

	// Edit the config
	if !parsedPath.Skipped {
		fields := parsedPath.StringList
		isSnapshot := !parsedPath.List[1].Skipped

		// Quick checks.
		if c.config.flagTarget != "" {
			return errors.New(i18n.G("--target cannot be used with instances"))
		}

		// If stdin isn't a terminal, read text from it
		if !termios.IsTerminal(getStdinFd()) {
			loader, err := yaml.NewLoader(os.Stdin)
			if err != nil {
				return err
			}

			var op incus.Operation

			if isSnapshot {
				newdata := api.InstanceSnapshotPut{}

				err = loader.Load(&newdata)
				if err != nil && !errors.Is(err, io.EOF) {
					return err
				}

				op, err = d.UpdateInstanceSnapshot(fields[0], fields[1], newdata, "")
				if err != nil {
					return err
				}
			} else {
				newdata := api.InstancePut{}
				err = loader.Load(&newdata)
				if err != nil && !errors.Is(err, io.EOF) {
					return err
				}

				op, err = d.UpdateInstance(parsedPath.String, newdata, "")
				if err != nil {
					return err
				}
			}

			return op.Wait()
		}

		var data []byte
		var etag string

		// Extract the current value
		if isSnapshot {
			var inst *api.InstanceSnapshot

			inst, etag, err = d.GetInstanceSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			// Empty expanded config so it isn't shown in edit screen (relies on omitempty tag).
			inst.ExpandedConfig = nil
			inst.ExpandedDevices = nil

			data, err = yaml.Dump(&inst, yaml.V2)
			if err != nil {
				return err
			}
		} else {
			var inst *api.Instance

			inst, etag, err = d.GetInstance(parsedPath.String)
			if err != nil {
				return err
			}

			// Empty expanded config so it isn't shown in edit screen (relies on omitempty tag).
			inst.ExpandedConfig = nil
			inst.ExpandedDevices = nil

			data, err = yaml.Dump(&inst, yaml.V2)
			if err != nil {
				return err
			}
		}

		// Spawn the editor
		content, err := cli.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
		if err != nil {
			return err
		}

		for {
			// Parse the text received from the editor
			if isSnapshot {
				newdata := api.InstanceSnapshotPut{}
				err = yaml.Load(content, &newdata)
				if err == nil {
					var op incus.Operation
					op, err = d.UpdateInstanceSnapshot(fields[0], fields[1],
						newdata, etag)
					if err == nil {
						err = op.Wait()
					}
				}
			} else {
				newdata := api.InstancePut{}
				err = yaml.Load(content, &newdata)
				if err == nil {
					var op incus.Operation
					op, err = d.UpdateInstance(parsedPath.String, newdata, etag)
					if err == nil {
						err = op.Wait()
					}
				}
			}

			// Respawn the editor
			if err != nil {
				fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
				fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

				_, err := os.Stdin.Read(make([]byte, 1))
				if err != nil {
					return err
				}

				content, err = cli.TextEditor("", content)
				if err != nil {
					return err
				}

				continue
			}

			break
		}

		return nil
	}

	// Targeting
	if c.config.flagTarget != "" {
		if !d.IsClustered() {
			return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
		}

		d = d.UseTarget(c.config.flagTarget)
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ServerPut{}
		err = loader.Load(&newdata)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		return d.UpdateServer(newdata, "")
	}

	// Extract the current value
	server, etag, err := d.GetServer()
	if err != nil {
		return err
	}

	brief := server.Writable()
	data, err := yaml.Dump(&brief, yaml.V2)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := cli.TextEditor("", data)
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.ServerPut{}
		err = yaml.Load(content, &newdata)
		if err == nil {
			err = d.UpdateServer(newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = cli.TextEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// Get.
type cmdConfigGet struct {
	global *cmdGlobal
	config *cmdConfig

	flagExpanded   bool
	flagIsProperty bool
}

var cmdConfigGetUsage = u.Usage{u.MakePath(u.Instance, u.Snapshot.Optional()).Optional().Remote(), u.Key}

func (c *cmdConfigGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdConfigGetUsage...)
	cmd.Short = i18n.G("Get values for instance or server configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Get values for instance or server configuration keys`))

	cmd.Flags().BoolVarP(&c.flagExpanded, "expanded", "e", false, i18n.G("Access the expanded configuration"))
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as an instance property"))
	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceAllKeys()
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigGet) run(cmd *cobra.Command, args []string) error {
	// Do NOT blindly copy the following parsing line; it performs right-to-left parsing, which in
	// most cases is NOT what you want.
	parsed, err := cmdConfigGetUsage.Parse(c.global.conf, cmd, args, true)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	parsedPath := parsed[0].RemoteObject
	key := parsed[1].String

	// Get the config key
	if !parsedPath.Skipped {
		fields := parsedPath.StringList
		isSnapshot := !parsedPath.List[1].Skipped

		// Quick checks.
		if c.config.flagTarget != "" {
			return errors.New(i18n.G("--target cannot be used with instances"))
		}

		if isSnapshot {
			inst, _, err := d.GetInstanceSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			if c.flagIsProperty {
				res, err := getFieldByJSONTag(inst, key)
				if err != nil {
					return fmt.Errorf(i18n.G("The property %q does not exist on the instance snapshot %s: %v"), key, formatRemote(c.global.conf, parsed[0]), err)
				}

				fmt.Printf("%v\n", res)
			} else {
				if c.flagExpanded {
					fmt.Println(inst.ExpandedConfig[key])
				} else {
					fmt.Println(inst.Config[key])
				}
			}

			return nil
		}

		resp, _, err := d.GetInstance(parsedPath.String)
		if err != nil {
			return err
		}

		if c.flagIsProperty {
			w := resp.Writable()
			res, err := getFieldByJSONTag(&w, key)
			if err != nil {
				return fmt.Errorf(i18n.G("The property %q does not exist on the instance %q: %v"), key, formatRemote(c.global.conf, parsed[0]), err)
			}

			fmt.Printf("%v\n", res)
		} else {
			if c.flagExpanded {
				fmt.Println(resp.ExpandedConfig[key])
			} else {
				fmt.Println(resp.Config[key])
			}
		}
	} else {
		// Quick check.
		if c.flagExpanded {
			return errors.New(i18n.G("--expanded cannot be used with a server"))
		}

		// Targeting
		if c.config.flagTarget != "" {
			if !d.IsClustered() {
				return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
			}

			d = d.UseTarget(c.config.flagTarget)
		}

		resp, _, err := d.GetServer()
		if err != nil {
			return err
		}

		value := resp.Config[key]
		fmt.Println(value)
	}

	return nil
}

// Set.
type cmdConfigSet struct {
	global *cmdGlobal
	config *cmdConfig

	flagIsProperty bool
}

var cmdConfigSetUsage = u.Usage{u.MakePath(u.Instance, u.Snapshot.Optional()).Optional().Remote(), u.LegacyKV.List(1)}

func (c *cmdConfigSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdConfigSetUsage...)
	cmd.Short = i18n.G("Set instance or server configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set instance or server configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus config set [<remote>:][<instance>] <key> <value>`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus config set [<remote>:]<instance> limits.cpu=2
    Will set a CPU limit of "2" for the instance.

incus config set my-instance cloud-init.user-data - < cloud-init.yaml
    Sets the cloud-init user-data for instance "my-instance" by reading "cloud-init.yaml" through stdin.

incus config set core.https_address=[::]:8443
    Will have the server listen on IPv4 and IPv6 port 8443.`))

	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as an instance property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceAllKeys()
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdConfigSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	parsedPath := parsed[0].RemoteObject
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	// Set the config keys
	if !parsedPath.Skipped {
		fields := parsedPath.StringList
		isSnapshot := !parsedPath.List[1].Skipped

		// Quick checks.
		if c.config.flagTarget != "" {
			return errors.New(i18n.G("--target cannot be used with instances"))
		}

		if isSnapshot {
			inst, etag, err := d.GetInstanceSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			writable := inst.Writable()
			if c.flagIsProperty {
				if cmd.Name() == "unset" {
					for k := range keys {
						err := unsetFieldByJSONTag(&writable, k)
						if err != nil {
							return fmt.Errorf(i18n.G("Error unsetting properties: %v"), err)
						}
					}
				} else {
					err := unpackKVToWritable(&writable, keys)
					if err != nil {
						return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
					}
				}

				op, err := d.UpdateInstanceSnapshot(fields[0], fields[1], writable, etag)
				if err != nil {
					return err
				}

				return op.Wait()
			}

			return errors.New(i18n.G("The is no config key to set on an instance snapshot."))
		}

		inst, etag, err := d.GetInstance(parsedPath.String)
		if err != nil {
			return err
		}

		writable := inst.Writable()
		if c.flagIsProperty {
			if cmd.Name() == "unset" {
				for k := range keys {
					err := unsetFieldByJSONTag(&writable, k)
					if err != nil {
						return fmt.Errorf(i18n.G("Error unsetting properties: %v"), err)
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
				if cmd.Name() == "unset" {
					_, ok := writable.Config[k]
					if !ok {
						return fmt.Errorf(i18n.G("Can't unset key '%s', it's not currently set"), k)
					}

					delete(writable.Config, k)
				} else {
					writable.Config[k] = v
				}
			}
		}

		op, err := d.UpdateInstance(parsedPath.String, writable, etag)
		if err != nil {
			return err
		}

		return op.Wait()
	}

	// Targeting
	if c.config.flagTarget != "" {
		if !d.IsClustered() {
			return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
		}

		d = d.UseTarget(c.config.flagTarget)
	}

	// Server keys
	server, etag, err := d.GetServer()
	if err != nil {
		return err
	}

	if server.Config == nil {
		server.Config = map[string]string{}
	}

	maps.Copy(server.Config, keys)

	return d.UpdateServer(server.Writable(), etag)
}

func (c *cmdConfigSet) run(cmd *cobra.Command, args []string) error {
	// Do NOT blindly copy the following parsing line; it performs right-to-left parsing, which in
	// most cases is NOT what you want.
	parsed, err := cmdConfigSetUsage.Parse(c.global.conf, cmd, args, true)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Show.
type cmdConfigShow struct {
	global *cmdGlobal
	config *cmdConfig

	flagExpanded bool
}

var cmdConfigShowUsage = u.Usage{u.MakePath(u.Instance, u.Snapshot.Optional()).Optional().Remote()}

func (c *cmdConfigShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdConfigShowUsage...)
	cmd.Short = i18n.G("Show instance or server configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Show instance or server configurations`))

	cmd.Flags().BoolVarP(&c.flagExpanded, "expanded", "e", false, i18n.G("Show the expanded configuration"))
	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpInstances(toComplete)
	}

	return cmd
}

func (c *cmdConfigShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdConfigShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	parsedPath := parsed[0].RemoteObject

	// Show configuration
	var data []byte

	if parsedPath.Skipped {
		// Quick check.
		if c.flagExpanded {
			return errors.New(i18n.G("--expanded cannot be used with a server"))
		}

		// Targeting
		if c.config.flagTarget != "" {
			if !d.IsClustered() {
				return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
			}

			d = d.UseTarget(c.config.flagTarget)
		}

		// Server config
		server, _, err := d.GetServer()
		if err != nil {
			return err
		}

		brief := server.Writable()
		data, err = yaml.Dump(&brief, yaml.V2)
		if err != nil {
			return err
		}
	} else {
		// Quick checks.
		if c.config.flagTarget != "" {
			return errors.New(i18n.G("--target cannot be used with instances"))
		}

		// Instance or snapshot config
		var brief any

		if !parsedPath.List[1].Skipped {
			// Snapshot
			fields := parsedPath.StringList

			snap, _, err := d.GetInstanceSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			brief = snap
			if c.flagExpanded {
				brief.(*api.InstanceSnapshot).Config = snap.ExpandedConfig
				brief.(*api.InstanceSnapshot).Devices = snap.ExpandedDevices
			}
		} else {
			// Instance
			inst, _, err := d.GetInstance(parsedPath.String)
			if err != nil {
				return err
			}

			writable := inst.Writable()
			brief = &writable

			if c.flagExpanded {
				brief.(*api.InstancePut).Config = inst.ExpandedConfig
				brief.(*api.InstancePut).Devices = inst.ExpandedDevices
			}
		}

		data, err = yaml.Dump(&brief, yaml.V2)
		if err != nil {
			return err
		}
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdConfigUnset struct {
	global    *cmdGlobal
	config    *cmdConfig
	configSet *cmdConfigSet

	flagIsProperty bool
}

var cmdConfigUnsetUsage = u.Usage{u.MakePath(u.Instance, u.Snapshot.Optional()).Optional().Remote(), u.Key}

func (c *cmdConfigUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdConfigUnsetUsage...)
	cmd.Short = i18n.G("Unset instance or server configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Unset instance or server configuration keys`))

	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as an instance property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceAllKeys()
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdConfigUnset) run(cmd *cobra.Command, args []string) error {
	// Do NOT blindly copy the following parsing line; it performs right-to-left parsing, which in
	// most cases is NOT what you want.
	parsed, err := cmdConfigUnsetUsage.Parse(c.global.conf, cmd, args, true)
	if err != nil {
		return err
	}

	c.configSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.configSet, cmd, parsed)
}
