package main

import (
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	incus "github.com/lxc/incus/v6/client"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/ioprogress"
	"github.com/lxc/incus/v6/shared/termios"
	"github.com/lxc/incus/v6/shared/units"
)

type cmdStorageBucket struct {
	global     *cmdGlobal
	flagTarget string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucket) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("bucket")
	cmd.Short = i18n.G("Manage storage buckets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Manage storage buckets.`))

	// Create.
	storageBucketCreateCmd := cmdStorageBucketCreate{global: c.global, storageBucket: c}
	cmd.AddCommand(storageBucketCreateCmd.Command())

	// Delete.
	storageBucketDeleteCmd := cmdStorageBucketDelete{global: c.global, storageBucket: c}
	cmd.AddCommand(storageBucketDeleteCmd.Command())

	// Edit.
	storageBucketEditCmd := cmdStorageBucketEdit{global: c.global, storageBucket: c}
	cmd.AddCommand(storageBucketEditCmd.Command())

	// Get.
	storageBucketGetCmd := cmdStorageBucketGet{global: c.global, storageBucket: c}
	cmd.AddCommand(storageBucketGetCmd.Command())

	// List.
	storageBucketListCmd := cmdStorageBucketList{global: c.global, storageBucket: c}
	cmd.AddCommand(storageBucketListCmd.Command())

	// Set.
	storageBucketSetCmd := cmdStorageBucketSet{global: c.global, storageBucket: c}
	cmd.AddCommand(storageBucketSetCmd.Command())

	// Show.
	storageBucketShowCmd := cmdStorageBucketShow{global: c.global, storageBucket: c}
	cmd.AddCommand(storageBucketShowCmd.Command())

	// Unset.
	storageBucketUnsetCmd := cmdStorageBucketUnset{global: c.global, storageBucket: c, storageBucketSet: &storageBucketSetCmd}
	cmd.AddCommand(storageBucketUnsetCmd.Command())

	// Key.
	storageBucketKeyCmd := cmdStorageBucketKey{global: c.global, storageBucket: c}
	cmd.AddCommand(storageBucketKeyCmd.Command())

	// Export.
	storageBucketExportCmd := cmdStorageBucketExport{global: c.global, storageBucket: c}
	cmd.AddCommand(storageBucketExportCmd.Command())

	// Import.
	storageBucketImporttCmd := cmdStorageBucketImport{global: c.global, storageBucket: c}
	cmd.AddCommand(storageBucketImporttCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdStorageBucketCreate struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket

	flagDescription string
}

var cmdStorageBucketCreateUsage = u.Usage{u.Pool.Remote(), u.NewName(u.Bucket), u.KV.List(0)}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdStorageBucketCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create new custom storage buckets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Create new custom storage buckets`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage bucket create p1 b01
	Create a new storage bucket named b01 in storage pool p1

incus storage bucket create p1 b01 < config.yaml
	Create a new storage bucket named b01 in storage pool p1 using the content of config.yaml`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Bucket description")+"``")

	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdStorageBucketCreate) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	// If stdin isn't a terminal, read yaml from it.
	var bucketPut api.StorageBucketPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &bucketPut)
		if err != nil {
			return err
		}
	}

	if bucketPut.Config == nil {
		bucketPut.Config = map[string]string{}
	}

	maps.Copy(bucketPut.Config, keys)

	// Create the storage bucket.
	bucket := api.StorageBucketsPost{
		Name:             bucketName,
		StorageBucketPut: bucketPut,
	}

	if c.flagDescription != "" {
		bucket.Description = c.flagDescription
	}

	// If a target was specified, create the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		d = d.UseTarget(c.storageBucket.flagTarget)
	}

	adminKey, err := d.CreateStoragePoolBucket(poolName, bucket)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage bucket %q created")+"\n", bucketName)

		if adminKey != nil {
			fmt.Printf(i18n.G("Admin access key: %s")+"\n", adminKey.AccessKey)
			fmt.Printf(i18n.G("Admin secret key: %s")+"\n", adminKey.SecretKey)
		}
	}

	return nil
}

