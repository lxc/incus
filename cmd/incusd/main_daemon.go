package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/server/sys"
	"github.com/lxc/incus/v6/shared/logger"
)

type cmdDaemon struct {
	global *cmdGlobal

	// Common options
	flagGroup string
}

func (c *cmdDaemon) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "incusd"
	cmd.Short = "The Incus daemon"
	cmd.Long = `Description:
  The Incus daemon

  This is the incus daemon command line. It's typically started directly by your
  init system and interacted with through a tool like ` + "`incus`" + `.
`
	cmd.RunE = c.Run
	cmd.Flags().StringVar(&c.flagGroup, "group", "", "The group of users that will be allowed to talk to Incus"+"``")

	return cmd
}

func (c *cmdDaemon) Run(cmd *cobra.Command, args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "daemon" && args[0] != "") {
		return fmt.Errorf("unknown command \"%s\" for \"%s\"", args[0], cmd.CommandPath())
	}

	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	neededPrograms := []string{"ip", "rsync", "setfattr", "tar", "unsquashfs", "xz"}
	for _, p := range neededPrograms {
		_, err := exec.LookPath(p)
		if err != nil {
			return err
		}
	}

	defer logger.Info("Daemon stopped")

	conf := defaultDaemonConfig()
	conf.Group = c.flagGroup
	conf.Trace = c.global.flagLogTrace
	d := newDaemon(conf, sys.DefaultOS())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, unix.SIGPWR)
	signal.Notify(sigCh, unix.SIGINT)
	signal.Notify(sigCh, unix.SIGQUIT)
	signal.Notify(sigCh, unix.SIGTERM)

	chIgnore := make(chan os.Signal, 1)
	signal.Notify(chIgnore, unix.SIGHUP)

	err := d.Init()
	if err != nil {
		return err
	}

	for {
		select {
		case sig := <-sigCh:
			logger.Info("Received signal", logger.Ctx{"signal": sig})
			if d.shutdownCtx.Err() != nil {
				logger.Warn("Ignoring signal, shutdown already in progress", logger.Ctx{"signal": sig})
			} else {
				go func() {
					d.shutdownDoneCh <- d.Stop(context.Background(), sig)
				}()
			}

		case err = <-d.shutdownDoneCh:
			return err
		}
	}
}
