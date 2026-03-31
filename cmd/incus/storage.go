package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/termios"
	"github.com/lxc/incus/v6/shared/units"
)

type cmdStorage struct {
	global *cmdGlobal

	flagTarget string
}

type storageColumn struct {
	Name string
	Data func(api.StoragePool) string
}

func (c *cmdStorage) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("storage")
	cmd.Short = i18n.G("Manage storage pools and volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage storage pools and volumes`))

	// Create
	storageCreateCmd := cmdStorageCreate{global: c.global, storage: c}
	cmd.AddCommand(storageCreateCmd.command())

	// Delete
	storageDeleteCmd := cmdStorageDelete{global: c.global, storage: c}
	cmd.AddCommand(storageDeleteCmd.command())

	// Edit
	storageEditCmd := cmdStorageEdit{global: c.global, storage: c}
	cmd.AddCommand(storageEditCmd.command())

	// Get
	storageGetCmd := cmdStorageGet{global: c.global, storage: c}
	cmd.AddCommand(storageGetCmd.command())

	// Info
	storageInfoCmd := cmdStorageInfo{global: c.global, storage: c}
	cmd.AddCommand(storageInfoCmd.command())

	// List
	storageListCmd := cmdStorageList{global: c.global, storage: c}
	cmd.AddCommand(storageListCmd.command())

	// Set
	storageSetCmd := cmdStorageSet{global: c.global, storage: c}
	cmd.AddCommand(storageSetCmd.command())

	// Show
	storageShowCmd := cmdStorageShow{global: c.global, storage: c}
	cmd.AddCommand(storageShowCmd.command())

	// Unset
	storageUnsetCmd := cmdStorageUnset{global: c.global, storage: c, storageSet: &storageSetCmd}
	cmd.AddCommand(storageUnsetCmd.command())

	// Bucket
	storageBucketCmd := cmdStorageBucket{global: c.global}
	cmd.AddCommand(storageBucketCmd.command())

	// Volume
	storageVolumeCmd := cmdStorageVolume{global: c.global, storage: c}
	cmd.AddCommand(storageVolumeCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdStorageCreate struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagDescription string
}

var cmdStorageCreateUsage = u.Usage{u.NewName(u.Pool).Remote(), u.Driver, u.KV.List(0)}

func (c *cmdStorageCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdStorageCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create storage pools")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Create storage pools`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage create s1 dir

incus storage create s1 dir < config.yaml
    Create a storage pool using the content of config.yaml.
	`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Storage pool description")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	driver := parsed[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	// If stdin isn't a terminal, read text from it
	var stdinData api.StoragePoolPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Load(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Create the new storage pool entry
	pool := api.StoragePoolsPost{StoragePoolPut: stdinData}
	pool.Name = poolName
	pool.Driver = driver

	if c.flagDescription != "" {
		pool.Description = c.flagDescription
	}

	if pool.Config == nil {
		pool.Config = map[string]string{}
	}

	maps.Copy(pool.Config, keys)

	// If a target member was specified the API won't actually create the
	// pool, but only define it as pending in the database.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	// Create the pool
	err = d.CreateStoragePool(pool)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		if c.storage.flagTarget != "" {
			fmt.Printf(i18n.G("Storage pool %s pending on member %s")+"\n", formatRemote(c.global.conf, parsed[0]), c.storage.flagTarget)
		} else {
			fmt.Printf(i18n.G("Storage pool %s created")+"\n", formatRemote(c.global.conf, parsed[0]))
		}
	}

	return nil
}

// Delete.
type cmdStorageDelete struct {
	global  *cmdGlobal
	storage *cmdStorage
}

var cmdStorageDeleteUsage = u.Usage{u.Pool.Remote().List(1)}

func (c *cmdStorageDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdStorageDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete storage pools")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete storage pools`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpStoragePools(toComplete)
	}

	return cmd
}

