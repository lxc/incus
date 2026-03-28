package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type warningColumn struct {
	Name string
	Data func(api.Warning) string
}

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
	warningListCmd := cmdWarningList{global: c.global, warning: c}
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

	flagColumns string
	flagFormat  string
	flagAll     bool
}

const defaultWarningColumns = "utSscpLl"

var cmdWarningListUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdWarningList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdWarningListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List warnings")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List warnings

The -c option takes a (optionally comma-separated) list of arguments
that control which warning attributes to output when displaying in table
or csv format.

Default column layout is: utSscpLl

Column shorthand chars:

    c - Count
    l - Last seen
    L - Location
    f - First seen
    p - Project
    s - Severity
    S - Status
    u - UUID
    t - Type`))

	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultWarningColumns, i18n.G("Columns")+"``")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().BoolVarP(&c.flagAll, "all", "a", false, i18n.G("List all warnings")+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	return cmd
}

func (c *cmdWarningList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdWarningListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	allWarnings, err := d.GetWarnings()
	if err != nil {
		return err
	}

	// Per default, acknowledged and resolved warnings are not shown. Using the --all flag will show
	// those as well.
	var warnings []api.Warning

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

	// Process the columns
	columns, err := c.parseColumns(d.IsClustered())
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, warning := range warnings {
		row := []string{}
		for _, column := range columns {
			row = append(row, column.Data(warning))
		}

		data = append(data, row)
	}

	sort.Sort(cli.StringList(data))

	rawData := make([]*api.Warning, len(warnings))
	for i := range warnings {
		rawData[i] = &warnings[i]
	}

	headers := []string{}
	for _, column := range columns {
		headers = append(headers, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, headers, data, rawData)
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

func (c *cmdWarningList) parseColumns(clustered bool) ([]warningColumn, error) {
	columnsShorthandMap := map[rune]warningColumn{
		'c': {i18n.G("COUNT"), c.countColumnData},
		'f': {i18n.G("FIRST SEEN"), c.firstSeenColumnData},
		'l': {i18n.G("LAST SEEN"), c.lastSeenColumnData},
		'p': {i18n.G("PROJECT"), c.projectColumnData},
		's': {i18n.G("SEVERITY"), c.severityColumnData},
		'S': {i18n.G("STATE"), c.stateColumnData},
		't': {i18n.G("TYPE"), c.typeColumnData},
		'u': {i18n.G("UUID"), c.uuidColumnData},
	}

	if clustered {
		columnsShorthandMap['L'] = warningColumn{i18n.G("LOCATION"), c.locationColumnData}
	} else {
		if c.flagColumns != defaultWarningColumns {
			if strings.ContainsAny(c.flagColumns, "L") {
				return nil, errors.New(i18n.G("Can't specify column L when not clustered"))
			}
		}
		c.flagColumns = strings.ReplaceAll(c.flagColumns, "L", "")
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []warningColumn{}
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
	parsed, err := cmdWarningAcknowledgeUsage.Parse(c.global.conf, cmd, args)
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
	parsed, err := cmdWarningShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	warningUUID := parsed[0].RemoteObject.String

	warning, _, err := d.GetWarning(warningUUID)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&warning)
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

	cmd.Flags().BoolVarP(&c.flagAll, "all", "a", false, i18n.G("Delete all warnings")+"``")

	cmd.RunE = c.run

	return cmd
}

func (c *cmdWarningDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdWarningDeleteUsage.Parse(c.global.conf, cmd, args)
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