// Delete.
type cmdStorageBucketDelete struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket
}

var cmdStorageBucketDeleteUsage = u.Usage{u.Pool.Remote(), u.Bucket}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdStorageBucketDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete storage buckets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Delete storage buckets`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdStorageBucketDelete) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String

	// If a target was specified, delete the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		d = d.UseTarget(c.storageBucket.flagTarget)
	}

	// Delete the bucket.
	err = d.DeleteStoragePoolBucket(poolName, bucketName)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage bucket %q deleted")+"\n", bucketName)
	}

	return nil
}

// Edit.
type cmdStorageBucketEdit struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket
}

var cmdStorageBucketEditUsage = u.Usage{u.Pool.Remote(), u.Bucket}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdStorageBucketEditUsage...)
	cmd.Short = i18n.G("Edit storage bucket configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Edit storage bucket configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage bucket edit [<remote>:]<pool> <bucket> < bucket.yaml
    Update a storage bucket using the content of bucket.yaml.`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageBucketEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of a storage bucket.
### Any line starting with a '# will be ignored.
###
### A storage bucket consists of a set of configuration items.
###
### name: bucket1
### used_by: []
### config:
###   size: "61203283968"`)
}

// Run runs the actual command logic.
func (c *cmdStorageBucketEdit) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `incus storage bucket show` command to be passed in here, but only take the
		// contents of the StorageBucketPut fields when updating.
		// The other fields are silently discarded.
		newdata := api.StorageBucketPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return d.UpdateStoragePoolBucket(poolName, bucketName, newdata, "")
	}

	// If a target was specified, edit the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		d = d.UseTarget(c.storageBucket.flagTarget)
	}

	// Get the current config.
	bucket, etag, err := d.GetStoragePoolBucket(poolName, bucketName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&bucket)
	if err != nil {
		return err
	}

	// Spawn the editor.
	content, err := cli.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.StorageBucket{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = d.UpdateStoragePoolBucket(poolName, bucketName, newdata.Writable(), etag)
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
type cmdStorageBucketGet struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket

	flagIsProperty bool
}

var cmdStorageBucketGetUsage = u.Usage{u.Pool.Remote(), u.Bucket, u.Key}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdStorageBucketGetUsage...)
	cmd.Short = i18n.G("Get values for storage bucket configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Get values for storage bucket configuration keys`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a storage bucket property"))
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdStorageBucketGet) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String
	key := parsed[2].String

	// If a target was specified, use the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		d = d.UseTarget(c.storageBucket.flagTarget)
	}

	// Get the storage bucket entry.
	resp, _, err := d.GetStoragePoolBucket(poolName, bucketName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the storage bucket %q: %v"), key, bucketName, err)
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

// List.
type cmdStorageBucketList struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket

	flagFormat      string
	flagAllProjects bool
	flagColumns     string
}

var cmdStorageBucketListUsage = u.Usage{u.Pool.Remote(), u.Filter.List(0)}

