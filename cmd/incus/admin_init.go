//go:build linux

package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/ports"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
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

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminInit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.Usage("init")
	cmd.Short = i18n.G("Configure the daemon")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Configure the daemon`))
	cmd.Example = `  init --minimal
  init --auto [--network-address=IP] [--network-port=8443] [--storage-backend=dir]
              [--storage-create-device=DEVICE] [--storage-create-loop=SIZE]
              [--storage-pool=POOL]
  init --preseed [preseed.yaml]
  init --dump
`
	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagAuto, "auto", false, i18n.G("Automatic (non-interactive) mode"))
	cmd.Flags().BoolVar(&c.flagMinimal, "minimal", false, i18n.G("Minimal configuration (non-interactive)"))
	cmd.Flags().BoolVar(&c.flagPreseed, "preseed", false, i18n.G("Pre-seed mode, expects YAML config from stdin"))
	cmd.Flags().BoolVar(&c.flagDump, "dump", false, i18n.G("Dump YAML config to stdout"))

	cmd.Flags().StringVar(&c.flagNetworkAddress, "network-address", "", i18n.G("Address to bind to (default: none)")+"``")
	cmd.Flags().IntVar(&c.flagNetworkPort, "network-port", -1, fmt.Sprintf(i18n.G("Port to bind to (default: %d)")+"``", ports.HTTPSDefaultPort))
	cmd.Flags().StringVar(&c.flagStorageBackend, "storage-backend", "", i18n.G("Storage backend to use (btrfs, dir, lvm or zfs, default: dir)")+"``")
	cmd.Flags().StringVar(&c.flagStorageDevice, "storage-create-device", "", i18n.G("Setup device based storage using DEVICE")+"``")
	cmd.Flags().IntVar(&c.flagStorageLoopSize, "storage-create-loop", -1, i18n.G("Setup loop based storage with SIZE in GiB")+"``")
	cmd.Flags().StringVar(&c.flagStoragePool, "storage-pool", "", i18n.G("Storage pool to use or create")+"``")

	return cmd
}

// Run runs the actual command logic.
func (c *cmdAdminInit) Run(cmd *cobra.Command, args []string) error {
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
		err := c.RunDump(d)
		if err != nil {
			return err
		}

		return nil
	}

	// Prepare the input data
	var config *api.InitPreseed

	switch {
	case c.flagPreseed:
		config, err = c.RunPreseed(cmd, args)
		if err != nil {
			return err
		}

	case c.flagAuto || c.flagMinimal:
		config, err = c.RunAuto(d, server)
		if err != nil {
			return err
		}

	default:
		config, err = c.RunInteractive(cmd, d, server)
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
