package main

import (
	"bufio"
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/ask"
)

type cmdGlobal struct {
	asker ask.Asker

	flagVersion bool
	flagHelp    bool
}

func main() {
	// migrate command (main)
	migrateCmd := cmdMigrate{}
	app := migrateCmd.command()
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Workaround for main command
	app.Args = cobra.ArbitraryArgs

	// Global flags
	globalCmd := cmdGlobal{asker: ask.NewAsker(bufio.NewReader(os.Stdin))}
	migrateCmd.global = &globalCmd
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// netcat sub-command
	netcatCmd := cmdNetcat{global: &globalCmd}
	app.AddCommand(netcatCmd.command())

	// Run the main command and handle errors
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
