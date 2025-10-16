package main

import (
	"bufio"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/user"
	"path"
	"slices"

	"github.com/kballard/go-shellquote"
	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/ask"
	config "github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

type cmdGlobal struct {
	asker ask.Asker

	conf     *config.Config
	confPath string
	cmd      *cobra.Command
	ret      int

	flagForceLocal bool
	flagHelp       bool
	flagHelpAll    bool
	flagLogDebug   bool
	flagLogVerbose bool
	flagProject    string
	flagQuiet      bool
	flagVersion    bool
	flagSubCmds    bool
}

func usageTemplateSubCmds() string {
	return `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}
Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
  {{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }}  {{.Short}}{{if .HasSubCommands}}{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
    {{rpad .Name .NamePadding }}  {{.Short}}{{if .HasSubCommands}}{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
      {{rpad .Name .NamePadding }}  {{.Short}}{{if .HasSubCommands}}{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
        {{rpad .Name .NamePadding }}  {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{end}}{{end}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
  {{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
}

func aliases() []string {
	c, err := config.LoadConfig("")
	if err != nil {
		return nil
	}

	aliases := make([]string, 0, len(defaultAliases)+len(c.Aliases))

	// Add default aliases
	for alias := range defaultAliases {
		aliases = append(aliases, alias)
	}

	// Add user-defined aliases
	for alias := range c.Aliases {
		aliases = append(aliases, alias)
	}

	return aliases
}

func createApp() (*cobra.Command, *cmdGlobal, error) {
	// Setup the parser
	app := &cobra.Command{}
	app.Use = "incus"
	app.Short = i18n.G("Command line client for Incus")
	app.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Command line client for Incus

All of Incus's features can be driven through the various commands below.
For help with any of those, simply call them with --help.

Custom commands can be defined through aliases, use "incus alias" to control those.`))
	app.SilenceUsage = true
	app.SilenceErrors = true
	app.CompletionOptions = cobra.CompletionOptions{HiddenDefaultCmd: true}
	app.ValidArgs = aliases()

	// Global struct.
	globalCmd := cmdGlobal{cmd: app, asker: ask.NewAsker(bufio.NewReader(os.Stdin))}

	conf, err := config.LoadConfig("")
	if err != nil {
		return nil, nil, fmt.Errorf(i18n.G("Failed to load configuration: %s"), err)
	}

	globalCmd.conf = conf

	// Global flags.
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, i18n.G("Print version number"))
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, i18n.G("Print help"))
	app.PersistentFlags().BoolVar(&globalCmd.flagForceLocal, "force-local", false, i18n.G("Force using the local unix socket"))
	app.PersistentFlags().StringVar(&globalCmd.flagProject, "project", "", i18n.G("Override the source project")+"``")
	app.PersistentFlags().BoolVar(&globalCmd.flagLogDebug, "debug", false, i18n.G("Show all debug messages"))
	app.PersistentFlags().BoolVarP(&globalCmd.flagLogVerbose, "verbose", "v", false, i18n.G("Show all information messages"))
	app.PersistentFlags().BoolVarP(&globalCmd.flagQuiet, "quiet", "q", false, i18n.G("Don't show progress information"))
	app.PersistentFlags().BoolVar(&globalCmd.flagSubCmds, "sub-commands", false, i18n.G("Use with help or --help to view sub-commands"))

	// Wrappers
	app.PersistentPreRunE = globalCmd.PreRun
	app.PersistentPostRunE = globalCmd.PostRun

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// alias sub-command
	aliasCmd := cmdAlias{global: &globalCmd}
	app.AddCommand(aliasCmd.Command())

	// admin sub-command
	adminCmd := cmdAdmin{global: &globalCmd}
	app.AddCommand(adminCmd.Command())

	// bug sub-command
	bugCmd := cmdBug{global: &globalCmd}
	app.AddCommand(bugCmd.Command())

	// cluster sub-command
	clusterCmd := cmdCluster{global: &globalCmd}
	app.AddCommand(clusterCmd.Command())

	// config sub-command
	configCmd := cmdConfig{global: &globalCmd}
	app.AddCommand(configCmd.Command())

	// console sub-command
	consoleCmd := cmdConsole{global: &globalCmd}
	app.AddCommand(consoleCmd.Command())

	// create sub-command
	createCmd := cmdCreate{global: &globalCmd}
	app.AddCommand(createCmd.Command())

	// copy sub-command
	copyCmd := cmdCopy{global: &globalCmd}
	app.AddCommand(copyCmd.Command())

	// delete sub-command
	deleteCmd := cmdDelete{global: &globalCmd}
	app.AddCommand(deleteCmd.Command())

	// exec sub-command
	execCmd := cmdExec{global: &globalCmd}
	app.AddCommand(execCmd.Command())

	// export sub-command
	exportCmd := cmdExport{global: &globalCmd}
	app.AddCommand(exportCmd.Command())

	// file sub-command
	fileCmd := cmdFile{global: &globalCmd}
	app.AddCommand(fileCmd.Command())

	// import sub-command
	importCmd := cmdImport{global: &globalCmd}
	app.AddCommand(importCmd.Command())

	// info sub-command
	infoCmd := cmdInfo{global: &globalCmd}
	app.AddCommand(infoCmd.Command())

	// image sub-command
	imageCmd := cmdImage{global: &globalCmd}
	app.AddCommand(imageCmd.Command())

	// launch sub-command
	launchCmd := cmdLaunch{global: &globalCmd, init: &createCmd}
	app.AddCommand(launchCmd.Command())

	// list sub-command
	listCmd := cmdList{global: &globalCmd}
	app.AddCommand(listCmd.Command())

	// manpage sub-command
	manpageCmd := cmdManpage{global: &globalCmd}
	app.AddCommand(manpageCmd.Command())

	// monitor sub-command
	monitorCmd := cmdMonitor{global: &globalCmd}
	app.AddCommand(monitorCmd.Command())

	// move sub-command
	moveCmd := cmdMove{global: &globalCmd}
	app.AddCommand(moveCmd.Command())

	// network sub-command
	networkCmd := cmdNetwork{global: &globalCmd}
	app.AddCommand(networkCmd.Command())

	// operation sub-command
	operationCmd := cmdOperation{global: &globalCmd}
	app.AddCommand(operationCmd.Command())

	// pause sub-command
	pauseCmd := cmdPause{global: &globalCmd}
	app.AddCommand(pauseCmd.Command())

	// publish sub-command
	publishCmd := cmdPublish{global: &globalCmd}
	app.AddCommand(publishCmd.Command())

	// profile sub-command
	profileCmd := cmdProfile{global: &globalCmd}
	app.AddCommand(profileCmd.Command())

	// project sub-command
	projectCmd := cmdProject{global: &globalCmd}
	app.AddCommand(projectCmd.Command())

	// query sub-command
	queryCmd := cmdQuery{global: &globalCmd}
	app.AddCommand(queryCmd.Command())

	// rebuild sub-command
	rebuildCmd := cmdRebuild{global: &globalCmd}
	app.AddCommand(rebuildCmd.Command())

	// rename sub-command
	renameCmd := cmdRename{global: &globalCmd}
	app.AddCommand(renameCmd.Command())

	// restart sub-command
	restartCmd := cmdRestart{global: &globalCmd}
	app.AddCommand(restartCmd.Command())

	// remote sub-command
	remoteCmd := cmdRemote{global: &globalCmd}
	app.AddCommand(remoteCmd.Command())

	// resume sub-command
	resumeCmd := cmdResume{global: &globalCmd}
	app.AddCommand(resumeCmd.Command())

	// snapshot sub-command
	snapshotCmd := cmdSnapshot{global: &globalCmd}
	app.AddCommand(snapshotCmd.Command())

	// storage sub-command
	storageCmd := cmdStorage{global: &globalCmd}
	app.AddCommand(storageCmd.Command())

	// start sub-command
	startCmd := cmdStart{global: &globalCmd}
	app.AddCommand(startCmd.Command())

	// stop sub-command
	stopCmd := cmdStop{global: &globalCmd}
	app.AddCommand(stopCmd.Command())

	// version sub-command
	versionCmd := cmdVersion{global: &globalCmd}
	app.AddCommand(versionCmd.Command())

	// top sub-command
	topCmd := cmdTop{global: &globalCmd}
	app.AddCommand(topCmd.Command())

	// warning sub-command
	warningCmd := cmdWarning{global: &globalCmd}
	app.AddCommand(warningCmd.Command())

	// webui sub-command
	webuiCmd := cmdWebui{global: &globalCmd}
	app.AddCommand(webuiCmd.Command())

	// debug sub-command
	debugCmd := cmdDebug{global: &globalCmd}
	app.AddCommand(debugCmd.Command())

	// Get help command
	app.InitDefaultHelpCmd()
	var help *cobra.Command
	for _, cmd := range app.Commands() {
		if cmd.Name() == "help" {
			help = cmd
			break
		}
	}

	// Help flags
	app.Flags().BoolVar(&globalCmd.flagHelpAll, "all", false, i18n.G("Show less common commands"))
	help.Flags().BoolVar(&globalCmd.flagHelpAll, "all", false, i18n.G("Show less common commands"))

	return app, &globalCmd, nil
}

