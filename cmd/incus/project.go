package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"slices"
	"sort"
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
	"github.com/lxc/incus/v6/shared/util"
)

type projectColumn struct {
	Name string
	Data func(api.Project) string
}

type cmdProject struct {
	global *cmdGlobal
}

func (c *cmdProject) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("project")
	cmd.Short = i18n.G("Manage projects")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage projects`))

	// Create
	projectCreateCmd := cmdProjectCreate{global: c.global, project: c}
	cmd.AddCommand(projectCreateCmd.command())

	// Delete
	projectDeleteCmd := cmdProjectDelete{global: c.global, project: c}
	cmd.AddCommand(projectDeleteCmd.command())

	// Edit
	projectEditCmd := cmdProjectEdit{global: c.global, project: c}
	cmd.AddCommand(projectEditCmd.command())

	// Get
	projectGetCmd := cmdProjectGet{global: c.global, project: c}
	cmd.AddCommand(projectGetCmd.command())

	// List
	projectListCmd := cmdProjectList{global: c.global, project: c}
	cmd.AddCommand(projectListCmd.command())

	// Rename
	projectRenameCmd := cmdProjectRename{global: c.global, project: c}
	cmd.AddCommand(projectRenameCmd.command())

	// Set
	projectSetCmd := cmdProjectSet{global: c.global, project: c}
	cmd.AddCommand(projectSetCmd.command())

	// Unset
	projectUnsetCmd := cmdProjectUnset{global: c.global, project: c, projectSet: &projectSetCmd}
	cmd.AddCommand(projectUnsetCmd.command())

	// Show
	projectShowCmd := cmdProjectShow{global: c.global, project: c}
	cmd.AddCommand(projectShowCmd.command())

	// Info
	projectGetInfo := cmdProjectInfo{global: c.global, project: c}
	cmd.AddCommand(projectGetInfo.command())

	// Set default
	projectSwitchCmd := cmdProjectSwitch{global: c.global, project: c}
	cmd.AddCommand(projectSwitchCmd.command())

	// Get current project
	projectGetCurrentCmd := cmdProjectGetCurrent{global: c.global, project: c}
	cmd.AddCommand(projectGetCurrentCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdProjectCreate struct {
	global          *cmdGlobal
	project         *cmdProject
	flagConfig      []string
	flagDescription string
}

var cmdProjectCreateUsage = u.Usage{u.NewName(u.Project).Remote()}

func (c *cmdProjectCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdProjectCreateUsage...)
	cmd.Aliases = []string{"add"}
	cmd.Short = i18n.G("Create projects")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Create projects`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus project create p1
    Create a project named p1

incus project create p1 < config.yaml
    Create a project named p1 with configuration from config.yaml`))

	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the new project")+"``")
	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Project description")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProjectCreateUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	projectName := parsed[0].RemoteObject.String
	var stdinData api.ProjectPut

	// If stdin isn't a terminal, read text from it
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

	// Create the project
	project := api.ProjectsPost{}
	project.Name = projectName
	project.ProjectPut = stdinData

	if project.Config == nil {
		project.Config = map[string]string{}
		for _, entry := range c.flagConfig {
			key, value, found := strings.Cut(entry, "=")
			if !found {
				return fmt.Errorf(i18n.G("Bad key=value pair: %q"), entry)
			}

			project.Config[key] = value
		}
	}

	if c.flagDescription != "" {
		project.Description = c.flagDescription
	}

	err = d.CreateProject(project)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Project %s created")+"\n", formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

// Delete.
type cmdProjectDelete struct {
	global  *cmdGlobal
	project *cmdProject

	flagForce bool
}

var cmdProjectDeleteUsage = u.Usage{u.Project.Remote().List(1)}

func (c *cmdProjectDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdProjectDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete projects")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete projects`))

	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force delete the project and everything it contains."))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpProjects(toComplete)
	}

	return cmd
}

func (c *cmdProjectDelete) promptConfirmation(p *u.Parsed) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf(i18n.G("Remove %s and everything it contains (instances, images, volumes, networks, ...) (yes/no): "), formatRemote(c.global.conf, p))
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !slices.Contains([]string{i18n.G("yes")}, strings.ToLower(input)) {
		return errors.New(i18n.G("User aborted delete operation"))
	}

	return nil
}

