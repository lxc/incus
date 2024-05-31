package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
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

func (c *cmdProject) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("project")
	cmd.Short = i18n.G("Manage projects")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage projects`))

	// Create
	projectCreateCmd := cmdProjectCreate{global: c.global, project: c}
	cmd.AddCommand(projectCreateCmd.Command())

	// Delete
	projectDeleteCmd := cmdProjectDelete{global: c.global, project: c}
	cmd.AddCommand(projectDeleteCmd.Command())

	// Edit
	projectEditCmd := cmdProjectEdit{global: c.global, project: c}
	cmd.AddCommand(projectEditCmd.Command())

	// Get
	projectGetCmd := cmdProjectGet{global: c.global, project: c}
	cmd.AddCommand(projectGetCmd.Command())

	// List
	projectListCmd := cmdProjectList{global: c.global, project: c}
	cmd.AddCommand(projectListCmd.Command())

	// Rename
	projectRenameCmd := cmdProjectRename{global: c.global, project: c}
	cmd.AddCommand(projectRenameCmd.Command())

	// Set
	projectSetCmd := cmdProjectSet{global: c.global, project: c}
	cmd.AddCommand(projectSetCmd.Command())

	// Unset
	projectUnsetCmd := cmdProjectUnset{global: c.global, project: c, projectSet: &projectSetCmd}
	cmd.AddCommand(projectUnsetCmd.Command())

	// Show
	projectShowCmd := cmdProjectShow{global: c.global, project: c}
	cmd.AddCommand(projectShowCmd.Command())

	// Info
	projectGetInfo := cmdProjectInfo{global: c.global, project: c}
	cmd.AddCommand(projectGetInfo.Command())

	// Set default
	projectSwitchCmd := cmdProjectSwitch{global: c.global, project: c}
	cmd.AddCommand(projectSwitchCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdProjectCreate struct {
	global     *cmdGlobal
	project    *cmdProject
	flagConfig []string
}

func (c *cmdProjectCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<project>"))
	cmd.Short = i18n.G("Create projects")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create projects`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus project create p1

incus project create p1 < config.yaml
    Create a project with configuration from config.yaml`))

	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the new project")+"``")

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectCreate) Run(cmd *cobra.Command, args []string) error {
	var stdinData api.ProjectPut

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

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

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// Create the project
	project := api.ProjectsPost{}
	project.Name = resource.name
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

	err = resource.server.CreateProject(project)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Project %s created")+"\n", resource.name)
	}

	return nil
}

// Delete.
type cmdProjectDelete struct {
	global  *cmdGlobal
	project *cmdProject

	flagForce bool
}

func (c *cmdProjectDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<project>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete projects")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete projects`))

	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force delete the project and everything it contains."))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectDelete) promptConfirmation(name string) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf(i18n.G("Remove %s and everything it contains (instances, images, volumes, networks, ...) (yes/no): "), name)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !slices.Contains([]string{i18n.G("yes")}, strings.ToLower(input)) {
		return fmt.Errorf(i18n.G("User aborted delete operation"))
	}

	return nil
}