type storageBucketColumn struct {
	Name string
	Data func(api.StorageBucket) string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdStorageBucketListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List storage buckets")

	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List storage buckets

Default column layout: ndL

== Columns ==
The -c option takes a comma separated list of arguments that control
which network zone attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  e - Project name
  n - Name
  d - Description
  L - Location of the storage bucket (e.g. its cluster member)`))

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Display storage pool buckets from all projects"))
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultStorageBucketColumns, i18n.G("Columns")+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.Run

	return cmd
}

const defaultStorageBucketColumns = "nd" // codespell:ignore nd

func (c *cmdStorageBucketList) parseColumns(clustered bool) ([]storageBucketColumn, error) {
	columnsShorthandMap := map[rune]storageBucketColumn{
		'e': {i18n.G("PROJECT"), c.projectColumnData},
		'n': {i18n.G("NAME"), c.nameColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'L': {i18n.G("LOCATION"), c.locationColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []storageBucketColumn{}

	if c.flagColumns == defaultStorageBucketColumns && clustered {
		columnList = append(columnList, "L")
	}

	if c.flagColumns == defaultStorageBucketColumns && c.flagAllProjects {
		columnList = append([]string{"e"}, columnList...)
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

func (c *cmdStorageBucketList) nameColumnData(bucket api.StorageBucket) string {
	return bucket.Name
}

func (c *cmdStorageBucketList) descriptionColumnData(bucket api.StorageBucket) string {
	return bucket.Description
}

func (c *cmdStorageBucketList) locationColumnData(bucket api.StorageBucket) string {
	return bucket.Location
}

func (c *cmdStorageBucketList) projectColumnData(bucket api.StorageBucket) string {
	return bucket.Project
}

// Run runs the actual command logic.
func (c *cmdStorageBucketList) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	filters := prepareStorageBucketFilters(parsed[1].StringList)

	var buckets []api.StorageBucket
	if c.flagAllProjects {
		buckets, err = d.GetStoragePoolBucketsWithFilterAllProjects(poolName, filters)
		if err != nil {
			return err
		}
	} else {
		buckets, err = d.GetStoragePoolBucketsWithFilter(poolName, filters)
		if err != nil {
			return err
		}
	}

	// Parse column flags.
	columns, err := c.parseColumns(d.IsClustered())
	if err != nil {
		return err
	}

	data := make([][]string, 0, len(buckets))
	for _, bucket := range buckets {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(bucket))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, buckets)
}

// Set.
type cmdStorageBucketSet struct {
	global *cmdGlobal

	storageBucket *cmdStorageBucket

	flagIsProperty bool
}

var cmdStorageBucketSetUsage = u.Usage{u.Pool.Remote(), u.Bucket, u.LegacyKV.List(1)}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdStorageBucketSetUsage...)
	cmd.Short = i18n.G("Set storage bucket configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set storage bucket configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus storage bucket set [<remote>:]<pool> <bucket> <key> <value>`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a storage bucket property"))
	cmd.RunE = c.Run

	return cmd
}

// prepareStorageBucketFilters processes and formats filter criteria
// for storage buckets, ensuring they are in a format that the server can interpret.
func prepareStorageBucketFilters(filters []string) []string {
	formatedFilters := []string{}

	for _, filter := range filters {
		membs := strings.SplitN(filter, "=", 2)
		key := membs[0]

		if len(membs) == 1 {
			regexpValue := key
			if !strings.Contains(key, "^") && !strings.Contains(key, "$") {
				regexpValue = "^" + regexpValue + "$"
			}

			filter = fmt.Sprintf("name=(%s|^%s.*)", regexpValue, key)
		}

		formatedFilters = append(formatedFilters, filter)
	}

	return formatedFilters
}

// set runs the post-parsing command logic.
func (c *cmdStorageBucketSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	// If a target was specified, use the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		d = d.UseTarget(c.storageBucket.flagTarget)
	}

	// Get the storage bucket entry.
	bucket, etag, err := d.GetStoragePoolBucket(poolName, bucketName)
	if err != nil {
		return err
	}

	writable := bucket.Writable()
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
		maps.Copy(writable.Config, keys)
	}

	err = d.UpdateStoragePoolBucket(poolName, bucketName, writable, etag)
	if err != nil {
		return err
	}

	return nil
}

// Run runs the actual command logic.
func (c *cmdStorageBucketSet) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Show.
type cmdStorageBucketShow struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket
}