func main() {
	app, globalCmd, err := createApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Deal with --all and --sub-commands flags as well as process aliases.
	err = app.ParseFlags(os.Args[1:])
	if err == nil {
		if globalCmd.flagHelpAll {
			// Show all commands
			for _, cmd := range app.Commands() {
				if cmd.Name() == "completion" {
					continue
				}

				cmd.Hidden = false
			}
		}

		if globalCmd.flagSubCmds {
			app.SetUsageTemplate(usageTemplateSubCmds())
		}
	}

	// Process aliases
	err = execIfAliases(app)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Run the main command and handle errors
	err = app.Execute()
	if err != nil {
		// Handle non-Linux systems
		if errors.Is(err, config.ErrNotLinux) {
			fmt.Fprintf(os.Stderr, "%s", i18n.G(`This client hasn't been configured to use a remote server yet.
As your platform can't run native Linux instances, you must connect to a remote server.

If you already added a remote server, make it the default with "incus remote switch NAME".`)+"\n")
			os.Exit(1)
		}

		// Default error handling
		if os.Getenv("INCUS_ALIASES") == "1" {
			fmt.Fprintf(os.Stderr, i18n.G("Error while executing alias expansion: %s\n"), shellquote.Join(os.Args...))
		}

		fmt.Fprintf(os.Stderr, i18n.G("Error: %v\n"), err)

		// If custom exit status not set, use default error status.
		if globalCmd.ret == 0 {
			globalCmd.ret = 1
		}
	}

	if globalCmd.ret != 0 {
		os.Exit(globalCmd.ret)
	}
}

