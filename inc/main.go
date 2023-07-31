package main

import (
	"fmt"
	"os"
	"os/user"
	"path"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cyphar/incus/client"
	"github.com/cyphar/incus/inc/config"
	"github.com/cyphar/incus/shared"
	cli "github.com/cyphar/incus/shared/cmd"
	"github.com/cyphar/incus/shared/i18n"
	"github.com/cyphar/incus/shared/logger"
	"github.com/cyphar/incus/shared/version"
)

type cmdGlobal struct {
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

func main() {
	// Process aliases
	err := execIfAliases()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Setup the parser
	app := &cobra.Command{}
	app.Use = "lxc"
	app.Short = i18n.G("Command line client for LXD")
	app.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Command line client for LXD

All of LXD's features can be driven through the various commands below.
For help with any of those, simply call them with --help.`))
	app.SilenceUsage = true
	app.SilenceErrors = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Global flags
	globalCmd := cmdGlobal{cmd: app}
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

	// cluster sub-command
	clusterCmd := cmdCluster{global: &globalCmd}
	app.AddCommand(clusterCmd.Command())

	// config sub-command
	configCmd := cmdConfig{global: &globalCmd}
	app.AddCommand(configCmd.Command())

	// console sub-command
	consoleCmd := cmdConsole{global: &globalCmd}
	app.AddCommand(consoleCmd.Command())

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

	// init sub-command
	initCmd := cmdInit{global: &globalCmd}
	app.AddCommand(initCmd.Command())

	// launch sub-command
	launchCmd := cmdLaunch{global: &globalCmd, init: &initCmd}
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

	// profile sub-command
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

	// restore sub-command
	restoreCmd := cmdRestore{global: &globalCmd}
	app.AddCommand(restoreCmd.Command())

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

	// warning sub-command
	warningCmd := cmdWarning{global: &globalCmd}
	app.AddCommand(warningCmd.Command())

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

	// Deal with --all flag and --sub-commands flag
	err = app.ParseFlags(os.Args[1:])
	if err == nil {
		if globalCmd.flagHelpAll {
			// Show all commands
			for _, cmd := range app.Commands() {
				cmd.Hidden = false
			}
		}
		if globalCmd.flagSubCmds {
			app.SetUsageTemplate(usageTemplateSubCmds())
		}
	}

	// Run the main command and handle errors
	err = app.Execute()
	if err != nil {
		// Handle non-Linux systems
		if err == config.ErrNotLinux {
			fmt.Fprintf(os.Stderr, i18n.G(`This client hasn't been configured to use a remote LXD server yet.
As your platform can't run native Linux instances, you must connect to a remote LXD server.

If you already added a remote server, make it the default with "lxc remote switch NAME".
To easily setup a local LXD server in a virtual machine, consider using: https://multipass.run`)+"\n")
			os.Exit(1)
		}

		// Default error handling
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)

		// If custom exit status not set, use default error status.
		if globalCmd.ret == 0 {
			globalCmd.ret = 1
		}
	}

	if globalCmd.ret != 0 {
		os.Exit(globalCmd.ret)
	}
}

func (c *cmdGlobal) PreRun(cmd *cobra.Command, args []string) error {
	var err error

	// If calling the help, skip pre-run
	if cmd.Name() == "help" {
		return nil
	}

	// Figure out the config directory and config path
	var configDir string
	if os.Getenv("LXD_CONF") != "" {
		configDir = os.Getenv("LXD_CONF")
	} else if os.Getenv("HOME") != "" {
		configDir = path.Join(os.Getenv("HOME"), ".config", "lxc")
	} else {
		user, err := user.Current()
		if err != nil {
			return err
		}

		configDir = path.Join(user.HomeDir, ".config", "lxc")
	}

	c.confPath = os.ExpandEnv(path.Join(configDir, "config.yml"))

	// Load the configuration
	if c.flagForceLocal {
		c.conf = config.NewConfig("", true)
	} else if shared.PathExists(c.confPath) {
		c.conf, err = config.LoadConfig(c.confPath)
		if err != nil {
			return err
		}
	} else {
		c.conf = config.NewConfig(filepath.Dir(c.confPath), true)
	}

	// Override the project
	if c.flagProject != "" {
		c.conf.ProjectOverride = c.flagProject
	}

	// Setup password helper
	c.conf.PromptPassword = func(filename string) (string, error) {
		return cli.AskPasswordOnce(fmt.Sprintf(i18n.G("Password for %s: "), filename)), nil
	}

	// If the user is running a command that may attempt to connect to the local daemon
	// and this is the first time the client has been run by the user, then check to see
	// if LXD has been properly configured.  Don't display the message if the var path
	// does not exist (LXD not installed), as the user may be targeting a remote daemon.
	if !c.flagForceLocal && shared.PathExists(shared.VarPath("")) && !shared.PathExists(c.confPath) {
		// Create the config dir so that we don't get in here again for this user.
		err = os.MkdirAll(c.conf.ConfigDir, 0750)
		if err != nil {
			return err
		}

		// Attempt to connect to the local server
		runInit := true
		d, err := lxd.ConnectLXDUnix("", nil)
		if err == nil {
			// Check if server is initialized.
			info, _, err := d.GetServer()
			if err == nil && info.Environment.Storage != "" {
				runInit = false
			}

			// Detect usable project.
			names, err := d.GetProjectNames()
			if err == nil {
				if len(names) == 1 && names[0] != "default" {
					remote := c.conf.Remotes["local"]
					remote.Project = names[0]
					c.conf.Remotes["local"] = remote
				}
			}
		}

		flush := false
		if runInit {
			fmt.Fprintf(os.Stderr, i18n.G("If this is your first time running LXD on this machine, you should also run: lxd init")+"\n")
			flush = true
		}

		if !shared.StringInSlice(cmd.Name(), []string{"init", "launch"}) {
			fmt.Fprintf(os.Stderr, i18n.G(`To start your first container, try: lxc launch ubuntu:22.04
Or for a virtual machine: lxc launch ubuntu:22.04 --vm`)+"\n")
			flush = true
		}

		if flush {
			fmt.Fprintf(os.Stderr, "\n")
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

func (c *cmdGlobal) PostRun(cmd *cobra.Command, args []string) error {
	// Macaroon teardown
	if c.conf != nil && shared.PathExists(c.confPath) {
		// Save cookies and OIDC tokens on exit
		c.conf.SaveCookies()
		c.conf.SaveOIDCTokens()
	}

	return nil
}

type remoteResource struct {
	remote string
	server lxd.InstanceServer
	name   string
}

func (c *cmdGlobal) ParseServers(remotes ...string) ([]remoteResource, error) {
	servers := map[string]lxd.InstanceServer{}
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

func (c *cmdGlobal) CheckArgs(cmd *cobra.Command, args []string, minArgs int, maxArgs int) (bool, error) {
	if len(args) < minArgs || (maxArgs != -1 && len(args) > maxArgs) {
		_ = cmd.Help()

		if len(args) == 0 {
			return true, nil
		}

		return true, fmt.Errorf(i18n.G("Invalid number of arguments"))
	}

	return false, nil
}
