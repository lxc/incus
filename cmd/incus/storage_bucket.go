package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/client"
	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/ioprogress"
	"github.com/lxc/incus/shared/termios"
	"github.com/lxc/incus/shared/units"
)

type cmdStorageBucket struct {
	global     *cmdGlobal
	flagTarget string
}

func (c *cmdStorageBucket) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("bucket")
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
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdStorageBucketCreate struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket
}

func (c *cmdStorageBucketCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<pool> <bucket> [key=value...]"))
	cmd.Short = i18n.G("Create new custom storage buckets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Create new custom storage buckets`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageBucketCreate) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
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

	// Get config filters from arguments.
	for i := 2; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key/value pair: %s"), args[i])
		}

		bucketPut.Config[entry[0]] = entry[1]
	}

	// Create the storage bucket.
	bucket := api.StorageBucketsPost{
		Name:             args[1],
		StorageBucketPut: bucketPut,
	}

	client := resource.server

	// If a target was specified, create the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		client = client.UseTarget(c.storageBucket.flagTarget)
	}

	adminKey, err := client.CreateStoragePoolBucket(resource.name, bucket)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage bucket %s created")+"\n", args[1])

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

func (c *cmdStorageBucketDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<pool> <bucket>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete storage buckets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Delete storage buckets`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageBucketDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
	}

	client := resource.server

	// If a target was specified, delete the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		client = client.UseTarget(c.storageBucket.flagTarget)
	}

	// Delete the bucket.
	err = client.DeleteStoragePoolBucket(resource.name, args[1])
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage bucket %s deleted")+"\n", args[1])
	}

	return nil
}

// Edit.
type cmdStorageBucketEdit struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket
}

func (c *cmdStorageBucketEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<pool> <bucket>"))
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

func (c *cmdStorageBucketEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
	}

	client := resource.server

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

		return client.UpdateStoragePoolBucket(resource.name, args[1], newdata, "")
	}

	// If a target was specified, edit the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		client = client.UseTarget(c.storageBucket.flagTarget)
	}

	// Get the current config.
	bucket, etag, err := client.GetStoragePoolBucket(resource.name, args[1])
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&bucket)
	if err != nil {
		return err
	}

	// Spawn the editor.
	content, err := textEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.StorageBucket{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.UpdateStoragePoolBucket(resource.name, args[1], newdata.Writable(), etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = textEditor("", content)
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

func (c *cmdStorageBucketGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<pool> <bucket> <key>"))
	cmd.Short = i18n.G("Get values for storage bucket configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Get values for storage bucket configuration keys`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a storage bucket property"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageBucketGet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
	}

	client := resource.server

	// If a target was specified, use the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		client = client.UseTarget(c.storageBucket.flagTarget)
	}

	// Get the storage bucket entry.
	resp, _, err := client.GetStoragePoolBucket(resource.name, args[1])
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJsonTag(&w, args[2])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the storage bucket %q: %v"), args[2], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		v, ok := resp.Config[args[2]]
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
	flagFormat    string
}

func (c *cmdStorageBucketList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<pool>"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List storage buckets")

	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`List storage buckets`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageBucketList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	client := resource.server

	buckets, err := client.GetStoragePoolBuckets(resource.name)
	if err != nil {
		return err
	}

	clustered := resource.server.IsClustered()

	data := make([][]string, 0, len(buckets))
	for _, bucket := range buckets {
		details := []string{
			bucket.Name,
			bucket.Description,
		}

		if clustered {
			details = append(details, bucket.Location)
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
	}

	if clustered {
		header = append(header, i18n.G("LOCATION"))
	}

	return cli.RenderTable(c.flagFormat, header, data, buckets)
}

// Set.
type cmdStorageBucketSet struct {
	global *cmdGlobal

	storageBucket *cmdStorageBucket

	flagIsProperty bool
}

func (c *cmdStorageBucketSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<pool> <bucket> <key>=<value>..."))
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

func (c *cmdStorageBucketSet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, -1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
	}

	client := resource.server

	// Get the values.
	keys, err := getConfig(args[2:]...)
	if err != nil {
		return err
	}

	// If a target was specified, use the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		client = client.UseTarget(c.storageBucket.flagTarget)
	}

	// Get the storage bucket entry.
	bucket, etag, err := client.GetStoragePoolBucket(resource.name, args[1])
	if err != nil {
		return err
	}

	writable := bucket.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJsonTag(&writable, k)
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
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	err = client.UpdateStoragePoolBucket(resource.name, args[1], writable, etag)
	if err != nil {
		return err
	}

	return nil
}

// Show.
type cmdStorageBucketShow struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket
}

