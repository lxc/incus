package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Return a new root command.
func newRoot() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate-database",
		Short: "Code generation tool for Incus development",
		Long: `This is the entry point for all "go:generate" directives
used in Incus' source code.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("Not implemented")
		},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}

	cmd.AddCommand(newDb())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}
