//go:build linux

package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/internal/ports"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
)

type cmdAdminInit struct {
	global *cmdGlobal

	flagAuto    bool
	flagMinimal bool
	flagPreseed bool
	flagDump    bool

	flagNetworkAddress  string
	flagNetworkPort     int
	flagStorageBackend  string
	flagStorageDevice   string
	flagStorageLoopSize int
	flagStoragePool     string
}

var cmdAdminInitUsage = u.Usage{u.Sequence(u.Flag("preseed"), u.Placeholder(i18n.G("preseed.yaml")).Optional()).Optional()}

func (c *cmdAdminInit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("init", cmdAdminInitUsage...)
	cmd.Short = i18n.G("Configure the daemon")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Configure the daemon`))
	cmd.Example = `  init --minimal
  init --auto [--network-address=IP] [--network-port=8443] [--storage-backend=dir]
              [--storage-create-device=DEVICE] [--storage-create-loop=SIZE]
              [--storage-pool=POOL]
  init --preseed [preseed.yaml]
  init --dump
`
	cmd.RunE = c.run
	cli.AddBoolFlag(cmd.Flags(), &c.flagAuto, "auto", i18n.G("Automatic (non-interactive) mode"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagMinimal, "minimal", i18n.G("Minimal configuration (non-interactive)"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagPreseed, "preseed", i18n.G("Pre-seed mode, expects YAML config from stdin"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagDump, "dump", i18n.G("Dump YAML config to stdout"))

	cli.AddStringFlag(cmd.Flags(), &c.flagNetworkAddress, "network-address", "", "", i18n.G("Address to bind to (default: none)"))
	cli.AddIntFlag(cmd.Flags(), &c.flagNetworkPort, "network-port", -1, fmt.Sprintf(i18n.G("Port to bind to (default: %d)"), ports.HTTPSDefaultPort))
	cli.AddStringFlag(cmd.Flags(), &c.flagStorageBackend, "storage-backend", "", "", i18n.G("Storage backend to use (btrfs, dir, lvm or zfs, default: dir)"))
	cli.AddStringFlag(cmd.Flags(), &c.flagStorageDevice, "storage-create-device", "", "", i18n.G("Setup device based storage using DEVICE"))
	cli.AddIntFlag(cmd.Flags(), &c.flagStorageLoopSize, "storage-create-loop", -1, i18n.G("Setup loop based storage with SIZE in GiB"))
	cli.AddStringFlag(cmd.Flags(), &c.flagStoragePool, "storage-pool", "", "", i18n.G("Storage pool to use or create"))

	return cmd
}

func (c *cmdAdminInit) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdAdminInitUsage, cmd, args)
	if err != nil {
		return err
	}

	// Quick checks.
	if c.flagAuto && c.flagPreseed {
		return errors.New(i18n.G("Can't use --auto and --preseed together"))
	}

	if c.flagMinimal && c.flagPreseed {
		return errors.New(i18n.G("Can't use --minimal and --preseed together"))
	}

	if c.flagMinimal && c.flagAuto {
		return errors.New(i18n.G("Can't use --minimal and --auto together"))
	}

	if !c.flagAuto && (c.flagNetworkAddress != "" || c.flagNetworkPort != -1 ||
		c.flagStorageBackend != "" || c.flagStorageDevice != "" ||
		c.flagStorageLoopSize != -1 || c.flagStoragePool != "") {
		return errors.New(i18n.G("Configuration flags require --auto"))
	}

	if c.flagDump && (c.flagAuto || c.flagMinimal ||
		c.flagPreseed || c.flagNetworkAddress != "" ||
		c.flagNetworkPort != -1 || c.flagStorageBackend != "" ||
		c.flagStorageDevice != "" || c.flagStorageLoopSize != -1 ||
		c.flagStoragePool != "") {
		return errors.New(i18n.G("Can't use --dump with other flags"))
	}

	// Connect to the daemon
	d, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to connect to local daemon: %w"), err)
	}

	server, _, err := d.GetServer()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to connect to get server info: %w"), err)
	}

	// Dump mode
	if c.flagDump {
		err := c.runDump(d)
		if err != nil {
			return err
		}

		return nil
	}

	// Prepare the input data
	var config *api.InitPreseed

	switch {
	case c.flagPreseed:
		config, err = c.runPreseed(parsed[0].List[1])
		if err != nil {
			return err
		}

	case c.flagAuto || c.flagMinimal:
		config, err = c.runAuto(d, server)
		if err != nil {
			return err
		}

	default:
		config, err = c.runInteractive(cmd, d, server)
		if err != nil {
			return err
		}
	}

	err = fillClusterConfig(config)
	if err != nil {
		return err
	}

	if config.Cluster != nil && config.Cluster.ClusterAddress != "" && config.Cluster.ServerAddress != "" {
		err = updateCluster(d, config)
		if err != nil {
			return err
		}

		return nil
	}

	return d.ApplyServerPreseed(*config)
}