// PreRun runs for every command and pre-configures the CLI.
func (c *cmdGlobal) PreRun(cmd *cobra.Command, _ []string) error {
	var err error

	// If calling the help, skip pre-run
	if cmd.Name() == "help" {
		return nil
	}

	// Figure out a potential cache path.
	var cachePath string
	if os.Getenv("INCUS_CACHE") != "" {
		cachePath = os.Getenv("INCUS_CACHE")
	} else if os.Getenv("HOME") != "" && util.PathExists(os.Getenv("HOME")) {
		cachePath = path.Join(os.Getenv("HOME"), ".cache", "incus")
	} else {
		currentUser, err := user.Current()
		if err != nil {
			return err
		}

		if util.PathExists(currentUser.HomeDir) {
			cachePath = path.Join(currentUser.HomeDir, ".cache", "incus")
		}
	}

	if cachePath != "" {
		err := os.MkdirAll(cachePath, 0o700)
		if err != nil && !os.IsExist(err) {
			cachePath = ""
		}
	}

	// If no config dir could be found, treat as if --force-local was passed.
	if c.conf.ConfigDir == "" {
		c.flagForceLocal = true
	}

	c.confPath = c.conf.ConfigPath("config.yml")

	// Set cache directory in config.
	c.conf.CacheDir = cachePath

	// Override the project
	if c.flagProject != "" {
		c.conf.ProjectOverride = c.flagProject
	} else {
		c.conf.ProjectOverride = os.Getenv("INCUS_PROJECT")
	}

	// Setup password helper
	c.conf.PromptPassword = func(filename string) (string, error) {
		return c.asker.AskPasswordOnce(fmt.Sprintf(i18n.G("Password for %s: "), filename)), nil
	}

	// If the user is running a command that may attempt to connect to the local daemon
	// and this is the first time the client has been run by the user, then check to see
	// if the server has been properly configured.  Don't display the message if the var path
	// does not exist (server missing), as the user may be targeting a remote daemon.
	if !c.flagForceLocal && !util.PathExists(c.confPath) {
		// Create the config dir so that we don't get in here again for this user.
		err = os.MkdirAll(c.conf.ConfigDir, 0o750)
		if err != nil {
			return err
		}

		// Handle local servers.
		if util.PathExists(internalUtil.VarPath("")) {
			// Attempt to connect to the local server
			runInit := true
			d, err := incus.ConnectIncusUnix("", nil)
			if err == nil {
				// Check if server is initialized.
				info, _, err := d.GetServer()
				if err == nil && info.Environment.Storage != "" {
					runInit = false
				}

				// Detect usable project.
				names, err := d.GetProjectNames()
				if err == nil {
					if len(names) == 1 && names[0] != api.ProjectDefaultName {
						remote := c.conf.Remotes["local"]
						remote.Project = names[0]
						c.conf.Remotes["local"] = remote
					}
				}
			}

			flush := false
			if runInit && (cmd.Name() != "init" || cmd.Parent() == nil || cmd.Parent().Name() != "admin") {
				fmt.Fprint(os.Stderr, i18n.G("If this is your first time running Incus on this machine, you should also run: incus admin init")+"\n")
				flush = true
			}

			if !slices.Contains([]string{"admin", "create", "launch"}, cmd.Name()) && (cmd.Parent() == nil || cmd.Parent().Name() != "admin") {
				images := []string{"debian/12", "fedora/42", "opensuse/tumbleweed", "ubuntu/24.04"}
				image := images[rand.Intn(len(images))]

				fmt.Fprintf(os.Stderr, i18n.G(`To start your first container, try: incus launch images:%s
Or for a virtual machine: incus launch images:%s --vm`)+"\n", image, image)
				flush = true
			}

			if flush {
				fmt.Fprint(os.Stderr, "\n")
			}
		}

		// And save the initial configuration
		err = c.conf.SaveConfig(c.confPath)
		if err != nil {
			return err
		}
	}

	// Set the user agent
	c.conf.UserAgent = version.UserAgent

	// Setup the logger
	err = logger.InitLogger("", "", c.flagLogVerbose, c.flagLogDebug, nil)
	if err != nil {
		return err
	}

	return nil
}

