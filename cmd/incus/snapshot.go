package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/termios"
)

type cmdSnapshot struct {
	global *cmdGlobal
}

func (c *cmdSnapshot) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("snapshot")
	cmd.Short = i18n.G("Manage instance snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage instance snapshots`))

	// Create.
	snapshotCreateCmd := cmdSnapshotCreate{global: c.global, snapshot: c}
	cmd.AddCommand(snapshotCreateCmd.command())

	// Delete.
	snapshotDeleteCmd := cmdSnapshotDelete{global: c.global, snapshot: c}
	cmd.AddCommand(snapshotDeleteCmd.command())

	// List.
	snapshotListCmd := cmdSnapshotList{global: c.global, snapshot: c}
	cmd.AddCommand(snapshotListCmd.command())

	// Rename.
	snapshotRenameCmd := cmdSnapshotRename{global: c.global, snapshot: c}
	cmd.AddCommand(snapshotRenameCmd.command())

	// Restore.
	snapshotRestoreCmd := cmdSnapshotRestore{global: c.global, snapshot: c}
	cmd.AddCommand(snapshotRestoreCmd.command())

	// Show.
	snapshotShowCmd := cmdSnapshotShow{global: c.global, snapshot: c}
	cmd.AddCommand(snapshotShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdSnapshotCreate struct {
	global   *cmdGlobal
	snapshot *cmdSnapshot

	flagStateful bool
	flagNoExpiry bool
	flagExpiry   string
	flagReuse    bool
}

var cmdSnapshotCreateUsage = u.Usage{u.Instance.Remote(), u.NewName(u.Snapshot).Optional()}

func (c *cmdSnapshotCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdSnapshotCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create instance snapshot")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Create instance snapshots

When --stateful is used, attempt to checkpoint the instance's
running state, including process memory state, TCP connections, ...`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus snapshot create u1 snap0
	Create a snapshot of "u1" called "snap0".

incus snapshot create u1 snap0 < config.yaml
	Create a snapshot of "u1" called "snap0" with the configuration from "config.yaml".`))

	cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, i18n.G("Whether or not to snapshot the instance's running state"))
	cmd.Flags().StringVar(&c.flagExpiry, "expiry", "", i18n.G("Expiry date or time span for the new snapshot"))
	cmd.Flags().BoolVar(&c.flagNoExpiry, "no-expiry", false, i18n.G("Ignore any configured auto-expiry for the instance"))
	cmd.Flags().BoolVar(&c.flagReuse, "reuse", false, i18n.G("If the snapshot name already exists, delete and create a new one"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdSnapshotCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdSnapshotCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	hasSnapName := !parsed[1].Skipped
	snapName := parsed[1].String

	if c.flagNoExpiry && c.flagExpiry != "" {
		return errors.New(i18n.G("Can't use both --no-expiry and --expiry"))
	}

	// If stdin isn't a terminal, read text from it
	var stdinData api.InstanceSnapshotPut
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin)
		if err != nil {
			return err
		}

		err = loader.Load(&stdinData)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}

	if c.flagReuse && hasSnapName {
		snap, _, _ := d.GetInstanceSnapshot(instanceName, snapName)
		if snap != nil {
			op, err := d.DeleteInstanceSnapshot(instanceName, snapName)
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return err
			}
		}
	}

	req := api.InstanceSnapshotsPost{
		Name:     snapName,
		Stateful: c.flagStateful,
	}

	if c.flagNoExpiry {
		req.ExpiresAt = &time.Time{}
	} else if c.flagExpiry != "" {
		// Try to parse as a duration.
		expiry, err := instance.GetExpiry(time.Now(), c.flagExpiry)
		if err != nil {
			if !errors.Is(err, instance.ErrInvalidExpiry) {
				return err
			}

			// Fallback to date parsing.
			expiry, err = time.Parse(dateLayout, c.flagExpiry)
			if err != nil {
				return err
			}
		}

		req.ExpiresAt = &expiry
	} else if !stdinData.ExpiresAt.IsZero() {
		req.ExpiresAt = &stdinData.ExpiresAt
	}

	op, err := d.CreateInstanceSnapshot(instanceName, req)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	opInfo := op.Get()

	snapshots, ok := opInfo.Resources["instances_snapshots"]
	if !ok || len(snapshots) == 0 {
		return errors.New(i18n.G("Didn't get name of new instance snapshot from the server"))
	}

	if len(snapshots) == 1 && !hasSnapName {
		uri, err := url.Parse(snapshots[0])
		if err != nil {
			return err
		}

		fmt.Printf(i18n.G("Instance snapshot name is: %s")+"\n", path.Base(uri.Path))
	}

	return nil
}

// Delete.
type cmdSnapshotDelete struct {
	global   *cmdGlobal
	snapshot *cmdSnapshot

	flagInteractive bool
}

var cmdSnapshotDeleteUsage = u.Usage{u.Instance.Remote(), u.Snapshot}

