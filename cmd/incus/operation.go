package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/shared/api"
)

type cmdOperation struct {
	global *cmdGlobal
}

func (c *cmdOperation) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("operation")
	cmd.Short = i18n.G("List, show and delete background operations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List, show and delete background operations`))
	cmd.Hidden = true

	// Delete
	operationDeleteCmd := cmdOperationDelete{global: c.global, operation: c}
	cmd.AddCommand(operationDeleteCmd.Command())

	// List
	operationListCmd := cmdOperationList{global: c.global, operation: c}
	cmd.AddCommand(operationListCmd.Command())

	// Show
	operationShowCmd := cmdOperationShow{global: c.global, operation: c}
	cmd.AddCommand(operationShowCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Delete.
type cmdOperationDelete struct {
	global    *cmdGlobal
	operation *cmdOperation
}

func (c *cmdOperationDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<operation>"))
	cmd.Aliases = []string{"cancel", "rm"}
	cmd.Short = i18n.G("Delete a background operation (will attempt to cancel)")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete a background operation (will attempt to cancel)`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdOperationDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Delete the operation
	err = resource.server.DeleteOperation(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Operation %s deleted")+"\n", resource.name)
	}

	return nil
}

// List.
type cmdOperationList struct {
	global    *cmdGlobal
	operation *cmdOperation

	flagFormat      string
	flagAllProjects bool
}

func (c *cmdOperationList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List background operations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List background operations`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("List operations from all projects")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdOperationList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) == 1 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name != "" {
		return fmt.Errorf(i18n.G("Filtering isn't supported yet"))
	}

	// Get operations
	var operations []api.Operation
	if c.flagAllProjects {
		operations, err = resource.server.GetOperationsAllProjects()
	} else {
		operations, err = resource.server.GetOperations()
	}

	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, op := range operations {
		cancelable := i18n.G("NO")
		if op.MayCancel {
			cancelable = i18n.G("YES")
		}

		entry := []string{op.ID, strings.ToUpper(op.Class), op.Description, strings.ToUpper(op.Status), cancelable, op.CreatedAt.Local().Format(dateLayout)}
		if resource.server.IsClustered() {
			entry = append(entry, op.Location)
		}

		data = append(data, entry)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("ID"),
		i18n.G("TYPE"),
		i18n.G("DESCRIPTION"),
		i18n.G("STATUS"),
		i18n.G("CANCELABLE"),
		i18n.G("CREATED")}
	if resource.server.IsClustered() {
		header = append(header, i18n.G("LOCATION"))
	}

	return cli.RenderTable(c.flagFormat, header, data, operations)
}

// Show.
type cmdOperationShow struct {
	global    *cmdGlobal
	operation *cmdOperation
}

func (c *cmdOperationShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<operation>"))
	cmd.Short = i18n.G("Show details on a background operation")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show details on a background operation`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus operation show 344a79e4-d88a-45bf-9c39-c72c26f6ab8a
    Show details on that operation UUID`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdOperationShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Get the operation
	op, _, err := resource.server.GetOperation(resource.name)
	if err != nil {
		return err
	}

	// Render as YAML
	data, err := yaml.Marshal(&op)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