func (c *cmdStorageBucketShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<pool> <bucket>"))
	cmd.Short = i18n.G("Show storage bucket configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Show storage bucket configurations`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage bucket show default data
    Will show the properties of a bucket called "data" in the "default" pool.`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageBucketShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
	}

	client := resource.server

	// If a target member was specified, get the bucket with the matching name on that member, if any.
	if c.storageBucket.flagTarget != "" {
		client = client.UseTarget(c.storageBucket.flagTarget)
	}

	bucket, _, err := client.GetStoragePoolBucket(resource.name, args[1])
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

func (c *cmdStorageBucketUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<pool> <bucket> <key>"))
	cmd.Short = i18n.G("Unset storage bucket configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Unset storage bucket configuration keys`))

	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a storage bucket property"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageBucketUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	c.storageBucketSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.storageBucketSet.Run(cmd, args)
}

// Key commands.
type cmdStorageBucketKey struct {
	global        *cmdGlobal
	storageBucket *cmdStorageBucket

	flagTarget string
}

func (c *cmdStorageBucketKey) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("key")
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
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List Keys.
type cmdStorageBucketKeyList struct {
	global           *cmdGlobal
	storageBucketKey *cmdStorageBucketKey
	flagFormat       string
}

func (c *cmdStorageBucketKeyList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<pool> <bucket>"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List storage bucket keys")

	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`List storage bucket keys`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().StringVar(&c.storageBucketKey.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageBucketKeyList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
	}

	client := resource.server

	// If a target member was specified, get the bucket with the matching name on that member, if any.
	if c.storageBucketKey.flagTarget != "" {
		client = client.UseTarget(c.storageBucketKey.flagTarget)
	}

	bucketKeys, err := client.GetStoragePoolBucketKeys(resource.name, args[1])
	if err != nil {
		return err
	}

	data := make([][]string, 0, len(bucketKeys))
	for _, bucketKey := range bucketKeys {
		details := []string{
			bucketKey.Name,
			bucketKey.Description,
			bucketKey.Role,
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("ROLE"),
	}

	return cli.RenderTable(c.flagFormat, header, data, bucketKeys)
}

// Create Key.
type cmdStorageBucketKeyCreate struct {
	global           *cmdGlobal
	storageBucketKey *cmdStorageBucketKey
	flagRole         string
	flagAccessKey    string
	flagSecretKey    string
}

func (c *cmdStorageBucketKeyCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<pool> <bucket> <key>"))
	cmd.Short = i18n.G("Create key for a storage bucket")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create key for a storage bucket"))
	cmd.RunE = c.RunAdd

	cmd.Flags().StringVar(&c.storageBucketKey.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().StringVar(&c.flagRole, "role", "read-only", i18n.G("Role (admin or read-only)")+"``")
	cmd.Flags().StringVar(&c.flagAccessKey, "access-key", "", i18n.G("Access key (auto-generated if empty)")+"``")
	cmd.Flags().StringVar(&c.flagSecretKey, "secret-key", "", i18n.G("Secret key (auto-generated if empty)")+"``")

	return cmd
}

func (c *cmdStorageBucketKeyCreate) RunAdd(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
	}

	if args[2] == "" {
		return fmt.Errorf(i18n.G("Missing key name"))
	}

	client := resource.server

	// If a target member was specified, get the bucket with the matching name on that member, if any.
	if c.storageBucketKey.flagTarget != "" {
		client = client.UseTarget(c.storageBucketKey.flagTarget)
	}

	req := api.StorageBucketKeysPost{
		Name: args[2],
		StorageBucketKeyPut: api.StorageBucketKeyPut{
			Role:      c.flagRole,
			AccessKey: c.flagAccessKey,
			SecretKey: c.flagSecretKey,
		},
	}

	key, err := client.CreateStoragePoolBucketKey(resource.name, args[1], req)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage bucket key %s added")+"\n", key.Name)
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

func (c *cmdStorageBucketKeyDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<pool> <bucket> <key>"))
	cmd.Short = i18n.G("Delete key from a storage bucket")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete key from a storage bucket"))
	cmd.RunE = c.RunRemove

	cmd.Flags().StringVar(&c.storageBucketKey.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	return cmd
}

func (c *cmdStorageBucketKeyDelete) RunRemove(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
	}

	if args[2] == "" {
		return fmt.Errorf(i18n.G("Missing key name"))
	}

	client := resource.server

	// If a target member was specified, get the bucket with the matching name on that member, if any.
	if c.storageBucketKey.flagTarget != "" {
		client = client.UseTarget(c.storageBucketKey.flagTarget)
	}

	err = client.DeleteStoragePoolBucketKey(resource.name, args[1], args[2])
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage bucket key %s removed")+"\n", args[2])
	}

	return nil
}

// Edit Key.
type cmdStorageBucketKeyEdit struct {
	global           *cmdGlobal
	storageBucketKey *cmdStorageBucketKey
}

func (c *cmdStorageBucketKeyEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<pool> <bucket> <key>"))
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

func (c *cmdStorageBucketKeyEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
	}

	if args[2] == "" {
		return fmt.Errorf(i18n.G("Missing key name"))
	}

	client := resource.server

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

		return client.UpdateStoragePoolBucketKey(resource.name, args[1], args[2], newdata, "")
	}

	// If a target was specified, edit the bucket on the given member.
	if c.storageBucketKey.flagTarget != "" {
		client = client.UseTarget(c.storageBucketKey.flagTarget)
	}

	// Get the current config.
	bucket, etag, err := client.GetStoragePoolBucketKey(resource.name, args[1], args[2])
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&bucket)
	if err != nil {
		return err
	}

	// Spawn the editor.
	content, err := textEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.StorageBucketKey{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.UpdateStoragePoolBucketKey(resource.name, args[1], args[2], newdata.Writable(), etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = textEditor("", content)
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

func (c *cmdStorageBucketKeyShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<pool> <bucket> <key>"))
	cmd.Short = i18n.G("Show storage bucket key configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Show storage bucket key configurations`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage bucket key show default data foo
    Will show the properties of a bucket key called "foo" for a bucket called "data" in the "default" pool.`))

	cmd.Flags().StringVar(&c.storageBucketKey.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageBucketKeyShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	if args[1] == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
	}

	if args[2] == "" {
		return fmt.Errorf(i18n.G("Missing key name"))
	}

	client := resource.server

	// If a target member was specified, get the bucket with the matching name on that member, if any.
	if c.storageBucketKey.flagTarget != "" {
		client = client.UseTarget(c.storageBucketKey.flagTarget)
	}

	bucket, _, err := client.GetStoragePoolBucketKey(resource.name, args[1], args[2])
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

// Command generates the command definition.
func (c *cmdStorageBucketExport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("export", i18n.G("[<remote>:]<pool> <bucket> [<path>]"))
	cmd.Short = i18n.G("Export storage bucket")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Export storage buckets as tarball.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage bucket default b1
    Download a backup tarball of the b1 storage bucket.`))

	cmd.Flags().StringVar(&c.flagCompressionAlgorithm, "compression", "", i18n.G("Define a compression algorithm: for backup or none")+"``")
	cmd.Flags().StringVar(&c.storageBucket.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdStorageBucketExport) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	pool := resources[0]
	if pool.name == "" {
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	bucketName := args[1]
	if bucketName == "" {
		return fmt.Errorf(i18n.G("Missing bucket name"))
	}

	s := pool.server

	// If a target was specified, use the bucket on the given member.
	if c.storageBucket.flagTarget != "" {
		s = s.UseTarget(c.storageBucket.flagTarget)
	}

	req := api.StorageBucketBackupsPost{
		Name:                 "",
		ExpiresAt:            time.Now().Add(23 * time.Hour),
		CompressionAlgorithm: c.flagCompressionAlgorithm,
	}

	op, err := s.CreateStoragePoolBucketBackup(pool.name, bucketName, req)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to create backup: %v"), err)
	}

	// Watch the background operation
	progress := cli.ProgressRenderer{
		Format: i18n.G("Backing up storage bucket %s"),
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
	u, err := url.Parse(utStr)
	if err != nil {
		return fmt.Errorf(i18n.G("Invalid URL %q: %w"), utStr, err)
	}

	backupName, err := url.PathUnescape(path.Base(u.EscapedPath()))
	if err != nil {
		return fmt.Errorf(i18n.G("Invalid backup name segment in path %q: %w"), u.EscapedPath(), err)
	}

	defer func() {
		// Delete backup after we're done
		op, err := s.DeleteStoragePoolBucketBackup(pool.name, bucketName, backupName)
		if err == nil {
			_ = op.Wait()
		}
	}()

	var targetName string
	if len(args) > 2 {
		targetName = args[2]
	} else {
		targetName = "backup.tar.gz"
	}

	target, err := os.Create(targetName)
	if err != nil {
		return err
	}

	defer func() { _ = target.Close() }()

	// Prepare the download request
	progress = cli.ProgressRenderer{
		Format: i18n.G("Exporting backup of storage bucket %s"),
		Quiet:  c.global.flagForceLocal,
	}

	backupFileRequest := incus.BackupFileRequest{
		BackupFile:      io.WriteSeeker(target),
		ProgressHandler: progress.UpdateProgress,
	}

	// Export tarball
	_, err = s.GetStoragePoolBucketBackupFile(pool.name, bucketName, backupName, &backupFileRequest)
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

// Command generates the command definition.
func (c *cmdStorageBucketImport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("import", i18n.G("[<remote>:]<pool> <backup file> [<bucket>]"))
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
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
	if exit {
		return err
	}

	// Connect to the daemon.
	remote, pool, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Use the provided target.
	if c.storageBucket.flagTarget != "" {
		d = d.UseTarget(c.storageBucket.flagTarget)
	}

	file, err := os.Open(args[1])
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	fstat, err := file.Stat()
	if err != nil {
		return err
	}

	bucketName := ""
	if len(args) >= 3 {
		bucketName = args[2]
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

	op, err := d.CreateStoragePoolBucketFromBackup(pool, createArgs)
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
