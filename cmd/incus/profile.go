package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/termios"
)

type profileColumn struct {
	Name string
	Data func(api.Profile) string
}

type cmdProfile struct {
	global *cmdGlobal
}

func (c *cmdProfile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("profile")
	cmd.Short = i18n.G("Manage profiles")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage profiles`))

	// Add
	profileAddCmd := cmdProfileAdd{global: c.global, profile: c}
	cmd.AddCommand(profileAddCmd.command())

	// Assign
	profileAssignCmd := cmdProfileAssign{global: c.global, profile: c}
	cmd.AddCommand(profileAssignCmd.command())

	// Copy
	profileCopyCmd := cmdProfileCopy{global: c.global, profile: c}
	cmd.AddCommand(profileCopyCmd.command())

	// Create
	profileCreateCmd := cmdProfileCreate{global: c.global, profile: c}
	cmd.AddCommand(profileCreateCmd.command())

	// Delete
	profileDeleteCmd := cmdProfileDelete{global: c.global, profile: c}
	cmd.AddCommand(profileDeleteCmd.command())

	// Device
	profileDeviceCmd := cmdConfigDevice{global: c.global, profile: c}
	cmd.AddCommand(profileDeviceCmd.command())

	// Edit
	profileEditCmd := cmdProfileEdit{global: c.global, profile: c}
	cmd.AddCommand(profileEditCmd.command())

	// Get
	profileGetCmd := cmdProfileGet{global: c.global, profile: c}
	cmd.AddCommand(profileGetCmd.command())

	// List
	profileListCmd := cmdProfileList{global: c.global, profile: c}
	cmd.AddCommand(profileListCmd.command())

	// Remove
	profileRemoveCmd := cmdProfileRemove{global: c.global, profile: c}
	cmd.AddCommand(profileRemoveCmd.command())

	// Rename
	profileRenameCmd := cmdProfileRename{global: c.global, profile: c}
	cmd.AddCommand(profileRenameCmd.command())

	// Set
	profileSetCmd := cmdProfileSet{global: c.global, profile: c}
	cmd.AddCommand(profileSetCmd.command())

	// Show
	profileShowCmd := cmdProfileShow{global: c.global, profile: c}
	cmd.AddCommand(profileShowCmd.command())

	// Unset
	profileUnsetCmd := cmdProfileUnset{global: c.global, profile: c, profileSet: &profileSetCmd}
	cmd.AddCommand(profileUnsetCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdProfileAdd struct {
	global  *cmdGlobal
	profile *cmdProfile
}

var cmdProfileAddUsage = u.Usage{u.Instance.Remote(), u.Profile}

func (c *cmdProfileAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdProfileAddUsage...)
	cmd.Short = i18n.G("Add profiles to instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Add profiles to instances`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpProfiles(args[0], false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileAdd) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileAddUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	profileName := parsed[1].String

	// Add the profile
	inst, etag, err := d.GetInstance(instanceName)
	if err != nil {
		return err
	}

	inst.Profiles = append(inst.Profiles, profileName)

	op, err := d.UpdateInstance(instanceName, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s added to %s")+"\n", profileName, formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

// Assign.
type cmdProfileAssign struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagNoProfiles bool
}

var cmdProfileAssignUsage = u.Usage{u.Instance.Remote(), u.Either(u.Profile.List(1, ","), u.Flag("no-profiles"))}

func (c *cmdProfileAssign) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("assign", cmdProfileAssignUsage...)
	cmd.Aliases = []string{"apply"}
	cmd.Short = i18n.G("Assign sets of profiles to instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Assign sets of profiles to instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus profile assign foo default,bar
    Set the profiles for "foo" to "default" and "bar".

incus profile assign foo default
    Reset "foo" to only using the "default" profile.

incus profile assign foo --no-profiles
    Remove all profile from "foo"`))

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, i18n.G("Remove all profiles from the instance"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return c.global.cmpProfiles(args[0], false)
	}

	return cmd
}

func (c *cmdProfileAssign) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileAssignUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	profiles := parsed[1].StringList

	inst, etag, err := d.GetInstance(instanceName)
	if err != nil {
		return err
	}

	if parsed[1].BranchID == 0 {
		if c.flagNoProfiles {
			return errors.New(i18n.G("--no-profiles cannot be used together with other arguments"))
		}

		inst.Profiles = profiles
	} else {
		inst.Profiles = []string{}
	}

	op, err := d.UpdateInstance(instanceName, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		if parsed[1].BranchID == 0 {
			fmt.Printf(i18n.G("Profiles %s applied to %s")+"\n", parsed[1].String, formatRemote(c.global.conf, parsed[0]))
		} else {
			fmt.Printf(i18n.G("All profiles removed from %s")+"\n", formatRemote(c.global.conf, parsed[0]))
		}
	}

	return nil
}

// Copy.
type cmdProfileCopy struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagTargetProject string
	flagRefresh       bool
}

var cmdProfileCopyUsage = u.Usage{u.Profile.Remote(), u.NewName(u.Profile).Remote()}

func (c *cmdProfileCopy) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("copy", cmdProfileCopyUsage...)
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy profiles")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Copy profiles`))
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().BoolVar(&c.flagRefresh, "refresh", false, i18n.G("Update the target profile from the source if it already exists"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		if len(args) == 1 {
			return c.global.cmpProfiles(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileCopy) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileCopyUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	srcServer := parsed[0].RemoteServer
	srcProfile := parsed[0].RemoteObject.String
	dstServer := parsed[1].RemoteServer
	dstProfile := parsed[1].RemoteObject.String

	// Copy the profile
	profile, _, err := srcServer.GetProfile(srcProfile)
	if err != nil {
		return err
	}

	if c.flagTargetProject != "" {
		dstServer = dstServer.UseProject(c.flagTargetProject)
	}

	// Refresh the profile if requested.
	if c.flagRefresh {
		err := dstServer.UpdateProfile(dstProfile, profile.Writable(), "")
		if err == nil || !api.StatusErrorCheck(err, http.StatusNotFound) {
			return err
		}
	}

	newProfile := api.ProfilesPost{
		ProfilePut: profile.Writable(),
		Name:       dstProfile,
	}

	return dstServer.CreateProfile(newProfile)
}

// Create.
type cmdProfileCreate struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagDescription string
}

var cmdProfileCreateUsage = u.Usage{u.NewName(u.Profile).Remote()}

func (c *cmdProfileCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdProfileCreateUsage...)
	cmd.Short = i18n.G("Create profiles")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Create profiles`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus profile create p1
    Create a profile named p1

incus profile create p1 < config.yaml
    Create a profile named p1 with configuration from config.yaml`))

	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Profile description")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	profileName := parsed[0].RemoteObject.String
	var stdinData api.ProfilePut

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Create the profile
	profile := api.ProfilesPost{}
	profile.Name = profileName
	profile.ProfilePut = stdinData

	if c.flagDescription != "" {
		profile.Description = c.flagDescription
	}

	err = d.CreateProfile(profile)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s created")+"\n", formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

