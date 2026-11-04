package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
)

type cmdWarning struct {
	global *cmdGlobal
}

func (c *cmdWarning) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("warning")
	cmd.Short = i18n.G("Manage warnings")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage warnings`))
	cmd.Hidden = true

	// List
	warningListCmd := cmdWarningList{global: c.global, warning: c, lister: &listable[api.Warning]{global: c.global}}
	cmd.AddCommand(warningListCmd.command())

	// Acknowledge
	warningAcknowledgeCmd := cmdWarningAcknowledge{global: c.global, warning: c}
	cmd.AddCommand(warningAcknowledgeCmd.command())

	// Show
	warningShowCmd := cmdWarningShow{global: c.global, warning: c}
	cmd.AddCommand(warningShowCmd.command())

	// Delete
	warningDeleteCmd := cmdWarningDelete{global: c.global, warning: c}
	cmd.AddCommand(warningDeleteCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdWarningList struct {
	global  *cmdGlobal
	warning *cmdWarning
	lister  *listable[api.Warning]

	flagAll bool
}

var cmdWarningListUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdWarningList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdWarningListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List warnings")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("List warnings"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagAll, "all|a", i18n.G("List all warnings"))

	c.lister.init(cmd, "utSscpl", map[rune]listColumn[api.Warning]{
		'c': {i18n.G("COUNT"), i18n.G("Count"), c.countColumnData},
		'f': {i18n.G("FIRST SEEN"), i18n.G("First seen date"), c.firstSeenColumnData},
		'l': {i18n.G("LAST SEEN"), i18n.G("Last seen date"), c.lastSeenColumnData},
		'p': {i18n.G("PROJECT"), i18n.G("Project"), c.projectColumnData},
		's': {i18n.G("SEVERITY"), i18n.G("Severity"), c.severityColumnData},
		'S': {i18n.G("STATE"), i18n.G("State"), c.stateColumnData},
		't': {i18n.G("TYPE"), i18n.G("Type"), c.typeColumnData},
		'u': {i18n.G("UUID"), i18n.G("UUID"), c.uuidColumnData},
		'L': {i18n.G("LOCATION"), i18n.G("Location (cluster member)"), c.locationColumnData},
	}, locationToggle)

	cmd.RunE = c.run

	return cmd
}

func (c *cmdWarningList) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdWarningListUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	err = c.lister.toggle(locationToggle, d.IsClustered())
	if err != nil {
		return err
	}

	allWarnings, err := d.GetWarnings()
	if err != nil {
		return err
	}

	// Per default, acknowledged and resolved warnings are not shown. Using the --all flag will show
	// those as well.
	warnings := []api.Warning{}

	if c.flagAll {
		warnings = allWarnings
	} else {
		for _, warning := range allWarnings {
			if warning.Status == "acknowledged" || warning.Status == "resolved" {
				continue
			}

			warnings = append(warnings, warning)
		}
	}

	return c.lister.run(warnings)
}

func (c *cmdWarningList) countColumnData(warning api.Warning) string {
	return fmt.Sprintf("%d", warning.Count)
}

func (c *cmdWarningList) firstSeenColumnData(warning api.Warning) string {
	return warning.FirstSeenAt.Local().Format(dateLayout)
}

func (c *cmdWarningList) lastSeenColumnData(warning api.Warning) string {
	return warning.LastSeenAt.Local().Format(dateLayout)
}

func (c *cmdWarningList) locationColumnData(warning api.Warning) string {
	return warning.Location
}

func (c *cmdWarningList) projectColumnData(warning api.Warning) string {
	return warning.Project
}

func (c *cmdWarningList) severityColumnData(warning api.Warning) string {
	return strings.ToUpper(warning.Severity)
}

func (c *cmdWarningList) stateColumnData(warning api.Warning) string {
	return strings.ToUpper(warning.Status)
}

func (c *cmdWarningList) typeColumnData(warning api.Warning) string {
	return warning.Type
}

func (c *cmdWarningList) uuidColumnData(warning api.Warning) string {
	return warning.UUID
}

// Acknowledge.
type cmdWarningAcknowledge struct {
	global  *cmdGlobal
	warning *cmdWarning
}

var cmdWarningAcknowledgeUsage = u.Usage{u.WarningUUID.Remote()}

func (c *cmdWarningAcknowledge) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("acknowledge", cmdWarningAcknowledgeUsage...)
	cmd.Aliases = []string{"ack"}
	cmd.Short = i18n.G("Acknowledge warning")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Acknowledge warning`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdWarningAcknowledge) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdWarningAcknowledgeUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	warningUUID := parsed[0].RemoteObject.String

	warning := api.WarningPut{Status: "acknowledged"}
	return d.UpdateWarning(warningUUID, warning, "")
}

// Show.
type cmdWarningShow struct {
	global  *cmdGlobal
	warning *cmdWarning
}

var cmdWarningShowUsage = u.Usage{u.WarningUUID.Remote()}

func (c *cmdWarningShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdWarningShowUsage...)
	cmd.Short = i18n.G("Show warning")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show warning`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdWarningShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdWarningShowUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	warningUUID := parsed[0].RemoteObject.String

	warning, _, err := d.GetWarning(warningUUID)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&warning, yaml.WithV2Defaults())
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Delete.
type cmdWarningDelete struct {
	global  *cmdGlobal
	warning *cmdWarning

	flagAll bool
}

var cmdWarningDeleteUsage = u.Usage{u.Either(u.WarningUUID.Remote().List(1), u.Sequence(u.Flag("all"), u.RemoteColonOpt))}

func (c *cmdWarningDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdWarningDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete warnings")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete warnings`))

	cli.AddBoolFlag(cmd.Flags(), &c.flagAll, "all|a", i18n.G("Delete all warnings"))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdWarningDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdWarningDeleteUsage, cmd, args)
	if err != nil {
		return err
	}

	var errs []error

	if c.flagAll {
		if parsed[0].BranchID != 1 {
			return errors.New(i18n.G("--all cannot be used together with other arguments"))
		}

		d := parsed[0].List[1].RemoteServer
		allWarnings, err := d.GetWarnings()
		if err != nil {
			return err
		}

		for _, warning := range allWarnings {
			err = d.DeleteWarning(warning.UUID)
			if err != nil {
				errs = append(errs, err)
			}
		}
	} else {
		for _, p := range parsed[0].List {
			d := p.RemoteServer
			warningUUID := p.RemoteObject.String

			// Delete warnings
			err = d.DeleteWarning(warningUUID)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
