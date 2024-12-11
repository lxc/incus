package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
)

type cmdOperation struct {
	global *cmdGlobal
}

type operationColumn struct {
	Name string
	Data func(api.Operation) string
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
	flagColumns     string
	flagAllProjects bool
}

func (c *cmdOperationList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List background operations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List background operations

Default column layout: itdscCl

== Columns ==
The -c option takes a comma separated list of arguments that control
which instance attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  i - ID
  t - Type
  d - Description
  s - State
  c - Cancelable
  C - Created
  L - Location of the operation (e.g. its cluster member)`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("List operations from all projects")+"``")
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultOperationColumns, i18n.G("Columns")+"``")

	cmd.RunE = c.Run

	return cmd
}

const defaultOperationColumns = "itdscC"

func (c *cmdOperationList) parseColumns(clustered bool) ([]operationColumn, error) {
	columnsShorthandMap := map[rune]operationColumn{
		'i': {i18n.G("ID"), c.operationIDcolumnData},
		't': {i18n.G("TYPE"), c.typeColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		's': {i18n.G("STATE"), c.stateColumnData},
		'c': {i18n.G("CANCELABLE"), c.cancelableColumnData},
		'C': {i18n.G("CREATED"), c.createdColumnData},
		'L': {i18n.G("LOCATION"), c.locationColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []operationColumn{}
	if c.flagColumns == defaultOperationColumns && clustered {
		columnList = append(columnList, "L")
	}

	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			column, ok := columnsShorthandMap[columnRune]
			if !ok {
				return nil, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}

			columns = append(columns, column)
		}
	}

	return columns, nil
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
	strCancelable := i18n.G("NO")

	if op.MayCancel {
		strCancelable = i18n.G("YES")
	}

	return strCancelable
}

func (c *cmdOperationList) createdColumnData(op api.Operation) string {
	return op.CreatedAt.Local().Format(dateLayout)
}

func (c *cmdOperationList) locationColumnData(op api.Operation) string {
	return op.Location
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

	// Parse column flags.
	columns, err := c.parseColumns(resource.server.IsClustered())
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, op := range operations {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(op))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, operations)
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
