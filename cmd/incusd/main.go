package main

import (
	"os"

	dqlite "github.com/cowsql/go-cowsql"
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/rsync"
	"github.com/lxc/incus/v6/internal/server/daemon"
	"github.com/lxc/incus/v6/internal/server/events"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/logger"
)

type cmdGlobal struct {
	cmd *cobra.Command

	flagHelp    bool
	flagVersion bool

	flagLogFile    string
	flagLogDebug   bool
	flagLogSyslog  bool
	flagLogTrace   []string
	flagLogVerbose bool
}

func (c *cmdGlobal) Run(cmd *cobra.Command, args []string) error {
	// Configure dqlite to *not* disable internal SQLite locking, since we
	// use SQLite both through dqlite and through go-dqlite, potentially
	// from different threads at the same time. We need to call this
	// function as early as possible since this is a global setting in
	// SQLite, which can't be changed afterwise.
	err := dqlite.ConfigMultiThread()
	if err != nil {
		return err
	}

	// Set logging global variables
	daemon.Debug = c.flagLogDebug
	rsync.Debug = c.flagLogDebug
	daemon.Verbose = c.flagLogVerbose

	// Set debug for the operations package
	operations.Init(daemon.Debug)

	// Set debug for the response package
	response.Init(daemon.Debug)

	// Setup logger
	syslog := ""
	if c.flagLogSyslog {
		syslog = "incus"
	}

	err = logger.InitLogger(c.flagLogFile, syslog, c.flagLogVerbose, c.flagLogDebug, events.NewEventHandler())
	if err != nil {
		return err
	}

	return nil
}

// rawArgs returns the raw unprocessed arguments from os.Args after the command name arg is found.
func (c *cmdGlobal) rawArgs(cmd *cobra.Command) []string {
	for i, arg := range os.Args {
		if arg == cmd.Name() && len(os.Args)-1 > i {
			return os.Args[i+1:]
		}
	}

	return []string{}
}

func main() {
	// daemon command (main)
	daemonCmd := cmdDaemon{}
	app := daemonCmd.Command()
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Workaround for main command
	app.Args = cobra.ArbitraryArgs

	// Global flags
	globalCmd := cmdGlobal{cmd: app}
	daemonCmd.global = &globalCmd
	app.PersistentPreRunE = globalCmd.Run
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")
	app.PersistentFlags().StringVar(&globalCmd.flagLogFile, "logfile", "", "Path to the log file"+"``")
	app.PersistentFlags().BoolVar(&globalCmd.flagLogSyslog, "syslog", false, "Log to syslog")
	app.PersistentFlags().StringArrayVar(&globalCmd.flagLogTrace, "trace", []string{}, "Log tracing targets"+"``")
	app.PersistentFlags().BoolVarP(&globalCmd.flagLogDebug, "debug", "d", false, "Show all debug messages")
	app.PersistentFlags().BoolVarP(&globalCmd.flagLogVerbose, "verbose", "v", false, "Show all information messages")

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// activateifneeded sub-command
	activateifneededCmd := cmdActivateifneeded{global: &globalCmd}
	app.AddCommand(activateifneededCmd.Command())

	// callhook sub-command
	callhookCmd := cmdCallhook{global: &globalCmd}
	app.AddCommand(callhookCmd.Command())

	// forkconsole sub-command
	forkconsoleCmd := cmdForkconsole{global: &globalCmd}
	app.AddCommand(forkconsoleCmd.Command())

	// forkexec sub-command
	forkexecCmd := cmdForkexec{global: &globalCmd}
	app.AddCommand(forkexecCmd.Command())

	// forkfile sub-command
	forkfileCmd := cmdForkfile{global: &globalCmd}
	app.AddCommand(forkfileCmd.Command())

	// forklimits sub-command
	forklimitsCmd := cmdForklimits{global: &globalCmd}
	app.AddCommand(forklimitsCmd.Command())

	// forkmigrate sub-command
	forkmigrateCmd := cmdForkmigrate{global: &globalCmd}
	app.AddCommand(forkmigrateCmd.Command())

	// forksyscall sub-command
	forksyscallCmd := cmdForksyscall{global: &globalCmd}
	app.AddCommand(forksyscallCmd.Command())

	// forkcoresched sub-command
	forkcoreschedCmd := cmdForkcoresched{global: &globalCmd}
	app.AddCommand(forkcoreschedCmd.Command())

	// forkmount sub-command
	forkmountCmd := cmdForkmount{global: &globalCmd}
	app.AddCommand(forkmountCmd.Command())

	// forknet sub-command
	forknetCmd := cmdForknet{global: &globalCmd}
	app.AddCommand(forknetCmd.Command())

	// forkproxy sub-command
	forkproxyCmd := cmdForkproxy{global: &globalCmd}
	app.AddCommand(forkproxyCmd.Command())

	// forkstart sub-command
	forkstartCmd := cmdForkstart{global: &globalCmd}
	app.AddCommand(forkstartCmd.Command())

	// forkuevent sub-command
	forkueventCmd := cmdForkuevent{global: &globalCmd}
	app.AddCommand(forkueventCmd.Command())

	// forkzfs sub-command
	forkzfsCmd := cmdForkZFS{global: &globalCmd}
	app.AddCommand(forkzfsCmd.Command())

	// manpage sub-command
	manpageCmd := cmdManpage{global: &globalCmd}
	app.AddCommand(manpageCmd.Command())

	// migratedumpsuccess sub-command
	migratedumpsuccessCmd := cmdMigratedumpsuccess{global: &globalCmd}
	app.AddCommand(migratedumpsuccessCmd.Command())

	// netcat sub-command
	netcatCmd := cmdNetcat{global: &globalCmd}
	app.AddCommand(netcatCmd.Command())

	// shutdown sub-command
	shutdownCmd := cmdShutdown{global: &globalCmd}
	app.AddCommand(shutdownCmd.Command())

	// version sub-command
	versionCmd := cmdVersion{global: &globalCmd}
	app.AddCommand(versionCmd.Command())

	// waitready sub-command
	waitreadyCmd := cmdWaitready{global: &globalCmd}
	app.AddCommand(waitreadyCmd.Command())

	// cluster sub-command
	clusterCmd := cmdCluster{global: &globalCmd}
	app.AddCommand(clusterCmd.Command())

	// Run the main command and handle errors
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