func (c *cmdStorageDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	var errs []error

	for _, p := range parsed[0].List {
		d := p.RemoteServer
		poolName := p.RemoteObject.String

		// Delete the pool
		err = d.DeleteStoragePool(poolName)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !c.global.flagQuiet {
			fmt.Printf(i18n.G("Storage pool %s deleted")+"\n", formatRemote(c.global.conf, p))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Edit.
type cmdStorageEdit struct {
	global  *cmdGlobal
	storage *cmdStorage
}

var cmdStorageEditUsage = u.Usage{u.Pool.Remote()}

func (c *cmdStorageEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdStorageEditUsage...)
	cmd.Short = i18n.G("Edit storage pool configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Edit storage pool configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage edit [<remote>:]<pool> < pool.yaml
    Update a storage pool using the content of pool.yaml.`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of a storage pool.
### Any line starting with a '#' will be ignored.
###
### A storage pool consists of a set of configuration items.
###
### An example would look like:
### name: default
### driver: zfs
### used_by: []
### config:
###   size: "61203283968"
###   source: default
###   zfs.pool_name: default`)
}

func (c *cmdStorageEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.StoragePoolPut{}
		err = yaml.Load(contents, &newdata)
		if err != nil {
			return err
		}

		return d.UpdateStoragePool(poolName, newdata, "")
	}

	// Extract the current value
	pool, etag, err := d.GetStoragePool(poolName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&pool, yaml.V2)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := cli.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.StoragePoolPut{}
		err = yaml.Load(content, &newdata)
		if err == nil {
			err = d.UpdateStoragePool(poolName, newdata, etag)
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
type cmdStorageGet struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagIsProperty bool
}

var cmdStorageGetUsage = u.Usage{u.Pool.Remote(), u.Key}

func (c *cmdStorageGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdStorageGetUsage...)
	cmd.Short = i18n.G("Get values for storage pool configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Get values for storage pool configuration keys`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a storage property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	key := parsed[1].String

	// If a target member was specified, we return also member-specific config values.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	// Get the property
	resp, _, err := d.GetStoragePool(poolName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the storage pool %q: %v"), key, formatRemote(c.global.conf, parsed[0]), err)
		}

		fmt.Printf("%v\n", res)
	} else {
		v, ok := resp.Config[key]
		if ok {
			fmt.Println(v)
		}
	}

	return nil
}

// Info.
type cmdStorageInfo struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagBytes bool
}

var cmdStorageInfoUsage = u.Usage{u.Pool.Remote()}

