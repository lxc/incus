package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/cyphar/incus/client"
)

type cmdShutdown struct {
	global *cmdGlobal

	flagForce   bool
	flagTimeout int
}

func (c *cmdShutdown) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "shutdown"
	cmd.Short = "Tell LXD to shutdown all containers and exit"
	cmd.Long = `Description:
  Tell LXD to shutdown all containers and exit

  This will tell LXD to start a clean shutdown of all containers,
  followed by having itself shutdown and exit.

  This can take quite a while as containers can take a long time to
  shutdown, especially if a non-standard timeout was configured for them.
`
	cmd.RunE = c.Run
	cmd.Flags().IntVarP(&c.flagTimeout, "timeout", "t", 0, "Number of seconds to wait before giving up"+"``")
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, "Force shutdown instead of waiting for running operations to finish"+"``")

	return cmd
}

func (c *cmdShutdown) Run(cmd *cobra.Command, args []string) error {
	connArgs := &incus.ConnectionArgs{
		SkipGetServer: true,
	}

	d, err := incus.ConnectLXDUnix("", connArgs)
	if err != nil {
		return err
	}

	v := url.Values{}
	v.Set("force", strconv.FormatBool(c.flagForce))

	chResult := make(chan error, 1)
	go func() {
		defer close(chResult)

		httpClient, err := d.GetHTTPClient()
		if err != nil {
			chResult <- err
			return
		}

		// Request shutdown, this shouldn't return until daemon has stopped so use a large request timeout.
		httpTransport := httpClient.Transport.(*http.Transport)
		httpTransport.ResponseHeaderTimeout = 3600 * time.Second

		_, _, err = d.RawQuery("PUT", fmt.Sprintf("/internal/shutdown?%s", v.Encode()), nil, "")
		if err != nil {
			chResult <- err
			return
		}
	}()

	if c.flagTimeout > 0 {
		select {
		case err = <-chResult:
			return err
		case <-time.After(time.Second * time.Duration(c.flagTimeout)):
			return fmt.Errorf("LXD still running after %ds timeout", c.flagTimeout)
		}
	}

	return <-chResult
}
