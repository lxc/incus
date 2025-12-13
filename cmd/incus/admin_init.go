//go:build linux

package main

import (
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/ports"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/ask"
	cli "github.com/lxc/incus/v6/shared/cmd"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

// NewInitPreseed creates and initializes a new InitPreseed struct with default values.
func NewInitPressed() *api.InitPreseed {
	// Initialize config
	config := api.InitPreseed{}
	config.Server.Config = map[string]string{}
	config.Server.Networks = []api.InitNetworksProjectPost{}
	config.Server.StoragePools = []api.StoragePoolsPost{}
	config.Server.Profiles = []api.InitProfileProjectPost{
		{
			ProfilesPost: api.ProfilesPost{
				Name: "default",
				ProfilePut: api.ProfilePut{
					Config:  map[string]string{},
					Devices: map[string]map[string]string{},
				},
			},
			Project: api.ProjectDefaultName,
		},
	}

	return &config
}

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

func askClustering(asker ask.Asker, config *api.InitPreseed, server *api.Server, forceJoinExisting bool) error {
	var err error
	config.Cluster = &api.InitClusterPreseed{}
	config.Cluster.Enabled = true

	askForServerName := func() error {
		config.Cluster.ServerName, err = asker.AskString(fmt.Sprintf(i18n.G("What member name should be used to identify this server in the cluster?")+" [default=%s]: ", defaultHostname()), defaultHostname(), nil)
		if err != nil {
			return err
		}

		return nil
	}

	// Cluster server address
	address := internalUtil.NetworkInterfaceAddress()
	validateServerAddress := func(value string) error {
		address := internalUtil.CanonicalNetworkAddress(value, ports.HTTPSDefaultPort)

		host, _, _ := net.SplitHostPort(address)
		if slices.Contains([]string{"", "[::]", "0.0.0.0"}, host) {
			return errors.New(i18n.G("Invalid IP address or DNS name"))
		}

		if err == nil {
			if server.Config["cluster.https_address"] == address || server.Config["core.https_address"] == address {
				// We already own the address, just move on.
				return nil
			}
		}

		listener, err := net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf(i18n.G("Can't bind address %q: %w"), address, err)
		}

		_ = listener.Close()
		return nil
	}

	serverAddress, err := asker.AskString(fmt.Sprintf(i18n.G("What IP address or DNS name should be used to reach this server?")+" [default=%s]: ", address), address, validateServerAddress)
	if err != nil {
		return err
	}

	serverAddress = internalUtil.CanonicalNetworkAddress(serverAddress, ports.HTTPSDefaultPort)
	config.Server.Config["core.https_address"] = serverAddress

	clusterJoin := false
	if !forceJoinExisting {
		clusterJoin, err = asker.AskBool(i18n.G("Are you joining an existing cluster?")+" (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}
	}

	if clusterJoin || forceJoinExisting {
		// Existing cluster
		config.Cluster.ServerAddress = serverAddress

		// Root is required to access the certificate files
		if os.Geteuid() != 0 {
			return errors.New(i18n.G("Joining an existing cluster requires root privileges"))
		}

		var joinToken *api.ClusterMemberJoinToken

		validJoinToken := func(input string) error {
			j, err := internalUtil.JoinTokenDecode(input)
			if err != nil {
				return fmt.Errorf(i18n.G("Invalid join token: %w"), err)
			}

			joinToken = j // Store valid decoded join token
			return nil
		}

		clusterJoinToken, err := asker.AskString(i18n.G("Please provide join token:")+" ", "", validJoinToken)
		if err != nil {
			return err
		}

		// Set server name from join token
		config.Cluster.ServerName = joinToken.ServerName

		// Attempt to find a working cluster member to use for joining by retrieving the
		// cluster certificate from each address in the join token until we succeed.
		for _, clusterAddress := range joinToken.Addresses {
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
			return errors.New(i18n.G("Unable to connect to any of the cluster members specified in join token"))
		}

		// Pass the raw join token.
		config.Cluster.ClusterToken = clusterJoinToken

		// Confirm wiping
		clusterWipeMember, err := asker.AskBool(i18n.G("All existing data is lost when joining a cluster, continue?")+" (yes/no) [default=no] ", "no")
		if err != nil {
			return err
		}

		if !clusterWipeMember {
			return errors.New(i18n.G("User aborted configuration"))
		}

		// Connect to existing cluster
		serverCert, err := internalUtil.LoadServerCert(internalUtil.VarPath(""))
		if err != nil {
			return err
		}

		err = setupClusterTrust(serverCert, config.Cluster.ServerName, config.Cluster.ClusterAddress, config.Cluster.ClusterCertificate, config.Cluster.ClusterToken)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to setup trust relationship with cluster: %w"), err)
		}

		// Now we have setup trust, don't send to server, otherwise it will try and setup trust
		// again and if using a one-time join token, will fail.
		config.Cluster.ClusterToken = ""

		// Client parameters to connect to the target cluster member.
		args := &incus.ConnectionArgs{
			TLSClientCert: string(serverCert.PublicKey()),
			TLSClientKey:  string(serverCert.PrivateKey()),
			TLSServerCert: string(config.Cluster.ClusterCertificate),
			UserAgent:     version.UserAgent,
		}

		client, err := incus.ConnectIncus(fmt.Sprintf("https://%s", config.Cluster.ClusterAddress), args)
		if err != nil {
			return err
		}

		// Get the list of required member config keys.
		cluster, _, err := client.GetCluster()
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to retrieve cluster information: %w"), err)
		}

		for i, config := range cluster.MemberConfig {
			question := fmt.Sprintf(i18n.G("Choose %s:")+" ", config.Description)

			// Allow for empty values.
			configValue, err := asker.AskString(question, "", validate.Optional())
			if err != nil {
				return err
			}

			cluster.MemberConfig[i].Value = configValue
		}

		config.Cluster.MemberConfig = cluster.MemberConfig
	} else {
		// Ask for server name since no token is provided
		err = askForServerName()
		if err != nil {
			return err
		}
	}

	return nil
}

