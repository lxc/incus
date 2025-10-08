package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	incus "github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
)

type cmdBug struct {
	global *cmdGlobal
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdBug) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("bug")
	cmd.Short = i18n.G("Prepare bug reports")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Prepare bug reports`))

	// Info
	bugInfoCmd := cmdBugInfo{global: c.global}
	cmd.AddCommand(bugInfoCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Info.
type cmdBugInfo struct {
	global *cmdGlobal

	flagTarget string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdBugInfo) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", i18n.G("[<remote>:]"))
	cmd.Short = i18n.G("Show system details to include in bug reports")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show system details to include in bug reports`))

	cmd.RunE = c.Run
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdBugInfo) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	var remote string
	if len(args) == 1 {
		remote, _, err = conf.ParseRemote(args[0])
		if err != nil {
			return err
		}
	} else {
		remote, _, err = conf.ParseRemote("")
		if err != nil {
			return err
		}
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	return c.remoteInfo(d)
}

func (c *cmdBugInfo) remoteInfo(d incus.InstanceServer) error {
	// Targeting
	if c.flagTarget != "" {
		if !d.IsClustered() {
			return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
		}

		d = d.UseTarget(c.flagTarget)
	}

	serverStatus, _, err := d.GetServer()
	if err != nil {
		return err
	}

	unfiltered, err := yaml.Marshal(&serverStatus)
	if err != nil {
		return err
	}

	var data map[string]any
	err = yaml.Unmarshal(unfiltered, &data)
	if err != nil {
		return err
	}

	filteredFields := map[string][]string{
		"api_extensions": nil,
		"environment":    {"addresses", "certificate", "certificate_fingerprint", "server_name", "server_pid"},
	}

	for field, subfields := range filteredFields {
		if len(subfields) == 0 {
			delete(data, field)
		} else {
			subdata, ok := data[field].(map[any]any)
			if ok {
				for _, subfield := range subfields {
					delete(subdata, subfield)
				}
			}
		}
	}

	filtered, err := yaml.Marshal(&data)
	if err != nil {
		return err
	}

	fmt.Printf("%s", filtered)
	return nil
}
