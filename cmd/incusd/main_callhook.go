package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/client"
)

type cmdCallhook struct {
	global *cmdGlobal
}

func (c *cmdCallhook) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "callhook <path> [<instance id>|<instance project> <instance name>] <hook>"
	cmd.Short = "Call container lifecycle hook"
	cmd.Long = `Description:
  Call container lifecycle hook

  This internal command notifies the daemon about a container lifecycle event
  (start, stopns, stop, restart) and blocks until it has been processed.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdCallhook) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) < 2 {
		_ = cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	path := args[0]

	var projectName string
	var instanceRef string
	var hook string

	if len(args) == 3 {
		instanceRef = args[1]
		hook = args[2]
	} else if len(args) == 4 {
		projectName = args[1]
		instanceRef = args[2]
		hook = args[3]
	}

	target := ""

	// Only root should run this.
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Connect to daemon.
	socket := os.Getenv("INCUS_SOCKET")
	if socket == "" {
		socket = filepath.Join(path, "unix.socket")
	}

	clientArgs := incus.ConnectionArgs{
		SkipGetServer: true,
	}

	d, err := incus.ConnectIncusUnix(socket, &clientArgs)
	if err != nil {
		return err
	}

	// Prepare the request URL query parameters.
	v := url.Values{}
	if projectName != "" {
		v.Set("project", projectName)
	}

	if hook == "stop" || hook == "stopns" {
		target = os.Getenv("LXC_TARGET")
		if target == "" {
			target = "unknown"
		}

		v.Set("target", target)
	}

	if hook == "stopns" {
		v.Set("netns", os.Getenv("LXC_NET_NS"))
	}

	// Setup the request.
	response := make(chan error, 1)
	go func() {
		url := fmt.Sprintf("/internal/containers/%s/%s?%s", url.PathEscape(instanceRef), url.PathEscape(fmt.Sprintf("on%s", hook)), v.Encode())
		_, _, err := d.RawQuery("GET", url, nil, "")
		response <- err
	}()

	// Handle the timeout.
	select {
	case err := <-response:
		if err != nil {
			return err
		}

		break
	case <-time.After(30 * time.Second):
		return fmt.Errorf("Hook didn't finish within 30s")
	}

	// If the container is rebooting, we purposefully tell LXC that this hook failed so that
	// it won't reboot the container, which lets us start it again in the OnStop function.
	// Other hook types can return without error safely.
	if hook == "stop" && target == "reboot" {
		return fmt.Errorf("Reboot must be handled by Incus")
	}

	return nil
}
