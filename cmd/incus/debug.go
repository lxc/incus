package main

import (
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/util"
)

type cmdDebug struct {
	global *cmdGlobal
}

func (c *cmdDebug) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Hidden = true
	cmd.Use = cli.U("debug")
	cmd.Short = i18n.G("Debug commands")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Debug commands for instances`))

	debugAttachCmd := cmdDebugMemory{global: c.global, debug: c}
	cmd.AddCommand(debugAttachCmd.command())

	debugNBDCmd := cmdDebugNBD{global: c.global, debug: c}
	cmd.AddCommand(debugNBDCmd.command())

	return cmd
}

type cmdDebugMemory struct {
	global *cmdGlobal
	debug  *cmdDebug

	flagFormat string
}

var cmdDebugMemoryUsage = u.Usage{u.Instance.Remote(), u.Target(u.File)}

func (c *cmdDebugMemory) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("dump-memory", cmdDebugMemoryUsage...)
	cmd.Short = i18n.G("Export a virtual machine's memory state")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Export the current memory state of a running virtual machine into a dump file.
		This can be useful for debugging or analysis purposes.`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus debug dump-memory vm1 memory-dump.elf --format=elf
    Creates an ELF format memory dump of the vm1 instance.`,
	))

	cmd.RunE = c.run
	cli.AddStringFlag(cmd.Flags(), &c.flagFormat, "format|f", "elf", "", i18n.G("Format of memory dump (e.g. elf, win-dmp, kdump-zlib, kdump-raw-zlib, ...)"))

	return cmd
}

func (c *cmdDebugMemory) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdDebugMemoryUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	path := parsed[1].String

	target, err := os.Create(path)
	if err != nil {
		return err
	}

	rc, err := d.GetInstanceDebugMemory(instanceName, c.flagFormat)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to dump instance memory: %w"), err)
	}

	_, err = util.SafeCopy(target, rc)
	if err != nil {
		return err
	}

	return nil
}

type cmdDebugNBD struct {
	global *cmdGlobal
	debug  *cmdDebug

	flagAddress string
}

var cmdDebugNBDUsage = u.Usage{u.Instance.Remote()}

func (c *cmdDebugNBD) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("nbd", cmdDebugNBDUsage...)
	cmd.Short = i18n.G("NBD access to all of a virtual machine's disks")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`NBD access to all of a virtual machine's disks

This exposes all the disks of a running virtual machine over a local NBD
server, with each disk reachable as an NBD export named after its Incus
device name.`,
	))

	cli.AddStringFlag(cmd.Flags(), &c.flagAddress, "address", "", "", i18n.G("Specific address to listen on"))

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

func (c *cmdDebugNBD) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdDebugNBDUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String

	// Check that the instance exists before starting the NBD server.
	_, _, err = d.GetInstance(instanceName)
	if err != nil {
		return err
	}

	// Proxy to a local listener.
	listenAddr := c.flagAddress

	if listenAddr == "" {
		listenAddr = "127.0.0.1:0" // Listen on a random local port if not specified.
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to listen for connection: %w"), err)
	}

	fmt.Printf(i18n.G("NBD listening on %v")+"\n", listener.Addr())

	// Track the active connections, the first one starts the NBD session and the
	// following ones attach to it. The server stops the session when all of its
	// connections are closed.
	var connMu sync.Mutex
	activeConns := 0

	for {
		// Wait for a connection.
		nConn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to accept incoming connection: %w"), err)
		}

		go func() {
			defer func() { _ = nConn.Close() }()

			fmt.Printf(i18n.G("NBD client connected %q")+"\n", nConn.RemoteAddr())
			defer fmt.Printf(i18n.G("NBD client disconnected %q")+"\n", nConn.RemoteAddr())

			connMu.Lock()
			reuse := activeConns > 0
			activeConns++
			connMu.Unlock()

			defer func() {
				connMu.Lock()
				activeConns--
				connMu.Unlock()
			}()

			// Get a connection to the NBD session.
			conn, err := d.GetInstanceNBDConn(instanceName, incus.InstanceNBDArgs{Reuse: reuse})
			if err != nil {
				fmt.Printf(i18n.G("NBD connection failed: %v")+"\n", err)
				return
			}

			defer func() { _ = conn.Close() }()

			// Proxy the traffic.
			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				defer wg.Done()

				_, _ = util.SafeCopy(conn, nConn)
				_ = conn.Close()
				_ = nConn.Close()
			}()

			go func() {
				defer wg.Done()

				_, _ = util.SafeCopy(nConn, conn)
				_ = conn.Close()
				_ = nConn.Close()
			}()

			wg.Wait()
		}()
	}
}
