package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	cli "github.com/lxc/incus/v6/shared/cmd"
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
		This can be useful for debugging or analysis purposes.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus debug dump-memory vm1 memory-dump.elf --format=elf
    Creates an ELF format memory dump of the vm1 instance.`))

	cmd.RunE = c.run
	cmd.Flags().StringVar(&c.flagFormat, "format", "elf", i18n.G("Format of memory dump (e.g. elf, win-dmp, kdump-zlib, kdump-raw-zlib, ...)"))

	return cmd
}

func (c *cmdDebugMemory) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdDebugMemoryUsage.Parse(c.global.conf, cmd, args)
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

	_, err = io.Copy(target, rc)
	if err != nil {
		return err
	}

	return nil
}