var cmdStorageBucketShowUsage = u.Usage{u.Pool.Remote(), u.Bucket}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdStorageBucketShowUsage...)
	cmd.Short = i18n.G("Show storage bucket configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Show storage bucket configurations`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage bucket show default data
    Will show the properties of a bucket called "data" in the "default" pool.`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdStorageBucketShow) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String

	// If a target member was specified, get the bucket with the matching name on that member, if any.
	if c.storageBucket.flagTarget != "" {
		d = d.UseTarget(c.storageBucket.flagTarget)
	}

	bucket, _, err := d.GetStoragePoolBucket(poolName, bucketName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&bucket)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdStorageBucketUnset struct {
	global           *cmdGlobal
	storageBucket    *cmdStorageBucket
	storageBucketSet *cmdStorageBucketSet

	flagIsProperty bool
}

var cmdStorageBucketUnsetUsage = u.Usage{u.Pool.Remote(), u.Bucket, u.Key}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdStorageBucketUnsetUsage...)
	cmd.Short = i18n.G("Unset storage bucket configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Unset storage bucket configuration keys`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a storage bucket property"))
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdStorageBucketUnset) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.storageBucketSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.storageBucketSet, cmd, parsed)
}

// Key commands.
type cmdStorageBucketKey struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket

	flagTarget string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketKey) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("key")
	cmd.Short = i18n.G("Manage storage bucket keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Manage storage bucket keys.`))

	// Create.
	storageBucketKeyCreateCmd := cmdStorageBucketKeyCreate{global: c.global, storageBucketKey: c}
	cmd.AddCommand(storageBucketKeyCreateCmd.Command())

	// Delete.
	storageBucketKeyDeleteCmd := cmdStorageBucketKeyDelete{global: c.global, storageBucketKey: c}
	cmd.AddCommand(storageBucketKeyDeleteCmd.Command())

	// Edit.
	storageBucketKeyEditCmd := cmdStorageBucketKeyEdit{global: c.global, storageBucketKey: c}
	cmd.AddCommand(storageBucketKeyEditCmd.Command())

	// List.
	storageBucketKeyListCmd := cmdStorageBucketKeyList{global: c.global, storageBucketKey: c}
	cmd.AddCommand(storageBucketKeyListCmd.Command())

	// Show.
	storageBucketKeyShowCmd := cmdStorageBucketKeyShow{global: c.global, storageBucketKey: c}
	cmd.AddCommand(storageBucketKeyShowCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// List Keys.
type cmdStorageBucketKeyList struct {
	global           *cmdGlobal
	storageBucketKey *cmdStorageBucketKey
	flagFormat       string
	flagColumns      string
}

type storageBucketKeyListColumns struct {
	Name string
	Data func(api.StorageBucketKey) string
}

var cmdStorageBucketKeyListUsage = u.Usage{u.Pool.Remote(), u.Bucket}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketKeyList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdStorageBucketKeyListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List storage bucket keys")

	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List storage bucket keys

Default column layout: ndr

== Columns ==
The -c option takes a comma separated list of arguments that control
which network zone attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:
  n - Name
  d - Description
  r - Role`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().StringVar(&c.storageBucketKey.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultStorageBucketKeyColumns, i18n.G("Columns")+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.Run

	return cmd
}

const defaultStorageBucketKeyColumns = "ndr"

func (c *cmdStorageBucketKeyList) parseColumns() ([]storageBucketKeyListColumns, error) {
	columnsShorthandMap := map[rune]storageBucketKeyListColumns{
		'n': {i18n.G("NAME"), c.nameColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'r': {i18n.G("ROLE"), c.roleColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []storageBucketKeyListColumns{}

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

func (c *cmdStorageBucketKeyList) nameColumnData(buckKey api.StorageBucketKey) string {
	return buckKey.Name
}

func (c *cmdStorageBucketKeyList) descriptionColumnData(buckKey api.StorageBucketKey) string {
	return buckKey.Description
}

func (c *cmdStorageBucketKeyList) roleColumnData(buckKey api.StorageBucketKey) string {
	return buckKey.Role
}

// Run runs the actual command logic.
func (c *cmdStorageBucketKeyList) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketKeyListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String

	// If a target member was specified, get the bucket with the matching name on that member, if any.
	if c.storageBucketKey.flagTarget != "" {
		d = d.UseTarget(c.storageBucketKey.flagTarget)
	}

	bucketKeys, err := d.GetStoragePoolBucketKeys(poolName, bucketName)
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := make([][]string, 0, len(bucketKeys))
	for _, bucketKey := range bucketKeys {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(bucketKey))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, bucketKeys)
}

// Create Key.
type cmdStorageBucketKeyCreate struct {
	global           *cmdGlobal
	storageBucketKey *cmdStorageBucketKey
	flagRole         string
	flagAccessKey    string
	flagSecretKey    string
	flagDescription  string
}

var cmdStorageBucketKeyCreateUsage = u.Usage{u.Pool.Remote(), u.Bucket, u.NewName(u.Key)}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketKeyCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdStorageBucketKeyCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create key for a storage bucket")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create key for a storage bucket"))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage bucket key create p1 b01 k1
	Create a key called k1 for the bucket b01 in the pool p1.

incus storage bucket key create p1 b01 k1 < config.yaml
	Create a key called k1 for the bucket b01 in the pool p1 using the content of config.yaml.`))

	cmd.RunE = c.RunAdd

	cmd.Flags().StringVar(&c.storageBucketKey.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagRole, "role", "read-only", i18n.G("Role (admin or read-only)")+"``")
	cmd.Flags().StringVar(&c.flagAccessKey, "access-key", "", i18n.G("Access key (auto-generated if empty)")+"``")
	cmd.Flags().StringVar(&c.flagSecretKey, "secret-key", "", i18n.G("Secret key (auto-generated if empty)")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Key description")+"``")

	return cmd
}

