package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/shared/version"
)

type cmdGlobal struct {
	flagHelp    bool
	flagVersion bool
}

func main() {
	// daemon command (main)
	daemonCmd := cmdDaemon{}
	app := daemonCmd.Command()
	app.Use = "lxd-user"
	app.Short = "LXD user project daemon"
	app.Long = `Description:
  LXD user project daemon

  This daemon is used to allow users that aren't considered to be LXD
  administrators access to a personal LXD project with suitable
  restrictions.
`
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Global flags
	globalCmd := cmdGlobal{}
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// Run the main command and handle errors
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
