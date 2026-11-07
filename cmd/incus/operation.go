package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
)

type cmdOperation struct {
	global *cmdGlobal
}

func (c *cmdOperation) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("operation")
	cmd.Short = i18n.G("List, show and delete background operations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List, show and delete background operations`,
	))
	cmd.Hidden = true

	// Delete
	operationDeleteCmd := cmdOperationDelete{global: c.global, operation: c}
	cmd.AddCommand(operationDeleteCmd.command())

	// List
	operationListCmd := cmdOperationList{global: c.global, operation: c, lister: &listable[api.Operation]{global: c.global}}
	cmd.AddCommand(operationListCmd.command())

	// Show
	operationShowCmd := cmdOperationShow{global: c.global, operation: c}
	cmd.AddCommand(operationShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Delete.
type cmdOperationDelete struct {
	global    *cmdGlobal
	operation *cmdOperation
}

var cmdOperationDeleteUsage = u.Usage{u.Operation.Remote().List(1)}

func (c *cmdOperationDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdOperationDeleteUsage...)
	cmd.Aliases = []string{"cancel", "rm", "remove"}
	cmd.Short = i18n.G("Delete background operations (will attempt to cancel)")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Delete background operations (will attempt to cancel)`,
	))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdOperationDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdOperationDeleteUsage, cmd, args)
	if err != nil {
		return err
	}

	var errs []error

	for _, p := range parsed[0].List {
		d := p.RemoteServer
		operationName := p.RemoteObject.String

		// Delete the operation
		err = d.DeleteOperation(operationName)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !c.global.flagQuiet {
			fmt.Printf(i18n.G("Operation %s deleted")+"\n", formatRemote(c.global.conf, p))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// List.
type cmdOperationList struct {
	global    *cmdGlobal
	operation *cmdOperation
	lister    *listable[api.Operation]

	flagAllProjects bool
}

var cmdOperationListUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdOperationList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdOperationListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List background operations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("List background operations"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagAllProjects, "all-projects", i18n.G("List operations from all projects"))

	c.lister.init(cmd, "itdscC", map[rune]listColumn[api.Operation]{
		'i': {i18n.G("ID"), i18n.G("ID"), c.operationIDcolumnData},
		't': {i18n.G("TYPE"), i18n.G("Type"), c.typeColumnData},
		'd': {i18n.G("DESCRIPTION"), i18n.G("Description"), c.descriptionColumnData},
		's': {i18n.G("STATE"), i18n.G("State"), c.stateColumnData},
		'c': {i18n.G("CANCELABLE"), i18n.G("Cancelable"), c.cancelableColumnData},
		'C': {i18n.G("CREATED"), i18n.G("Creation date"), c.createdColumnData},
		'L': {i18n.G("LOCATION"), i18n.G("Location (cluster member)"), c.locationColumnData},
	}, locationToggle)

	cmd.RunE = c.run

	return cmd
}

func (c *cmdOperationList) operationIDcolumnData(op api.Operation) string {
	return op.ID
}

func (c *cmdOperationList) typeColumnData(op api.Operation) string {
	return strings.ToUpper(op.Class)
}

func (c *cmdOperationList) descriptionColumnData(op api.Operation) string {
	return op.Description
}

func (c *cmdOperationList) stateColumnData(op api.Operation) string {
	return strings.ToUpper(op.Status)
}

func (c *cmdOperationList) cancelableColumnData(op api.Operation) string {
	return yesNo(op.MayCancel)
}

func (c *cmdOperationList) createdColumnData(op api.Operation) string {
	return op.CreatedAt.Local().Format(dateLayout)
}

func (c *cmdOperationList) locationColumnData(op api.Operation) string {
	return op.Location
}

func (c *cmdOperationList) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdOperationListUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	if c.global.flagProject != "" && c.flagAllProjects {
		return errors.New(i18n.G("Can't specify --project with --all-projects"))
	}

	err = c.lister.toggle(locationToggle, d.IsClustered())
	if err != nil {
		return err
	}

	// Get operations
	var operations []api.Operation
	if c.flagAllProjects {
		operations, err = d.GetOperationsAllProjects()
	} else {
		operations, err = d.GetOperations()
	}

	if err != nil {
		return err
	}

	return c.lister.run(operations)
}

// Show.
type cmdOperationShow struct {
	global    *cmdGlobal
	operation *cmdOperation
}

var cmdOperationShowUsage = u.Usage{u.Operation.Remote()}

func (c *cmdOperationShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdOperationShowUsage...)
	cmd.Short = i18n.G("Show details on a background operation")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Show details on a background operation`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus operation show 344a79e4-d88a-45bf-9c39-c72c26f6ab8a
    Show details on that operation UUID`,
	))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdOperationShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdOperationShowUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	operationName := parsed[0].RemoteObject.String

	// Get the operation
	op, _, err := d.GetOperation(operationName)
	if err != nil {
		return err
	}

	// Render as YAML
	data, err := yaml.Dump(&op, yaml.WithV2Defaults())
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