func (c *cmdProjectDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remoteName, _, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// Delete the project, server is unable to find the project here.
	if c.flagForce {
		err := c.promptConfirmation(resource.name)
		if err != nil {
			return err
		}

		err = resource.server.DeleteProjectForce(resource.name)
		if err != nil {
			return err
		}
	} else {
		err = resource.server.DeleteProject(resource.name)
		if err != nil {
			return err
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Project %s deleted")+"\n", resource.name)
	}

	// Switch back to default project
	if resource.name == c.global.conf.Remotes[remoteName].Project {
		rc := c.global.conf.Remotes[remoteName]
		rc.Project = ""
		c.global.conf.Remotes[remoteName] = rc
		return c.global.conf.SaveConfig(c.global.confPath)
	}

	return nil
}

// Edit.
type cmdProjectEdit struct {
	global  *cmdGlobal
	project *cmdProject
}

func (c *cmdProjectEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<project>"))
	cmd.Short = i18n.G("Edit project configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit project configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus project edit <project> < project.yaml
    Update a project using the content of project.yaml`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdProjectEdit) Run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ProjectPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateProject(resource.name, newdata, "")
	}

	// Extract the current value
	project, etag, err := resource.server.GetProject(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&project)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := textEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.ProjectPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateProject(resource.name, newdata, etag)
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
type cmdProjectGet struct {
	global  *cmdGlobal
	project *cmdProject

	flagIsProperty bool
}

func (c *cmdProjectGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<project> <key>"))
	cmd.Short = i18n.G("Get values for project configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for project configuration keys`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a project property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdProjectGet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// Get the configuration key
	project, _, err := resource.server.GetProject(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := project.Writable()
		res, err := getFieldByJsonTag(&w, args[1])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the project %q: %v"), args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		fmt.Printf("%s\n", project.Config[args[1]])
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

func (c *cmdProjectList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List projects")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
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

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(false)
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

func (c *cmdProjectList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := conf.DefaultRemote
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// List projects
	projects, err := resource.server.GetProjects()
	if err != nil {
		return err
	}

	// Get the current project.
	info, err := resource.server.GetConnectionInfo()
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
			if column.Name == "NAME" {
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

	return cli.RenderTable(c.flagFormat, header, data, projects)
}

// Rename.
type cmdProjectRename struct {
	global  *cmdGlobal
	project *cmdProject
}

func (c *cmdProjectRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<project> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename projects")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename projects`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectRename) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// Rename the project
	op, err := resource.server.RenameProject(resource.name, api.ProjectPost{Name: args[1]})
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Project %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Set.
type cmdProjectSet struct {
	global  *cmdGlobal
	project *cmdProject

	flagIsProperty bool
}

func (c *cmdProjectSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<project> <key>=<value>..."))
	cmd.Short = i18n.G("Set project configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set project configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus project set [<remote>:]<project> <key> <value>`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a project property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectSet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// Get the project
	project, etag, err := resource.server.GetProject(resource.name)
	if err != nil {
		return err
	}

	// Set the configuration key
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := project.Writable()
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

	return resource.server.UpdateProject(resource.name, writable, etag)
}

// Unset.
type cmdProjectUnset struct {
	global     *cmdGlobal
	project    *cmdProject
	projectSet *cmdProjectSet

	flagIsProperty bool
}

func (c *cmdProjectUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<project> <key>"))
	cmd.Short = i18n.G("Unset project configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset project configuration keys`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a project property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

func (c *cmdProjectUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.projectSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.projectSet.Run(cmd, args)
}

// Show.
type cmdProjectShow struct {
	global  *cmdGlobal
	project *cmdProject
}

func (c *cmdProjectShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<project>"))
	cmd.Short = i18n.G("Show project options")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show project options`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectShow) Run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// Show the project
	project, _, err := resource.server.GetProject(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&project)
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

func (c *cmdProjectSwitch) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("switch", i18n.G("[<remote>:]<project>"))
	cmd.Short = i18n.G("Switch the current project")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Switch the current project`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectSwitch) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse the remote
	remote, project, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	// Make sure the remote exists
	rc, ok := conf.Remotes[remote]
	if !ok {
		return fmt.Errorf(i18n.G("Remote %s doesn't exist"), remote)
	}

	// Make sure the project exists
	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	_, _, err = d.GetProject(project)
	if err != nil {
		return err
	}

	rc.Project = project

	conf.Remotes[remote] = rc

	return conf.SaveConfig(c.global.confPath)
}

// Info.
type cmdProjectInfo struct {
	global  *cmdGlobal
	project *cmdProject

	flagShowAccess bool
	flagFormat     string
}

func (c *cmdProjectInfo) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", i18n.G("[<remote>:]<project>"))
	cmd.Short = i18n.G("Get a summary of resource allocations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get a summary of resource allocations`))
	cmd.Flags().BoolVar(&c.flagShowAccess, "show-access", false, i18n.G("Show the instance's access list"))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProjects(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProjectInfo) Run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	if c.flagShowAccess {
		access, err := resource.server.GetProjectAccess(resource.name)
		if err != nil {
			return err
		}

		data, err := yaml.Marshal(access)
		if err != nil {
			return err
		}

		fmt.Printf("%s", data)
		return nil
	}

	// Get the current allocations
	projectState, err := resource.server.GetProjectState(resource.name)
	if err != nil {
		return err
	}

	// Render the output
	byteLimits := []string{"disk", "memory"}
	data := [][]string{}
	for k, v := range projectState.Resources {
		limit := i18n.G("UNLIMITED")
		if v.Limit >= 0 {
			if slices.Contains(byteLimits, k) {
				limit = units.GetByteSizeStringIEC(v.Limit, 2)
			} else {
				limit = fmt.Sprintf("%d", v.Limit)
			}
		}

		usage := ""
		if slices.Contains(byteLimits, k) {
			usage = units.GetByteSizeStringIEC(v.Usage, 2)
		} else {
			usage = fmt.Sprintf("%d", v.Usage)
		}

		data = append(data, []string{strings.ToUpper(k), limit, usage})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("RESOURCE"),
		i18n.G("LIMIT"),
		i18n.G("USAGE"),
	}

	return cli.RenderTable(c.flagFormat, header, data, projectState)
}
