package main

import (
	"fmt"
	"os"

	liblxc "github.com/lxc/go-lxc"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/linux"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/util"
)

type cmdForkstart struct {
	global *cmdGlobal
}

func (c *cmdForkstart) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkstart <container name> <containers path> <config>"
	cmd.Short = "Start the container"
	cmd.Long = `Description:
  Start the container

  This internal command is used to start the container as a separate
  process.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkstart) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) != 3 {
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

	err := linux.CloseRange(uint32(os.Stderr.Fd())+1, ^uint32(0), linux.CLOSE_RANGE_CLOEXEC)
	if err != nil {
		return fmt.Errorf("Aborting attach to prevent leaking file descriptors into container")
	}

	d, err := liblxc.NewContainer(name, lxcpath)
	if err != nil {
		return fmt.Errorf("Error initializing container for start: %q", err)
	}

	err = d.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("Error opening startup config file: %q", err)
	}

	/* due to https://github.com/golang/go/issues/13155 and the
	 * CollectOutput call we make for the forkstart process, we need to
	 * close our stdin/stdout/stderr here. Collecting some of the logs is
	 * better than collecting no logs, though.
	 */
	_ = os.Stdin.Close()
	_ = os.Stderr.Close()
	_ = os.Stdout.Close()

	// Redirect stdout and stderr to a log file
	logPath := internalUtil.LogPath(name, "forkstart.log")
	if util.PathExists(logPath) {
		_ = os.Remove(logPath)
	}

	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_SYNC, 0644)
	if err == nil {
		_ = unix.Dup3(int(logFile.Fd()), 1, 0)
		_ = unix.Dup3(int(logFile.Fd()), 2, 0)
	}

	return d.Start()
}