// RunAdd runs the actual command logic.
func (c *cmdStorageBucketKeyCreate) RunAdd(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketKeyCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String
	keyName := parsed[2].String

	// If a target member was specified, get the bucket with the matching name on that member, if any.
	if c.storageBucketKey.flagTarget != "" {
		d = d.UseTarget(c.storageBucketKey.flagTarget)
	}

	// If stdin isn't a terminal, read yaml from it.
	var bucketKeyPut api.StorageBucketKeyPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &bucketKeyPut)
		if err != nil {
			return err
		}
	}

	req := api.StorageBucketKeysPost{
		Name:                keyName,
		StorageBucketKeyPut: bucketKeyPut,
	}

	if c.flagRole != "" {
		req.Role = c.flagRole
	}

	if c.flagAccessKey != "" {
		req.AccessKey = c.flagAccessKey
	}

	if c.flagSecretKey != "" {
		req.SecretKey = c.flagSecretKey
	}

	if c.flagDescription != "" {
		req.Description = c.flagDescription
	}

	key, err := d.CreateStoragePoolBucketKey(poolName, bucketName, req)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage bucket key %q added")+"\n", key.Name)
		fmt.Printf(i18n.G("Access key: %s")+"\n", key.AccessKey)
		fmt.Printf(i18n.G("Secret key: %s")+"\n", key.SecretKey)
	}

	return nil
}

// Delete Key.
type cmdStorageBucketKeyDelete struct {
	global           *cmdGlobal
	storageBucketKey *cmdStorageBucketKey
}

var cmdStorageBucketKeyDeleteUsage = u.Usage{u.Pool.Remote(), u.Bucket, u.Key}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketKeyDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdStorageBucketKeyDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete key from a storage bucket")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete key from a storage bucket"))
	cmd.RunE = c.RunRemove

	cmd.Flags().StringVar(&c.storageBucketKey.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	return cmd
}

// RunRemove runs the actual command logic.
func (c *cmdStorageBucketKeyDelete) RunRemove(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketKeyDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String
	keyName := parsed[2].String

	// If a target member was specified, get the bucket with the matching name on that member, if any.
	if c.storageBucketKey.flagTarget != "" {
		d = d.UseTarget(c.storageBucketKey.flagTarget)
	}

	err = d.DeleteStoragePoolBucketKey(poolName, bucketName, keyName)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage bucket key %q removed")+"\n", keyName)
	}

	return nil
}