func (c *cmdStorageInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("info", cmdStorageInfoUsage...)
	cmd.Short = i18n.G("Show useful information about storage pools")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Show useful information about storage pools`))

	cmd.Flags().BoolVar(&c.flagBytes, "bytes", false, i18n.G("Show the used and free space in bytes"))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageInfo) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageInfoUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String

	// Targeting
	if c.storage.flagTarget != "" {
		if !d.IsClustered() {
			return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
		}

		d = d.UseTarget(c.storage.flagTarget)
	}

	// Get the pool information
	pool, _, err := d.GetStoragePool(poolName)
	if err != nil {
		return err
	}

	res, err := d.GetStoragePoolResources(poolName)
	if err != nil {
		return err
	}

	// Declare the poolinfo map of maps in order to build up the yaml
	poolinfo := make(map[string]map[string]string)
	poolusedby := make(map[string]map[string][]string)

	// Translations
	usedbystring := i18n.G("used by")
	infostring := i18n.G("info")
	namestring := i18n.G("name")
	driverstring := i18n.G("driver")
	descriptionstring := i18n.G("description")
	totalspacestring := i18n.G("total space")
	spaceusedstring := i18n.G("space used")

	// Initialize the usedby map
	poolusedby[usedbystring] = make(map[string][]string)

	// Build up the usedby map
	for _, v := range pool.UsedBy {
		uri, err := url.Parse(v)
		if err != nil {
			continue
		}

		fields := strings.Split(strings.TrimPrefix(uri.Path, "/1.0/"), "/")
		fieldsLen := len(fields)

		entityType := "unrecognized"
		entityName := uri.Path

		if fieldsLen > 1 {
			entityType = fields[0]
			entityName = fields[1]

			if fields[fieldsLen-2] == "snapshots" {
				continue // Skip snapshots as the parent entity will be included once in the list.
			}

			if fields[0] == "storage-pools" && fieldsLen > 3 {
				entityType = fields[2]
				entityName = fields[3]

				if entityType == "volumes" && fieldsLen > 4 {
					entityName = fields[4]
				}
			}
		}

		var sb strings.Builder
		var attribs []string
		sb.WriteString(entityName)

		// Show info regarding the project and location if present.
		values := uri.Query()
		projectName := values.Get("project")
		if projectName != "" {
			attribs = append(attribs, fmt.Sprintf("project %q", projectName))
		}

		locationName := values.Get("target")
		if locationName != "" {
			attribs = append(attribs, fmt.Sprintf("location %q", locationName))
		}

		if len(attribs) > 0 {
			sb.WriteString(" (")
			for i, attrib := range attribs {
				if i > 0 {
					sb.WriteString(", ")
				}

				sb.WriteString(attrib)
			}

			sb.WriteString(")")
		}

		poolusedby[usedbystring][entityType] = append(poolusedby[usedbystring][entityType], sb.String())
	}

	// Initialize the info map
	poolinfo[infostring] = map[string]string{}

	// Build up the info map
	poolinfo[infostring][namestring] = pool.Name
	poolinfo[infostring][driverstring] = pool.Driver
	poolinfo[infostring][descriptionstring] = pool.Description
	if c.flagBytes {
		poolinfo[infostring][totalspacestring] = strconv.FormatUint(res.Space.Total, 10)
		poolinfo[infostring][spaceusedstring] = strconv.FormatUint(res.Space.Used, 10)
	} else {
		poolinfo[infostring][totalspacestring] = units.GetByteSizeStringIEC(int64(res.Space.Total), 2)
		poolinfo[infostring][spaceusedstring] = units.GetByteSizeStringIEC(int64(res.Space.Used), 2)
	}

	poolinfodata, err := yaml.Dump(poolinfo, yaml.V2)
	if err != nil {
		return err
	}

	poolusedbydata, err := yaml.Dump(poolusedby, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", poolinfodata)
	fmt.Printf("%s", poolusedbydata)

	return nil
}

// List.
type cmdStorageList struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagFormat  string
	flagColumns string
}

var cmdStorageListUsage = u.Usage{u.RemoteColonOpt, u.Filter.List(0)}

func (c *cmdStorageList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdStorageListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available storage pools")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List available storage pools

Default column layout: nDdus

== Columns ==
The -c option takes a comma separated list of arguments that control
which instance attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  n - Name
  D - Driver
  d - Description
  S - Source
  u - used by
  s - state`))
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultStorageColumns, i18n.G("Columns")+"``")

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const defaultStorageColumns = "nDdus"

