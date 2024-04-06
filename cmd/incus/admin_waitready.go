//go:build linux

package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/logger"
)

type cmdAdminWaitready struct {
	global *cmdGlobal

	flagTimeout int
}

func (c *cmdAdminWaitready) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("waitready")
	cmd.Short = i18n.G("Wait for the daemon to be ready to process requests")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Wait for the daemon to be ready to process requests

  This command will block until the daemon is reachable over its REST API and
  is done with early start tasks like re-starting previously started
  containers.`))
	cmd.RunE = c.Run
	cmd.Flags().IntVarP(&c.flagTimeout, "timeout", "t", 0, "Number of seconds to wait before giving up"+"``")

	return cmd
}

func (c *cmdAdminWaitready) Run(cmd *cobra.Command, args []string) error {
	finger := make(chan error, 1)
	var errLast error
	go func() {
		for i := 0; ; i++ {
			// Start logging only after the 10'th attempt (about 5
			// seconds). Then after the 30'th attempt (about 15
			// seconds), log only only one attempt every 10
			// attempts (about 5 seconds), to avoid being too
			// verbose.
			doLog := false
			if i > 10 {
				doLog = i < 30 || ((i % 10) == 0)
			}

			if doLog {
				logger.Debugf(i18n.G("Connecting to the daemon (attempt %d)"), i)
			}

			d, err := incus.ConnectIncusUnix("", nil)
			if err != nil {
				errLast = err
				if doLog {
					logger.Debugf(i18n.G("Failed connecting to the daemon (attempt %d): %v"), i, err)
				}

				time.Sleep(500 * time.Millisecond)
				continue
			}

			if doLog {
				logger.Debugf(i18n.G("Checking if the daemon is ready (attempt %d)"), i)
			}

			_, _, err = d.RawQuery("GET", "/internal/ready", nil, "")
			if err != nil {
				errLast = err
				if doLog {
					logger.Debugf(i18n.G("Failed to check if the daemon is ready (attempt %d): %v"), i, err)
				}

				time.Sleep(500 * time.Millisecond)
				continue
			}

			finger <- nil
			return
		}
	}()

	if c.flagTimeout > 0 {
		select {
		case <-finger:
			break
		case <-time.After(time.Second * time.Duration(c.flagTimeout)):
			return fmt.Errorf(i18n.G("Daemon still not running after %ds timeout (%v)"), c.flagTimeout, errLast)
		}
	} else {
		<-finger
	}

	return nil
}
