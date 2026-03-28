package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdManpage struct {
	global *cmdGlobal

	flagFormat string
	flagAll    bool
}

var cmdManpageUsage = u.Usage{u.Target(u.Directory)}

func (c *cmdManpage) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("manpage", cmdManpageUsage...)
	cmd.Short = i18n.G("Generate manpages for all commands")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Generate manpages for all commands`))
	cmd.Hidden = true
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "man", i18n.G("Format (man|md|rest|yaml)")+"``")
	cmd.Flags().BoolVar(&c.flagAll, "all", false, i18n.G("Include less common commands"))

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		format := cmd.Flag("format").Value.String()
		switch format {
		case "man", "md", "rest", "yaml":
			return nil
		default:
			return fmt.Errorf(`Invalid value %q for flag "--format"`, format)
		}
	}

	cmd.RunE = c.run

	return cmd
}

func (c *cmdManpage) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdManpageUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	target := parsed[0].String

	// If asked to do all commands, mark them all visible.
	for _, c := range c.global.cmd.Commands() {
		if c.Name() == "completion" {
			continue
		}

		c.Hidden = false
	}

	// Generate the documentation.
	switch c.flagFormat {
	case "man":
		header := &doc.GenManHeader{
			Title:   i18n.G("Incus - Command line client"),
			Section: "1",
		}

		opts := doc.GenManTreeOptions{
			Header:           header,
			Path:             target,
			CommandSeparator: ".",
		}

		err = doc.GenManTreeFromOpts(c.global.cmd, opts)

	case "md":
		err = doc.GenMarkdownTree(c.global.cmd, target)

	case "rest":
		err = doc.GenReSTTree(c.global.cmd, target)

	case "yaml":
		err = doc.GenYamlTree(c.global.cmd, target)
	}

	return err
}
