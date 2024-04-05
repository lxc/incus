package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/version"
)

type cmdGlobal struct {
	flagVersion bool
	flagHelp    bool
}

func main() {
	// shift command (main)
	shiftCmd := cmdShift{}
	app := shiftCmd.Command()
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Global flags
	globalCmd := cmdGlobal{}
	shiftCmd.global = &globalCmd
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