// Edit Key.
type cmdStorageBucketKeyEdit struct {
	global           *cmdGlobal
	storageBucketKey *cmdStorageBucketKey
}

var cmdStorageBucketKeyEditUsage = u.Usage{u.Pool.Remote(), u.Bucket, u.Key}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketKeyEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdStorageBucketKeyEditUsage...)
	cmd.Short = i18n.G("Edit storage bucket key as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Edit storage bucket key as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage bucket edit [<remote>:]<pool> <bucket> <key> < key.yaml
    Update a storage bucket key using the content of key.yaml.`))

	cmd.Flags().StringVar(&c.storageBucketKey.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageBucketKeyEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of a storage bucket.
### Any line starting with a '# will be ignored.
###
### A storage bucket consists of a set of configuration items.
###
### name: bucket1
### used_by: []
### config:
###   size: "61203283968"`)
}

// Run runs the actual command logic.
func (c *cmdStorageBucketKeyEdit) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketKeyEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String
	keyName := parsed[2].String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `incus storage bucket key show` command to be passed in here, but only take the
		// contents of the StorageBucketPut fields when updating.
		// The other fields are silently discarded.
		newdata := api.StorageBucketKeyPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return d.UpdateStoragePoolBucketKey(poolName, bucketName, keyName, newdata, "")
	}

	// If a target was specified, edit the bucket on the given member.
	if c.storageBucketKey.flagTarget != "" {
		d = d.UseTarget(c.storageBucketKey.flagTarget)
	}

	// Get the current config.
	bucket, etag, err := d.GetStoragePoolBucketKey(poolName, bucketName, keyName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&bucket)
	if err != nil {
		return err
	}

	// Spawn the editor.
	content, err := cli.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.StorageBucketKey{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = d.UpdateStoragePoolBucketKey(poolName, bucketName, keyName, newdata.Writable(), etag)
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

// Show Key.
type cmdStorageBucketKeyShow struct {
	global           *cmdGlobal
	storageBucketKey *cmdStorageBucketKey
}

var cmdStorageBucketKeyShowUsage = u.Usage{u.Pool.Remote(), u.Bucket, u.Key}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdStorageBucketKeyShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdStorageBucketKeyShowUsage...)
	cmd.Short = i18n.G("Show storage bucket key configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Show storage bucket key configurations`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage bucket key show default data foo
    Will show the properties of a bucket key called "foo" for a bucket called "data" in the "default" pool.`))

	cmd.Flags().StringVar(&c.storageBucketKey.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdStorageBucketKeyShow) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketKeyShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String
	keyName := parsed[2].String

	// If a target member was specified, get the bucket with the matching name on that member, if any.
	if c.storageBucketKey.flagTarget != "" {
		d = d.UseTarget(c.storageBucketKey.flagTarget)
	}

	bucket, _, err := d.GetStoragePoolBucketKey(poolName, bucketName, keyName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&bucket)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

type cmdStorageBucketExport struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket

	flagCompressionAlgorithm string
}

var cmdStorageBucketExportUsage = u.Usage{u.Pool.Remote(), u.Bucket, u.Target(u.File).Optional()}

// Command generates the command definition.
func (c *cmdStorageBucketExport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("export", cmdStorageBucketExportUsage...)
	cmd.Short = i18n.G("Export storage bucket")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Export storage buckets as tarball.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage bucket export default b1
    Download a backup tarball of the b1 storage bucket from the default pool.`))

	cmd.Flags().StringVar(&c.flagCompressionAlgorithm, "compression", "", i18n.G("Define a compression algorithm: for backup or none")+"``")
	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdStorageBucketExport) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketExportUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	bucketName := parsed[1].String
	targetName := parsed[2].Get("backup.tar.gz")

	// If a target was specified, use the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		d = d.UseTarget(c.storageBucket.flagTarget)
	}

	req := api.StorageBucketBackupsPost{
		Name:                 "",
		ExpiresAt:            time.Now().Add(23 * time.Hour),
		CompressionAlgorithm: c.flagCompressionAlgorithm,
	}

	var getter func(backupReq *incus.BackupFileRequest) error

	if d.HasExtension("direct_backup") {
		getter = func(backupReq *incus.BackupFileRequest) error {
			return d.CreateStoragePoolBucketBackupStream(poolName, bucketName, req, backupReq)
		}
	} else {
		op, err := d.CreateStoragePoolBucketBackup(poolName, bucketName, req)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to create backup: %v"), err)
		}

		// Watch the background operation
		progress := cli.ProgressRenderer{
			Format: i18n.G("Backing up storage bucket: %s"),
			Quiet:  c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		// Wait until backup is done
		err = cli.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return err
		}

		progress.Done("")

		err = op.Wait()
		if err != nil {
			return err
		}

		// Get name of backup
		utStr := op.Get().Resources["backups"][0]
		uri, err := url.Parse(utStr)
		if err != nil {
			return fmt.Errorf(i18n.G("Invalid URL %q: %w"), utStr, err)
		}

		backupName, err := url.PathUnescape(path.Base(uri.EscapedPath()))
		if err != nil {
			return fmt.Errorf(i18n.G("Invalid backup name segment in path %q: %w"), uri.EscapedPath(), err)
		}

		defer func() {
			// Delete backup after we're done
			op, err := d.DeleteStoragePoolBucketBackup(poolName, bucketName, backupName)
			if err == nil {
				_ = op.Wait()
			}
		}()

		getter = func(backupReq *incus.BackupFileRequest) error {
			_, err := d.GetStoragePoolBucketBackupFile(poolName, bucketName, backupName, backupReq)
			return err
		}
	}

	target, err := os.Create(targetName)
	if err != nil {
		return err
	}

	defer func() { _ = target.Close() }()

	// Prepare the download request
	progress := cli.ProgressRenderer{
		Format: i18n.G("Exporting backup of storage bucket: %s"),
		Quiet:  c.global.flagForceLocal,
	}

	backupFileRequest := incus.BackupFileRequest{
		BackupFile:      io.WriteSeeker(target),
		ProgressHandler: progress.UpdateProgress,
	}

	// Export tarball
	err = getter(&backupFileRequest)
	if err != nil {
		_ = os.Remove(targetName)
		progress.Done("")
		return fmt.Errorf(i18n.G("Failed to fetch storage bucket backup: %w"), err)
	}

	progress.Done(i18n.G("Backup exported successfully!"))

	return nil
}

