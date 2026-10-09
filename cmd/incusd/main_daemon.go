package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/internal/server/sys"
	"github.com/lxc/incus/v7/shared/logger"
)

type cmdDaemon struct {
	global *cmdGlobal

	// Common options
	flagGroup string
}

func (c *cmdDaemon) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "incusd"
	cmd.Short = "The Incus daemon"
	cmd.Long = `Description:
  The Incus daemon

  This is the incus daemon command line. It's typically started directly by your
  init system and interacted with through a tool like ` + "`incus`" + `.
`
	cmd.RunE = c.run
	cmd.Flags().StringVar(&c.flagGroup, "group", "", "The group of users that will be allowed to talk to Incus"+"``")

	return cmd
}

func (c *cmdDaemon) run(cmd *cobra.Command, args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "daemon" && args[0] != "") {
		return fmt.Errorf("unknown command \"%s\" for \"%s\"", args[0], cmd.CommandPath())
	}

	// Only root should run this
	if os.Geteuid() != 0 {
		return errors.New("This must be run as root")
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
	osInfo := sys.DefaultOS()
	err := os.MkdirAll(osInfo.VarDir, 0o711)
	if err != nil {
		return err
	}

	// Keep the raw descriptor until process exit, including any remaining shutdown goroutines.
	_, err = lockDaemon(osInfo.VarDir)
	if err != nil {
		return err
	}

	d := newDaemon(conf, osInfo)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, unix.SIGPWR)
	signal.Notify(sigCh, unix.SIGINT)
	signal.Notify(sigCh, unix.SIGQUIT)
	signal.Notify(sigCh, unix.SIGTERM)

	chIgnore := make(chan os.Signal, 1)
	signal.Notify(chIgnore, unix.SIGHUP)

	err = d.Init()
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
					d.shutdownDoneCh <- d.Stop(d.shutdownForceCtx, sig)
				}()
			}

		case err = <-d.shutdownDoneCh:
			return err
		}
	}
}

// lockDaemon excludes another daemon for the same data directory independently of listener state.
func lockDaemon(varDir string) (int, error) {
	fd, err := unix.Open(filepath.Join(varDir, "daemon.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return -1, fmt.Errorf("Failed opening daemon lock: %w", err)
	}

	err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("Failed acquiring daemon lock (another daemon may still be running): %w", err)
	}

	return fd, nil
}
