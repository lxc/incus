package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/util"
)

// Port forward.
type cmdPortForward struct {
	global *cmdGlobal
}

var cmdPortForwardUsage = u.Usage{u.Instance.Remote(), u.Target(u.Port), u.ListenPort}

func (c *cmdPortForward) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("port-forward", cmdPortForwardUsage...)
	cmd.Short = i18n.G("Forward a local TCP port to the instance")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Forward a local TCP port to the instance

This runs a local TCP listener and forwards every connection made to it
to the given address and port inside of the instance.

Both ports can be prefixed with an address to use ("ADDRESS:PORT"),
defaulting to 127.0.0.1 otherwise. IPv6 addresses must be wrapped in
square brackets (e.g. "[::1]:80").`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus port-forward c1 80 8080
    Forward local port 8080 to port 80 inside of the instance.

incus port-forward c1 10.0.3.1:443 0.0.0.0:8443
    Forward port 8443 on all local addresses to 10.0.3.1:443 inside of the instance.`,
	))

	cmd.RunE = c.run

	// completion for instance.
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// parsePort parses a port argument with an optional address prefix.
func (c *cmdPortForward) parsePort(arg string) (string, int, error) {
	address := "127.0.0.1"
	portStr := arg

	if strings.Contains(arg, ":") {
		var err error

		address, portStr, err = net.SplitHostPort(arg)
		if err != nil {
			return "", -1, err
		}
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return "", -1, fmt.Errorf(i18n.G("Invalid port %q"), portStr)
	}

	return address, port, nil
}

func (c *cmdPortForward) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdPortForwardUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String

	// Parse the target and listen arguments.
	targetAddress, targetPort, err := c.parsePort(parsed[1].String)
	if err != nil {
		return err
	}

	listenAddress, listenPort, err := c.parsePort(parsed[2].String)
	if err != nil {
		return err
	}

	// Check that the instance exists before starting the listener.
	_, _, err = d.GetInstance(instanceName)
	if err != nil {
		return err
	}

	// Start the local listener.
	listener, err := net.Listen("tcp", net.JoinHostPort(listenAddress, strconv.Itoa(listenPort)))
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to listen for connection: %w"), err)
	}

	fmt.Printf(i18n.G("Forwarding %v to %s port %d on %s")+"\n", listener.Addr(), targetAddress, targetPort, instanceName)

	forward := api.InstancePortForwardPost{
		Address: targetAddress,
		Port:    targetPort,
	}

	for {
		// Wait for a connection.
		lConn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to accept incoming connection: %w"), err)
		}

		go func() {
			defer func() { _ = lConn.Close() }()

			// Get a connection to the target.
			conn, err := d.GetInstancePortForwardConn(instanceName, forward)
			if err != nil {
				fmt.Printf(i18n.G("Connection to the instance failed: %v")+"\n", err)
				return
			}

			defer func() { _ = conn.Close() }()

			// Proxy the traffic.
			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				defer wg.Done()

				_, _ = util.SafeCopy(conn, lConn)
				_ = conn.Close()
				_ = lConn.Close()
			}()

			go func() {
				defer wg.Done()

				_, _ = util.SafeCopy(lConn, conn)
				_ = conn.Close()
				_ = lConn.Close()
			}()

			wg.Wait()
		}()
	}
}