// Import.
type cmdStorageBucketImport struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket
}

var cmdStorageBucketImportUsage = u.Usage{u.Pool.Remote(), u.BackupFile, u.Bucket.Optional()}

// Command generates the command definition.
func (c *cmdStorageBucketImport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("import", cmdStorageBucketImportUsage...)
	cmd.Short = i18n.G("Import storage bucket")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Import backups of storage buckets.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage bucket import default backup0.tar.gz
		Create a new storage bucket using backup0.tar.gz as the source.`))
	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdStorageBucketImport) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdStorageBucketImportUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	backupFile := parsed[1].String
	bucketName := parsed[2].String

	// Use the provided target.
	if c.storageBucket.flagTarget != "" {
		d = d.UseTarget(c.storageBucket.flagTarget)
	}

	file, err := os.Open(backupFile)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	fstat, err := file.Stat()
	if err != nil {
		return err
	}

	progress := cli.ProgressRenderer{
		Format: i18n.G("Importing bucket: %s"),
		Quiet:  c.global.flagQuiet,
	}

	createArgs := incus.StoragePoolBucketBackupArgs{
		BackupFile: &ioprogress.ProgressReader{
			ReadCloser: file,
			Tracker: &ioprogress.ProgressTracker{
				Length: fstat.Size(),
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
				},
			},
		},
		Name: bucketName,
	}

	op, err := d.CreateStoragePoolBucketFromBackup(poolName, createArgs)
	if err != nil {
		return err
	}

	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}
