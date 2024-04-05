//go:build linux

package main

import (
	"encoding/pem"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/ports"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/util"
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

	hostname string
}

func (c *cmdAdminInit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("init")
	cmd.Short = i18n.G("Configure the daemon")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Configure the daemon`))
	cmd.Example = `  init --minimal
  init --auto [--network-address=IP] [--network-port=8443] [--storage-backend=dir]
              [--storage-create-device=DEVICE] [--storage-create-loop=SIZE]
              [--storage-pool=POOL]
  init --preseed
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

func (c *cmdAdminInit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if c.flagAuto && c.flagPreseed {
		return fmt.Errorf(i18n.G("Can't use --auto and --preseed together"))
	}

	if c.flagMinimal && c.flagPreseed {
		return fmt.Errorf(i18n.G("Can't use --minimal and --preseed together"))
	}

	if c.flagMinimal && c.flagAuto {
		return fmt.Errorf(i18n.G("Can't use --minimal and --auto together"))
	}

	if !c.flagAuto && (c.flagNetworkAddress != "" || c.flagNetworkPort != -1 ||
		c.flagStorageBackend != "" || c.flagStorageDevice != "" ||
		c.flagStorageLoopSize != -1 || c.flagStoragePool != "") {
		return fmt.Errorf(i18n.G("Configuration flags require --auto"))
	}

	if c.flagDump && (c.flagAuto || c.flagMinimal ||
		c.flagPreseed || c.flagNetworkAddress != "" ||
		c.flagNetworkPort != -1 || c.flagStorageBackend != "" ||
		c.flagStorageDevice != "" || c.flagStorageLoopSize != -1 ||
		c.flagStoragePool != "") {
		return fmt.Errorf(i18n.G("Can't use --dump with other flags"))
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

	// Preseed mode
	if c.flagPreseed {
		config, err = c.RunPreseed(cmd, args, d)
		if err != nil {
			return err
		}
	}

	// Auto mode
	if c.flagAuto || c.flagMinimal {
		config, err = c.RunAuto(cmd, args, d, server)
		if err != nil {
			return err
		}
	}

	// Interactive mode
	if !c.flagAuto && !c.flagMinimal && !c.flagPreseed {
		config, err = c.RunInteractive(cmd, args, d, server)
		if err != nil {
			return err
		}
	}

	// Check if the path to the cluster certificate is set
	// If yes then read cluster certificate from file
	if config.Cluster != nil && config.Cluster.ClusterCertificatePath != "" {
		if !util.PathExists(config.Cluster.ClusterCertificatePath) {
			return fmt.Errorf(i18n.G("Path %s doesn't exist"), config.Cluster.ClusterCertificatePath)
		}

		content, err := os.ReadFile(config.Cluster.ClusterCertificatePath)
		if err != nil {
			return err
		}

		config.Cluster.ClusterCertificate = string(content)
	}

	// Check if we got a cluster join token, if so, fill in the config with it.
	if config.Cluster != nil && config.Cluster.ClusterToken != "" {
		joinToken, err := internalUtil.JoinTokenDecode(config.Cluster.ClusterToken)
		if err != nil {
			return fmt.Errorf(i18n.G("Invalid cluster join token: %w"), err)
		}

		// Set server name from join token
		config.Cluster.ServerName = joinToken.ServerName

		// Attempt to find a working cluster member to use for joining by retrieving the
		// cluster certificate from each address in the join token until we succeed.
		for _, clusterAddress := range joinToken.Addresses {
			// Cluster URL
			config.Cluster.ClusterAddress = internalUtil.CanonicalNetworkAddress(clusterAddress, ports.HTTPSDefaultPort)

			// Cluster certificate
			cert, err := localtls.GetRemoteCertificate(fmt.Sprintf("https://%s", config.Cluster.ClusterAddress), version.UserAgent)
			if err != nil {
				fmt.Printf(i18n.G("Error connecting to existing cluster member %q: %v")+"\n", clusterAddress, err)
				continue
			}

			certDigest := localtls.CertFingerprint(cert)
			if joinToken.Fingerprint != certDigest {
				return fmt.Errorf(i18n.G("Certificate fingerprint mismatch between join token and cluster member %q"), clusterAddress)
			}

			config.Cluster.ClusterCertificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))

			break // We've found a working cluster member.
		}

		if config.Cluster.ClusterCertificate == "" {
			return fmt.Errorf(i18n.G("Unable to connect to any of the cluster members specified in join token"))
		}
	}

	// If clustering is enabled, and no cluster.https_address network address
	// was specified, we fallback to core.https_address.
	if config.Cluster != nil &&
		config.Server.Config["core.https_address"] != "" &&
		config.Server.Config["cluster.https_address"] == "" {
		config.Server.Config["cluster.https_address"] = config.Server.Config["core.https_address"]
	}

	// Detect if the user has chosen to join a cluster using the new
	// cluster join API format, and use the dedicated API if so.
	if config.Cluster != nil && config.Cluster.ClusterAddress != "" && config.Cluster.ServerAddress != "" {
		// Ensure the server and cluster addresses are in canonical form.
		config.Cluster.ServerAddress = internalUtil.CanonicalNetworkAddress(config.Cluster.ServerAddress, ports.HTTPSDefaultPort)
		config.Cluster.ClusterAddress = internalUtil.CanonicalNetworkAddress(config.Cluster.ClusterAddress, ports.HTTPSDefaultPort)

		op, err := d.UpdateCluster(config.Cluster.ClusterPut, "")
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to join cluster: %w"), err)
		}

		err = op.Wait()
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to join cluster: %w"), err)
		}

		return nil
	}

	return d.ApplyServerPreseed(*config)
}

func (c *cmdAdminInit) defaultHostname() string {
	if c.hostname != "" {
		return c.hostname
	}

	// Cluster server name
	hostName, err := os.Hostname()
	if err != nil {
		hostName = "incus"
	}

	c.hostname = hostName
	return hostName
}
