package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	liblxc "github.com/lxc/go-lxc"
	"github.com/spf13/cobra"
)

type cmdForkconsole struct {
	global *cmdGlobal
}

func (c *cmdForkconsole) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkconsole <container name> <containers path> <config> <tty> <escape>"
	cmd.Short = "Attach to the console of a container"
	cmd.Long = `Description:
  Attach to the console of a container

  This internal command is used to attach to one of the container's tty devices.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkconsole) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) != 5 {
		_ = cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	name := args[0]
	lxcpath := args[1]
	configPath := args[2]

	ttyNum := strings.TrimPrefix(args[3], "tty=")
	tty, err := strconv.Atoi(ttyNum)
	if err != nil {
		return fmt.Errorf("Failed to retrieve tty number: %q", err)
	}

	escapeNum := strings.TrimPrefix(args[4], "escape=")
	escape, err := strconv.Atoi(escapeNum)
	if err != nil {
		return fmt.Errorf("Failed to retrieve escape character: %q", err)
	}

	d, err := liblxc.NewContainer(name, lxcpath)
	if err != nil {
		return fmt.Errorf("Error initializing container: %q", err)
	}

	err = d.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("Error opening config file: %q", err)
	}

	opts := liblxc.ConsoleOptions{}
	opts.Tty = tty
	opts.StdinFd = uintptr(os.Stdin.Fd())
	opts.StdoutFd = uintptr(os.Stdout.Fd())
	opts.StderrFd = uintptr(os.Stderr.Fd())
	opts.EscapeCharacter = rune(escape)

	err = d.Console(opts)
	if err != nil {
		return fmt.Errorf("Failed running forkconsole: %q", err)
	}

	return nil
}