func (c *cmdProjectDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProjectDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	var errs []error

	for _, p := range parsed[0].List {
		d := p.RemoteServer
		remoteName := p.RemoteName
		projectName := p.RemoteObject.String

		err := func() error {
			if c.flagForce {
				err := c.promptConfirmation(p)
				if err != nil {
					return err
				}

				err = d.DeleteProjectForce(projectName)
				if err != nil {
					return err
				}
			} else {
				err = d.DeleteProject(projectName)
				if err != nil {
					return err
				}
			}

			if !c.global.flagQuiet {
				fmt.Printf(i18n.G("Project %s deleted")+"\n", formatRemote(c.global.conf, p))
			}

			remote := c.global.conf.Remotes[remoteName]

			// Switch back to default project
			if remote.Project == projectName {
				remote.Project = ""
				c.global.conf.Remotes[remoteName] = remote
				return c.global.conf.SaveConfig(c.global.confPath)
			}

			return nil
		}()
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Edit.
type cmdProjectEdit struct {
	global  *cmdGlobal
	project *cmdProject
}

var cmdProjectEditUsage = u.Usage{u.Project.Remote()}

func (c *cmdProjectEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdProjectEditUsage...)
	cmd.Short = i18n.G("Edit project configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Edit project configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus project edit <project> < project.yaml
    Update a project using the content of project.yaml`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the project.
### Any line starting with a '# will be ignored.
###
### A project consists of a set of features and a description.
###
### An example would look like:
### config:
###   features.images: "true"
###   features.networks: "true"
###   features.networks.zones: "true"
###   features.profiles: "true"
###   features.storage.buckets: "true"
###   features.storage.volumes: "true"
### description: My own project
### name: my-project
###
### Note that the name is shown but cannot be changed`)
}

func (c *cmdProjectEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProjectEditUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	projectName := parsed[0].RemoteObject.String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ProjectPut{}
		err = yaml.Load(contents, &newdata)
		if err != nil {
			return err
		}

		return d.UpdateProject(projectName, newdata, "")
	}

	// Extract the current value
	project, etag, err := d.GetProject(projectName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&project, yaml.V2)
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
		newdata := api.ProjectPut{}
		err = yaml.Load(content, &newdata)
		if err == nil {
			err = d.UpdateProject(projectName, newdata, etag)
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
type cmdProjectGet struct {
	global  *cmdGlobal
	project *cmdProject

	flagIsProperty bool
}

var cmdProjectGetUsage = u.Usage{u.Project.Remote(), u.Key}

func (c *cmdProjectGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdProjectGetUsage...)
	cmd.Short = i18n.G("Get values for project configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Get values for project configuration keys`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a project property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpProjectConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProjectGetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	projectName := parsed[0].RemoteObject.String
	key := parsed[1].String

	// Get the configuration key
	project, _, err := d.GetProject(projectName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := project.Writable()
		res, err := getFieldByJSONTag(&w, key)
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the project %q: %v"), key, formatRemote(c.global.conf, parsed[0]), err)
		}

		fmt.Printf("%v\n", res)
	} else {
		fmt.Printf("%s\n", project.Config[key])
	}

	return nil
}

// List.
type cmdProjectList struct {
	global  *cmdGlobal
	project *cmdProject

	flagFormat  string
	flagColumns string
}

var cmdProjectListUsage = u.Usage{u.RemoteColonOpt, u.Filter.List(0)}

func (c *cmdProjectList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdProjectListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List projects")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`List projects

The -c option takes a (optionally comma-separated) list of arguments
that control which image attributes to output when displaying in table
or csv format.
Default column layout is: nipvbwzdu
Column shorthand chars:

n - Project Name
i - Images
p - Profiles
v - Storage Volumes
b - Storage Buckets
w - Networks
z - Network Zones
d - Description
u - Used By`))

	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultProjectColumns, i18n.G("Columns")+"``")

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

const defaultProjectColumns = "nipvbwzdu"

func (c *cmdProjectList) parseColumns() ([]projectColumn, error) {
	columnsShorthandMap := map[rune]projectColumn{
		'n': {i18n.G("NAME"), c.projectNameColumnData},
		'i': {i18n.G("IMAGES"), c.imagesColumnData},
		'p': {i18n.G("PROFILES"), c.profilesColumnData},
		'v': {i18n.G("STORAGE VOLUMES"), c.storageVolumesColumnData},
		'b': {i18n.G("STORAGE BUCKETS"), c.storageBucketsColumnData},
		'w': {i18n.G("NETWORKS"), c.networksColumnData},
		'z': {i18n.G("NETWORK ZONES"), c.networkZonesColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'u': {i18n.G("USED BY"), c.usedByColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []projectColumn{}

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

func (c *cmdProjectList) projectNameColumnData(project api.Project) string {
	return project.Name
}

func (c *cmdProjectList) imagesColumnData(project api.Project) string {
	images := i18n.G("NO")
	if util.IsTrue(project.Config["features.images"]) {
		images = i18n.G("YES")
	}

	return images
}

func (c *cmdProjectList) profilesColumnData(project api.Project) string {
	profiles := i18n.G("NO")
	if util.IsTrue(project.Config["features.profiles"]) {
		profiles = i18n.G("YES")
	}

	return profiles
}

func (c *cmdProjectList) storageVolumesColumnData(project api.Project) string {
	storageVolumes := i18n.G("NO")
	if util.IsTrue(project.Config["features.storage.volumes"]) {
		storageVolumes = i18n.G("YES")
	}

	return storageVolumes
}

func (c *cmdProjectList) storageBucketsColumnData(project api.Project) string {
	storageBuckets := i18n.G("NO")
	if util.IsTrue(project.Config["features.storage.buckets"]) {
		storageBuckets = i18n.G("YES")
	}

	return storageBuckets
}

func (c *cmdProjectList) networksColumnData(project api.Project) string {
	networks := i18n.G("NO")
	if util.IsTrue(project.Config["features.networks"]) {
		networks = i18n.G("YES")
	}

	return networks
}

func (c *cmdProjectList) networkZonesColumnData(project api.Project) string {
	networkZones := i18n.G("NO")
	if util.IsTrue(project.Config["features.networks.zones"]) {
		networkZones = i18n.G("YES")
	}

	return networkZones
}

func (c *cmdProjectList) descriptionColumnData(project api.Project) string {
	return project.Description
}

func (c *cmdProjectList) usedByColumnData(project api.Project) string {
	return fmt.Sprintf("%d", len(project.UsedBy))
}

func (c *cmdProjectList) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProjectListUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	filters := prepareProjectServerFilters(parsed[1].StringList, api.Project{})

	// List projects
	projects, err := d.GetProjectsWithFilter(filters)
	if err != nil {
		return err
	}

	// Get the current project.
	info, err := d.GetConnectionInfo()
	if err != nil {
		return err
	}

	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, project := range projects {
		line := []string{}
		for _, column := range columns {
			if column.Name == i18n.G("NAME") {
				if project.Name == info.Project {
					project.Name = fmt.Sprintf("%s (%s)", project.Name, i18n.G("current"))
				}
			}

			line = append(line, column.Data(project))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, projects)
}

// Rename.
type cmdProjectRename struct {
	global  *cmdGlobal
	project *cmdProject
}

var cmdProjectRenameUsage = u.Usage{u.Project.Remote(), u.NewName(u.Project)}

func (c *cmdProjectRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("rename", cmdProjectRenameUsage...)
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename projects")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Rename projects`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectRename) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProjectRenameUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	projectName := parsed[0].RemoteObject.String
	newProjectName := parsed[1].String

	// Rename the project
	op, err := d.RenameProject(projectName, api.ProjectPost{Name: newProjectName})
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Project %s renamed to %s")+"\n", formatRemote(c.global.conf, parsed[0]), newProjectName)
	}

	return nil
}

