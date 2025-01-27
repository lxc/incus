package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/logger"
)

type cmdGlobal struct {
	flagHelp       bool
	flagVersion    bool
	flagLogVerbose bool
	flagLogDebug   bool
}

// PreRun runs immediately prior to the main Run function.
func (c *cmdGlobal) PreRun(cmd *cobra.Command, args []string) error {
	return logger.InitLogger("", "", c.flagLogVerbose, c.flagLogDebug, nil)
}

func run() error {
	// daemon command (main)
	daemonCmd := cmdDaemon{}
	app := daemonCmd.Command()
	app.Use = "incus-user"
	app.Short = "Incus user project daemon"
	app.Long = `Description:
  Incus user project daemon

  This daemon is used to allow users that aren't considered to be Incus
  administrators access to a personal project with suitable restrictions.
`
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Global flags
	globalCmd := cmdGlobal{}
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")
	app.PersistentFlags().BoolVarP(&globalCmd.flagLogVerbose, "verbose", "v", false, "Show all information messages")
	app.PersistentFlags().BoolVarP(&globalCmd.flagLogDebug, "debug", "d", false, "Show debug messages")
	app.PersistentPreRunE = globalCmd.PreRun

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// Run the main command and handle errors
	return app.Execute()
}

func main() {
	err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v", err)
		os.Exit(1)
	}
}