func (c *cmdStorageList) parseColumns() ([]storageColumn, error) {
	columnsShorthandMap := map[rune]storageColumn{
		'n': {i18n.G("NAME"), c.storageNameColumnData},
		'D': {i18n.G("DRIVER"), c.driverColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'S': {i18n.G("SOURCE"), c.sourceColumnData},
		'u': {i18n.G("USED BY"), c.usedByColumnData},
		's': {i18n.G("STATE"), c.stateColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []storageColumn{}

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

func (c *cmdStorageList) storageNameColumnData(storage api.StoragePool) string {
	return storage.Name
}

func (c *cmdStorageList) driverColumnData(storage api.StoragePool) string {
	return storage.Driver
}

func (c *cmdStorageList) descriptionColumnData(storage api.StoragePool) string {
	return storage.Description
}

func (c *cmdStorageList) sourceColumnData(storage api.StoragePool) string {
	return storage.Config["source"]
}

func (c *cmdStorageList) usedByColumnData(storage api.StoragePool) string {
	return fmt.Sprintf("%d", len(storage.UsedBy))
}

func (c *cmdStorageList) stateColumnData(storage api.StoragePool) string {
	return strings.ToUpper(storage.Status)
}

func (c *cmdStorageList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	filters := prepareStoragePoolsServerFilters(parsed[1].StringList, api.StoragePool{})

	// Get the storage pools
	pools, err := d.GetStoragePoolsWithFilter(filters)
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, pool := range pools {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(pool))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, pools)
}

// Set.
type cmdStorageSet struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagIsProperty bool
}

var cmdStorageSetUsage = u.Usage{u.Pool.Remote(), u.LegacyKV.List(1)}

func (c *cmdStorageSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdStorageSetUsage...)
	cmd.Short = i18n.G("Set storage pool configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set storage pool configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus storage set [<remote>:]<pool> <key> <value>`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a storage property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdStorageSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	// Get the pool entry
	pool, etag, err := d.GetStoragePool(poolName)
	if err != nil {
		return err
	}

	writable := pool.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJSONTag(&writable, k)
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
		if writable.Config == nil {
			writable.Config = make(map[string]string)
		}

		// Update the volume config keys.
		maps.Copy(writable.Config, keys)
	}

	err = d.UpdateStoragePool(poolName, writable, etag)
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdStorageSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Show.
type cmdStorageShow struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagResources bool
}

var cmdStorageShowUsage = u.Usage{u.Pool.Remote()}

func (c *cmdStorageShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdStorageShowUsage...)
	cmd.Short = i18n.G("Show storage pool configurations and resources")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Show storage pool configurations and resources`))

	cmd.Flags().BoolVar(&c.flagResources, "resources", false, i18n.G("Show the resources available to the storage pool"))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String

	// If a target member was specified, we return also member-specific config values.
	if c.storage.flagTarget != "" {
		d = d.UseTarget(c.storage.flagTarget)
	}

	if c.flagResources {
		res, err := d.GetStoragePoolResources(poolName)
		if err != nil {
			return err
		}

		data, err := yaml.Dump(&res, yaml.V2)
		if err != nil {
			return err
		}

		fmt.Printf("%s", data)

		return nil
	}

	pool, _, err := d.GetStoragePool(poolName)
	if err != nil {
		return err
	}

	sort.Strings(pool.UsedBy)

	data, err := yaml.Dump(&pool, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdStorageUnset struct {
	global     *cmdGlobal
	storage    *cmdStorage
	storageSet *cmdStorageSet

	flagIsProperty bool
}

var cmdStorageUnsetUsage = u.Usage{u.Pool.Remote(), u.Key}

func (c *cmdStorageUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdStorageUnsetUsage...)
	cmd.Short = i18n.G("Unset storage pool configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Unset storage pool configuration keys`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a storage property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.storageSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.storageSet, cmd, parsed)
}

// prepareStoragePoolsServerFilters processes and formats filter criteria
// for storage pools, ensuring they are in a format that the server can interpret.
func prepareStoragePoolsServerFilters(filters []string, i any) []string {
	formattedFilters := []string{}

	for _, filter := range filters {
		membs := strings.SplitN(filter, "=", 2)
		key := membs[0]

		if len(membs) == 1 {
			regexpValue := key
			if !strings.Contains(key, "^") && !strings.Contains(key, "$") {
				regexpValue = "^" + regexpValue + "$"
			}

			filter = fmt.Sprintf("name=(%s|^%s.*)", regexpValue, key)
		} else {
			firstPart := key
			if strings.Contains(key, ".") {
				firstPart = strings.Split(key, ".")[0]
			}

			if !structHasField(reflect.TypeOf(i), firstPart) {
				filter = fmt.Sprintf("config.%s", filter)
			}
		}

		formattedFilters = append(formattedFilters, filter)
	}

	return formattedFilters
}