// PostRun runs after a successful command.
func (c *cmdGlobal) PostRun(_ *cobra.Command, _ []string) error {
	if c.conf != nil && util.PathExists(c.confPath) {
		// Save OIDC tokens on exit
		c.conf.SaveOIDCTokens()
	}

	return nil
}

type remoteResource struct {
	remote string
	server incus.InstanceServer
	name   string
}

func (c *cmdGlobal) parseServers(remotes ...string) ([]remoteResource, error) {
	servers := map[string]incus.InstanceServer{}
	resources := []remoteResource{}

	for _, remote := range remotes {
		// Parse the remote
		remoteName, name, err := c.conf.ParseRemote(remote)
		if err != nil {
			return nil, err
		}

		// Setup the struct
		resource := remoteResource{
			remote: remoteName,
			name:   name,
		}

		// Look at our cache
		_, ok := servers[remoteName]
		if ok {
			resource.server = servers[remoteName]
			resources = append(resources, resource)
			continue
		}

		// New connection
		d, err := c.conf.GetInstanceServer(remoteName)
		if err != nil {
			return nil, err
		}

		resource.server = d
		servers[remoteName] = d
		resources = append(resources, resource)
	}

	return resources, nil
}

func (c *cmdGlobal) checkArgs(cmd *cobra.Command, args []string, minArgs int, maxArgs int) (bool, error) {
	if len(args) < minArgs || (maxArgs != -1 && len(args) > maxArgs) {
		_ = cmd.Help()

		if len(args) == 0 {
			return true, nil
		}

		return true, errors.New(i18n.G("Invalid number of arguments"))
	}

	return false, nil
}

// Return the default list format if the user configured it, otherwise just return "table".
func (c *cmdGlobal) defaultListFormat() string {
	if c.conf == nil || c.conf.Defaults.ListFormat == "" {
		return "table"
	}

	return c.conf.Defaults.ListFormat
}

// Return "vga" if preferred console type is VGA, otherwise return "console".
func (c *cmdGlobal) defaultConsoleType() string {
	if c.conf == nil || c.conf.Defaults.ConsoleType == "" {
		return "console"
	}

	return c.conf.Defaults.ConsoleType
}

// Return the default console type if the user configured it, otherwise just return "console".
func (c *cmdGlobal) defaultConsoleSpiceCommand() string {
	// Alternative SPICE command.
	if c.conf == nil || c.conf.Defaults.ConsoleSpiceCommand == "" {
		return ""
	}

	return c.conf.Defaults.ConsoleSpiceCommand
}