func setupClusterTrust(serverCert *localtls.CertInfo, serverName string, targetAddress string, targetCert string, targetToken string) error {
	// Connect to the target cluster node.
	args := &incus.ConnectionArgs{
		TLSServerCert: targetCert,
		UserAgent:     version.UserAgent,
	}

	target, err := incus.ConnectIncus(fmt.Sprintf("https://%s", targetAddress), args)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to connect to target cluster node %q: %w"), targetAddress, err)
	}

	cert, err := localtls.GenerateTrustCertificate(serverCert, serverName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed generating trust certificate: %w"), err)
	}

	post := api.CertificatesPost{
		CertificatePut: cert.CertificatePut,
		TrustToken:     targetToken,
	}

	err = target.CreateCertificate(post)
	if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
		return fmt.Errorf(i18n.G("Failed to add server cert to cluster: %w"), err)
	}

	return nil
}

func fillClusterConfig(config *api.InitPreseed) error {
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
			return errors.New(i18n.G("Unable to connect to any of the cluster members specified in join token"))
		}
	}

	// If clustering is enabled, and no cluster.https_address network address
	// was specified, we fallback to core.https_address.
	if config.Cluster != nil &&
		config.Server.Config["core.https_address"] != "" &&
		config.Server.Config["cluster.https_address"] == "" {
		config.Server.Config["cluster.https_address"] = config.Server.Config["core.https_address"]
	}

	return nil
}

func updateCluster(d incus.InstanceServer, config *api.InitPreseed) error {
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

	return nil
}

func defaultHostname() string {
	// Cluster server name
	hostName, err := os.Hostname()
	if err != nil {
		hostName = "incus"
	}

	return hostName
}