func (c *cmdSnapshotDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdSnapshotDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete instance snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete instance snapshots`))

	cmd.Flags().BoolVarP(&c.flagInteractive, "interactive", "i", false, i18n.G("Require user confirmation"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdSnapshotDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdSnapshotDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	snapName := parsed[1].String

	// Process with deletion.
	if c.flagInteractive {
		err := c.promptDelete(parsed[0], snapName)
		if err != nil {
			return err
		}
	}

	err = c.doDelete(d, instanceName, snapName)
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdSnapshotDelete) promptDelete(p *u.Parsed, name string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(i18n.G("Remove snapshot %s from %s (yes/no): "), name, formatRemote(c.global.conf, p))
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !slices.Contains([]string{i18n.G("yes")}, strings.ToLower(input)) {
		return errors.New(i18n.G("User aborted delete operation"))
	}

	return nil
}

func (c *cmdSnapshotDelete) doDelete(d incus.InstanceServer, instName string, name string) error {
	var op incus.Operation
	var err error

	// Snapshot delete
	op, err = d.DeleteInstanceSnapshot(instName, name)
	if err != nil {
		return err
	}

	return op.Wait()
}

// List.
type cmdSnapshotList struct {
	global   *cmdGlobal
	snapshot *cmdSnapshot

	flagFormat  string
	flagColumns string
}

var cmdSnapshotListUsage = u.Usage{u.Instance.Remote()}

type snapshotColumn struct {
	Name string
	Data func(api.InstanceSnapshot) string
}

func (c *cmdSnapshotList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdSnapshotListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List instance snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List instance snapshots

Default column layout: nTEs

== Columns ==
The -c option takes a comma separated list of arguments that control
which network zone attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  n - Name
  T - Taken At
  E - Expires At
  s - Stateful`))

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultSnapshotColumns, i18n.G("Columns")+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const defaultSnapshotColumns = "nTEs"

func (c *cmdSnapshotList) parseColumns() ([]snapshotColumn, error) {
	columnsShorthandMap := map[rune]snapshotColumn{
		'n': {i18n.G("NAME"), c.nameColumnData},
		'T': {i18n.G("TAKEN AT"), c.takenAtColumnData},
		'E': {i18n.G("EXPIRES AT"), c.expiresAtColumnData},
		's': {i18n.G("STATEFUL"), c.statefulColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []snapshotColumn{}

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

func (c *cmdSnapshotList) nameColumnData(snapshot api.InstanceSnapshot) string {
	return snapshot.Name
}

func (c *cmdSnapshotList) takenAtColumnData(snapshot api.InstanceSnapshot) string {
	if snapshot.CreatedAt.IsZero() {
		return " "
	}

	return snapshot.CreatedAt.Local().Format(dateLayout)
}

func (c *cmdSnapshotList) expiresAtColumnData(snapshot api.InstanceSnapshot) string {
	if snapshot.ExpiresAt.IsZero() {
		return " "
	}

	return snapshot.ExpiresAt.Local().Format(dateLayout)
}

func (c *cmdSnapshotList) statefulColumnData(snapshot api.InstanceSnapshot) string {
	strStateful := "NO"
	if snapshot.Stateful {
		strStateful = "YES"
	}

	return strStateful
}

func (c *cmdSnapshotList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdSnapshotListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String

	snapshots, err := d.GetInstanceSnapshots(instanceName)
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, snap := range snapshots {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(snap))
		}

		data = append(data, line)
	}

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, snapshots)
}

// Rename.
type cmdSnapshotRename struct {
	global   *cmdGlobal
	snapshot *cmdSnapshot
}

var cmdSnapshotRenameUsage = u.Usage{u.Instance.Remote(), u.Snapshot, u.NewName(u.Snapshot)}

func (c *cmdSnapshotRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdSnapshotRenameUsage...)
	cmd.Short = i18n.G("Rename instance snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename instance snapshots`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceSnapshots(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdSnapshotRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdSnapshotRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	snapName := parsed[1].String
	newSnapName := parsed[2].String

	// Snapshot rename
	op, err := d.RenameInstanceSnapshot(instanceName, snapName, api.InstanceSnapshotPost{Name: newSnapName})
	if err != nil {
		return err
	}

	return op.Wait()
}

// Restore.
type cmdSnapshotRestore struct {
	global   *cmdGlobal
	snapshot *cmdSnapshot

	flagStateful bool
	flagDiskOnly bool
}

var cmdSnapshotRestoreUsage = u.Usage{u.Instance.Remote(), u.Snapshot}

func (c *cmdSnapshotRestore) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("restore", cmdSnapshotRestoreUsage...)
	cmd.Short = i18n.G("Restore instance snapshots")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Restore instance from snapshots

If --stateful is passed, then the running state will be restored too.
If --diskonly is passed, then only the disk will be restored.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus snapshot restore u1 snap0
    Restore instance u1 to snapshot snap0`))

	cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, i18n.G("Whether or not to restore the instance's running state from snapshot (if available)"))
	cmd.Flags().BoolVar(&c.flagDiskOnly, "diskonly", false, i18n.G("Whether or not to restore the instance's disk only"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceSnapshots(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdSnapshotRestore) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdSnapshotRestoreUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	snapName := parsed[1].String

	req := api.InstancePut{
		Restore:  instanceName + "/" + snapName,
		Stateful: c.flagStateful,
		DiskOnly: c.flagDiskOnly,
	}

	// Restore the snapshot
	op, err := d.UpdateInstance(instanceName, req, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

// Show.
type cmdSnapshotShow struct {
	global   *cmdGlobal
	snapshot *cmdSnapshot
}

var cmdSnapshotShowUsage = u.Usage{u.Instance.Remote(), u.Snapshot}

func (c *cmdSnapshotShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdSnapshotShowUsage...)
	cmd.Short = i18n.G("Show instance snapshot configuration")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Show instance snapshot configuration`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceSnapshots(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdSnapshotShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdSnapshotShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	snapName := parsed[1].String

	// Snapshot
	snap, _, err := d.GetInstanceSnapshot(instanceName, snapName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&snap, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
