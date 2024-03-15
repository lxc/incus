package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/internal/version"
)

type cmdGlobal struct {
	flagHelp    bool
	flagVersion bool
}

func main() {
	app := &cobra.Command{}
	app.Use = "incus-simplestreams"
	app.Short = "Maintain and Incus-compatible simplestreams tree"
	app.Long = `Description:
  Maintain an Incus-compatible simplestreams tree

  This tool makes it easy to manage the files on a static image server
  using simplestreams index files as the publishing mechanism.
`
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Global flags.
	globalCmd := cmdGlobal{}
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")

	// Help handling.
	app.SetHelpCommand(&cobra.Command{
		Use:    "no-help",
		Hidden: true,
	})

	// Version handling.
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// add sub-command.
	addCmd := cmdAdd{global: &globalCmd}
	app.AddCommand(addCmd.Command())

	// generate-metadata sub-command.
	generateMetadataCmd := cmdGenerateMetadata{global: &globalCmd}
	app.AddCommand(generateMetadataCmd.Command())

	// list sub-command.
	listCmd := cmdList{global: &globalCmd}
	app.AddCommand(listCmd.Command())

	// remove sub-command.
	removeCmd := cmdRemove{global: &globalCmd}
	app.AddCommand(removeCmd.Command())

	// verify sub-command.
	verifyCmd := cmdVerify{global: &globalCmd}
	app.AddCommand(verifyCmd.Command())

	// Run the main command and handle errors.
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}

// CheckArgs validates the number of arguments passed to the function and shows the help if incorrect.
func (c *cmdGlobal) CheckArgs(cmd *cobra.Command, args []string, minArgs int, maxArgs int) (bool, error) {
	if len(args) < minArgs || (maxArgs != -1 && len(args) > maxArgs) {
		_ = cmd.Help()

		if len(args) == 0 {
			return true, nil
		}

		return true, fmt.Errorf("Invalid number of arguments")
	}

	return false, nil
}