// Delete.
type cmdProfileDelete struct {
	global  *cmdGlobal
	profile *cmdProfile
}

var cmdProfileDeleteUsage = u.Usage{u.Profile.Remote().List(1)}

func (c *cmdProfileDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdProfileDeleteUsage...)
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete profiles")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete profiles`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpProfiles(toComplete, true)
	}

	return cmd
}

func (c *cmdProfileDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	var errs []error

	for _, p := range parsed[0].List {
		d := p.RemoteServer
		profileName := p.RemoteObject.String

		// Delete the profile
		err = d.DeleteProfile(profileName)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !c.global.flagQuiet {
			fmt.Printf(i18n.G("Profile %s deleted")+"\n", formatRemote(c.global.conf, p))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Edit.
type cmdProfileEdit struct {
	global  *cmdGlobal
	profile *cmdProfile
}

var cmdProfileEditUsage = u.Usage{u.Profile.Remote()}

func (c *cmdProfileEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdProfileEditUsage...)
	cmd.Short = i18n.G("Edit profile configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Edit profile configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus profile edit <profile> < profile.yaml
    Update a profile using the content of profile.yaml`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the profile.
### Any line starting with a '# will be ignored.
###
### A profile consists of a set of configuration items followed by a set of
### devices.
###
### An example would look like:
### name: onenic
### config:
###   raw.lxc: lxc.aa_profile=unconfined
### devices:
###   eth0:
###     nictype: bridged
###     parent: mybr0
###     type: nic
###
### Note that the name is shown but cannot be changed`)
}

func (c *cmdProfileEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	profileName := parsed[0].RemoteObject.String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ProfilePut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return d.UpdateProfile(profileName, newdata, "")
	}

	// Extract the current value
	profile, etag, err := d.GetProfile(profileName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&profile)
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
		newdata := api.ProfilePut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = d.UpdateProfile(profileName, newdata, etag)
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
type cmdProfileGet struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagIsProperty bool
}

var cmdProfileGetUsage = u.Usage{u.Profile.Remote(), u.Key}

func (c *cmdProfileGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdProfileGetUsage...)
	cmd.Short = i18n.G("Get values for profile configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Get values for profile configuration keys`))

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a profile property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		if len(args) == 1 {
			return c.global.cmpProfileConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	profileName := parsed[0].RemoteObject.String
	key := parsed[1].String

	// Get the configuration key
	profile, _, err := d.GetProfile(profileName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := profile.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the profile %q: %v"), profileName, formatRemote(c.global.conf, parsed[0]), err)
		}

		fmt.Printf("%v\n", res)
	} else {
		fmt.Printf("%s\n", profile.Config[key])
	}

	return nil
}

// List.
type cmdProfileList struct {
	global          *cmdGlobal
	profile         *cmdProfile
	flagFormat      string
	flagColumns     string
	flagAllProjects bool
}

var cmdProfileListUsage = u.Usage{u.RemoteColonOpt, u.Filter.List(0)}

func (c *cmdProfileList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdProfileListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List profiles")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List profiles

Filters may be of the <key>=<value> form for property based filtering,
or part of the profile name. Filters must be delimited by a ','.

Examples:
  - "foo" lists all profiles that start with the name foo
  - "name=foo" lists all profiles that exactly have the name foo
  - "description=.*bar.*" lists all profiles with a description that contains "bar"

The -c option takes a (optionally comma-separated) list of arguments
that control which image attributes to output when displaying in table
or csv format.

Default column layout is: ndu

Column shorthand chars:
n - Profile Name
d - Description
u - Used By`))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultProfileColumns, i18n.G("Columns")+"``")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Display profiles from all projects"))

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const (
	defaultProfileColumns            = "ndu"
	defaultProfileColumnsAllProjects = "endu"
)

func (c *cmdProfileList) parseColumns() ([]profileColumn, error) {
	columnsShorthandMap := map[rune]profileColumn{
		'n': {i18n.G("NAME"), c.profileNameColumnData},
		'e': {i18n.G("PROJECT"), c.projectNameColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'u': {i18n.G("USED BY"), c.usedByColumnData},
	}

	// Add project column if --all-projects flag specified and no custom column was passed.
	if c.flagAllProjects {
		if c.flagColumns == defaultProfileColumns {
			c.flagColumns = defaultProfileColumnsAllProjects
		}
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []profileColumn{}

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

func (c *cmdProfileList) profileNameColumnData(profile api.Profile) string {
	return profile.Name
}

func (c *cmdProfileList) descriptionColumnData(profile api.Profile) string {
	return profile.Description
}

func (c *cmdProfileList) projectNameColumnData(profile api.Profile) string {
	return profile.Project
}

func (c *cmdProfileList) usedByColumnData(profile api.Profile) string {
	return fmt.Sprintf("%d", len(profile.UsedBy))
}

func (c *cmdProfileList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	filters := parsed[1].StringList

	if c.global.flagProject != "" && c.flagAllProjects {
		return errors.New(i18n.G("Can't specify --project with --all-projects"))
	}

	flattenedFilters := []string{}
	for _, filter := range filters {
		flattenedFilters = append(flattenedFilters, strings.Split(filter, ",")...)
	}

	filters = flattenedFilters

	if len(filters) > 0 && !strings.Contains(filters[0], "=") {
		filters[0] = fmt.Sprintf("name=^%s($|.*)", regexp.QuoteMeta(filters[0]))
	}

	serverFilters, _ := getServerSupportedFilters(filters, []string{}, false)

	// List profiles
	var profiles []api.Profile
	if c.flagAllProjects {
		profiles, err = d.GetProfilesAllProjectsWithFilter(serverFilters)
	} else {
		profiles, err = d.GetProfilesWithFilter(serverFilters)
	}

	if err != nil {
		return err
	}

	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, profile := range profiles {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(profile))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, profiles)
}

// Remove.
type cmdProfileRemove struct {
	global  *cmdGlobal
	profile *cmdProfile
}

var cmdProfileRemoveUsage = u.Usage{u.Instance.Remote(), u.Profile}

func (c *cmdProfileRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdProfileRemoveUsage...)
	cmd.Short = i18n.G("Remove profiles from instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Remove profiles from instances`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpProfiles(args[0], false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileRemove) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileRemoveUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	profileName := parsed[1].String

	// Remove the profile
	inst, etag, err := d.GetInstance(instanceName)
	if err != nil {
		return err
	}

	if !slices.Contains(inst.Profiles, profileName) {
		return fmt.Errorf(i18n.G("Profile %s isn't currently applied to %s"), profileName, formatRemote(c.global.conf, parsed[0]))
	}

	profiles := []string{}
	for _, profile := range inst.Profiles {
		if profile == profileName {
			continue
		}

		profiles = append(profiles, profile)
	}

	inst.Profiles = profiles

	op, err := d.UpdateInstance(instanceName, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s removed from %s")+"\n", profileName, formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

// Rename.
type cmdProfileRename struct {
	global  *cmdGlobal
	profile *cmdProfile
}

var cmdProfileRenameUsage = u.Usage{u.Profile.Remote(), u.NewName(u.Profile)}

func (c *cmdProfileRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdProfileRenameUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename profiles")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename profiles`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	profileName := parsed[0].RemoteObject.String
	newProfileName := parsed[1].String

	// Rename the profile
	err = d.RenameProfile(profileName, api.ProfilePost{Name: newProfileName})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s renamed to %s")+"\n", formatRemote(c.global.conf, parsed[0]), newProfileName)
	}

	return nil
}

// Set.
type cmdProfileSet struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagIsProperty bool
}

var cmdProfileSetUsage = u.Usage{u.Profile.Remote(), u.LegacyKV.List(1)}

func (c *cmdProfileSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdProfileSetUsage...)
	cmd.Short = i18n.G("Set profile configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set profile configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus profile set [<remote>:]<profile> <key> <value>`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a profile property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceAllKeys()
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdProfileSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	profileName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	// Get the profile
	profile, etag, err := d.GetProfile(profileName)
	if err != nil {
		return err
	}

	writable := profile.Writable()
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

	return d.UpdateProfile(profileName, writable, etag)
}

func (c *cmdProfileSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Show.
type cmdProfileShow struct {
	global  *cmdGlobal
	profile *cmdProfile
}

var cmdProfileShowUsage = u.Usage{u.Profile.Remote()}

func (c *cmdProfileShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdProfileShowUsage...)
	cmd.Short = i18n.G("Show profile configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show profile configurations`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	profileName := parsed[0].RemoteObject.String

	// Show the profile
	profile, _, err := d.GetProfile(profileName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&profile)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdProfileUnset struct {
	global     *cmdGlobal
	profile    *cmdProfile
	profileSet *cmdProfileSet

	flagIsProperty bool
}

var cmdProfileUnsetUsage = u.Usage{u.Profile.Remote(), u.Key}

func (c *cmdProfileUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdProfileUnsetUsage...)
	cmd.Short = i18n.G("Unset profile configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Unset profile configuration keys`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a profile property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		if len(args) == 1 {
			return c.global.cmpProfileConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProfileUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.profileSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.profileSet, cmd, parsed)
}
