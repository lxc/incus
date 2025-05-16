package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	liblxc "github.com/lxc/go-lxc"
	"github.com/spf13/cobra"
)

type cmdForkmigrate struct {
	global *cmdGlobal
}

func (c *cmdForkmigrate) command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkmigrate <container name> <containers path> <config> <images path> <preserve>"
	cmd.Short = "Restore the container from saved state"
	cmd.Long = `Description:
  Restore the container from saved state

  This internal command is used to start the container as a separate
  process, restoring its recorded state.
`
	cmd.RunE = c.run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkmigrate) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) != 5 {
		_ = cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return errors.New("Missing required arguments")
	}

	// Only root should run this
	if os.Geteuid() != 0 {
		return errors.New("This must be run as root")
	}

	name := args[0]
	lxcpath := args[1]
	configPath := args[2]
	imagesDir := args[3]

	preservesInodes, err := strconv.ParseBool(args[4])
	if err != nil {
		return err
	}

	d, err := liblxc.NewContainer(name, lxcpath)
	if err != nil {
		return err
	}

	err = d.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("Failed loading config file %q: %w", configPath, err)
	}

	/* see https://github.com/golang/go/issues/13155, startContainer, and dc3a229 */
	_ = os.Stdin.Close()
	_ = os.Stdout.Close()
	_ = os.Stderr.Close()

	return d.Migrate(liblxc.MIGRATE_RESTORE, liblxc.MigrateOptions{
		Directory:       imagesDir,
		Verbose:         true,
		PreservesInodes: preservesInodes,
	})
}