// Set.
type cmdProjectSet struct {
	global  *cmdGlobal
	project *cmdProject

	flagIsProperty bool
}

var cmdProjectSetUsage = u.Usage{u.Project.Remote(), u.LegacyKV.List(1)}

func (c *cmdProjectSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdProjectSetUsage...)
	cmd.Short = i18n.G("Set project configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Set project configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus project set [<remote>:]<project> <key> <value>`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a project property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// set runs the post-parsing command logic.
func (c *cmdProjectSet) set(cmd *cobra.Command, parsed []*u.Parsed) error {
	d := parsed[0].RemoteServer
	projectName := parsed[0].RemoteObject.String
	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	// Get the project
	project, etag, err := d.GetProject(projectName)
	if err != nil {
		return err
	}

	writable := project.Writable()
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

	return d.UpdateProject(projectName, writable, etag)
}

func (c *cmdProjectSet) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProjectSetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	return c.set(cmd, parsed)
}

// Unset.
type cmdProjectUnset struct {
	global     *cmdGlobal
	project    *cmdProject
	projectSet *cmdProjectSet

	flagIsProperty bool
}

var cmdProjectUnsetUsage = u.Usage{u.Project.Remote(), u.Key}

func (c *cmdProjectUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdProjectUnsetUsage...)
	cmd.Short = i18n.G("Unset project configuration keys")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Unset project configuration keys`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a project property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpProjectConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectUnset) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProjectUnsetUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	c.projectSet.flagIsProperty = c.flagIsProperty
	return unsetKey(c.projectSet, cmd, parsed)
}

// Show.
type cmdProjectShow struct {
	global  *cmdGlobal
	project *cmdProject
}

var cmdProjectShowUsage = u.Usage{u.Project.Remote()}

func (c *cmdProjectShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdProjectShowUsage...)
	cmd.Short = i18n.G("Show project options")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show project options`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProjectShowUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	projectName := parsed[0].RemoteObject.String

	// Show the project
	project, _, err := d.GetProject(projectName)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&project, yaml.V2)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Switch project.
