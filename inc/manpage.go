package main

import (
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/cyphar/incus/shared"
	cli "github.com/cyphar/incus/shared/cmd"
	"github.com/cyphar/incus/shared/i18n"
)

type cmdManpage struct {
	global *cmdGlobal

	flagFormat string
}

func (c *cmdManpage) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("manpage", i18n.G("<target>"))
	cmd.Short = i18n.G("Generate manpages for all commands")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Generate manpages for all commands`))
	cmd.Hidden = true
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "man", i18n.G("Format (man|md|rest|yaml)")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdManpage) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Generate the documentation.
	switch c.flagFormat {
	case "man":
		header := &doc.GenManHeader{
			Title:   i18n.G("LXD - Command line client"),
			Section: "1",
		}

		opts := doc.GenManTreeOptions{
			Header:           header,
			Path:             shared.HostPathFollow(args[0]),
			CommandSeparator: ".",
		}

		err = doc.GenManTreeFromOpts(c.global.cmd, opts)

	case "md":
		err = doc.GenMarkdownTree(c.global.cmd, shared.HostPathFollow(args[0]))

	case "rest":
		err = doc.GenReSTTree(c.global.cmd, shared.HostPathFollow(args[0]))

	case "yaml":
		err = doc.GenYamlTree(c.global.cmd, shared.HostPathFollow(args[0]))
	}

	return err
}
