package main

import (
	"sort"

	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/shared/simplestreams"
)

type cmdList struct {
	global *cmdGlobal

	flagFormat string
}

// Command generates the command definition.
func (c *cmdList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "list"
	cmd.Short = "List all images on the server"
	cmd.Long = cli.FormatSection("Description",
		`List all image on the server

This renders a table with all images currently published on the server.
`)
	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", "Format (csv|json|table|yaml|compact)"+"``")

	return cmd
}

// Run runs the actual command logic.
func (c *cmdList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 0)
	if exit {
		return err
	}

	// Get a simplestreams client.
	ss := simplestreams.NewLocalClient("")

	// Get all the images.
	images, err := ss.ListImages()
	if err != nil {
		return err
	}

	// Generate the table.
	data := [][]string{}
	for _, image := range images {
		data = append(data, []string{image.Fingerprint, image.Properties["description"], image.Properties["os"], image.Properties["release"], image.Properties["variant"], image.Architecture, image.Type, image.CreatedAt.Format("2006/01/02 15:04 MST")})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"FINGERPRINT",
		"DESCRIPTION",
		"OS",
		"RELEASE",
		"VARIANT",
		"ARCHITECTURE",
		"TYPE",
		"CREATED",
	}

	return cli.RenderTable(c.flagFormat, header, data, data)
}