type cmdProjectSwitch struct {
	global  *cmdGlobal
	project *cmdProject
}

var cmdProjectSwitchUsage = u.Usage{u.Project.Remote()}

func (c *cmdProjectSwitch) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("switch", cmdProjectSwitchUsage...)
	cmd.Short = i18n.G("Switch the current project")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Switch the current project`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectSwitch) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf
	parsed, err := cmdProjectSwitchUsage.Parse(conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	remoteName := parsed[0].RemoteName
	projectName := parsed[0].RemoteObject.String

	_, _, err = d.GetProject(projectName)
	if err != nil {
		return err
	}

	remote := conf.Remotes[remoteName]
	remote.Project = projectName
	conf.Remotes[remoteName] = remote

	return conf.SaveConfig(c.global.confPath)
}

// Info.
type cmdProjectInfo struct {
	global  *cmdGlobal
	project *cmdProject

	flagShowAccess bool
	flagFormat     string
}

var cmdProjectInfoUsage = u.Usage{u.Project.Remote()}

func (c *cmdProjectInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("info", cmdProjectInfoUsage...)
	cmd.Short = i18n.G("Get a summary of resource allocations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Get a summary of resource allocations`))
	cmd.Flags().BoolVar(&c.flagShowAccess, "show-access", false, i18n.G("Show the instance's access list"))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectInfo) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProjectInfoUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	projectName := parsed[0].RemoteObject.String

	if c.flagShowAccess {
		access, err := d.GetProjectAccess(projectName)
		if err != nil {
			return err
		}

		data, err := yaml.Dump(access, yaml.V2)
		if err != nil {
			return err
		}

		fmt.Printf("%s", data)
		return nil
	}

	// Get the current allocations
	projectState, err := d.GetProjectState(projectName)
	if err != nil {
		return err
	}

	// Render the output
	byteLimits := []string{"disk", "memory"}
	data := [][]string{}
	for k, v := range projectState.Resources {
		shortKey := strings.SplitN(k, ".", 2)[0]

		limit := i18n.G("UNLIMITED")
		if v.Limit >= 0 {
			if slices.Contains(byteLimits, shortKey) {
				limit = units.GetByteSizeStringIEC(v.Limit, 2)
			} else {
				limit = fmt.Sprintf("%d", v.Limit)
			}
		}

		usage := ""
		if slices.Contains(byteLimits, shortKey) {
			usage = units.GetByteSizeStringIEC(v.Usage, 2)
		} else {
			usage = fmt.Sprintf("%d", v.Usage)
		}

		columnName := strings.ToUpper(k)
		fields := strings.SplitN(columnName, ".", 2)
		if len(fields) == 2 {
			columnName = fmt.Sprintf("%s (%s)", fields[0], fields[1])
		}

		data = append(data, []string{columnName, limit, usage})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("RESOURCE"),
		i18n.G("LIMIT"),
		i18n.G("USAGE"),
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, projectState)
}

// Get current project.
type cmdProjectGetCurrent struct {
	global  *cmdGlobal
	project *cmdProject
}

var cmdProjectGetCurrentUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdProjectGetCurrent) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get-current", cmdProjectGetCurrentUsage...)
	cmd.Short = i18n.G("Show the current project")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show the current project`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdProjectGetCurrent) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdProjectGetCurrentUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	serverInfo, _, err := d.GetServer()
	if err != nil {
		return err
	}

	// Print the project name.
	fmt.Println(serverInfo.Environment.Project)

	return nil
}

// prepareProjectServerFilter processes and formats filter criteria
// for projects, ensuring they are in a format that the server can interpret.
func prepareProjectServerFilters(filters []string, i any) []string {
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
