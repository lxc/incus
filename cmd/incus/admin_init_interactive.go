//go:build linux

package main

import (
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/ports"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/subprocess"
	localtls "github.com/lxc/incus/shared/tls"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

func (c *cmdAdminInit) RunInteractive(cmd *cobra.Command, args []string, d incus.InstanceServer, server *api.Server) (*api.InitPreseed, error) {
	// Initialize config
	config := api.InitPreseed{}
	config.Server.Config = map[string]string{}
	config.Server.Networks = []api.InitNetworksProjectPost{}
	config.Server.StoragePools = []api.StoragePoolsPost{}
	config.Server.Profiles = []api.ProfilesPost{
		{
			Name: "default",
			ProfilePut: api.ProfilePut{
				Config:  map[string]string{},
				Devices: map[string]map[string]string{},
			},
		},
	}

	// Clustering
	err := c.askClustering(&config, d, server)
	if err != nil {
		return nil, err
	}

	// Ask all the other questions
	if config.Cluster == nil || config.Cluster.ClusterAddress == "" {
		// Storage
		err = c.askStorage(&config, d, server)
		if err != nil {
			return nil, err
		}

		// Networking
		err = c.askNetworking(&config, d)
		if err != nil {
			return nil, err
		}

		// Daemon config
		err = c.askDaemon(&config, d, server)
		if err != nil {
			return nil, err
		}
	}

	// Print the YAML
	preSeedPrint, err := c.global.asker.AskBool(i18n.G("Would you like a YAML \"init\" preseed to be printed?")+" (yes/no) [default=no]: ", "no")
	if err != nil {
		return nil, err
	}

	if preSeedPrint {
		var object api.InitPreseed

		// If the user has chosen to join an existing cluster, print
		// only YAML for the cluster section, which is the only
		// relevant one. Otherwise print the regular config.
		if config.Cluster != nil && config.Cluster.ClusterAddress != "" {
			object = api.InitPreseed{}
			object.Cluster = config.Cluster
		} else {
			object = config
		}

		out, err := yaml.Marshal(object)
		if err != nil {
			return nil, fmt.Errorf(i18n.G("Failed to render the config: %w"), err)
		}

		fmt.Printf("%s\n", out)
	}

	return &config, nil
}

func (c *cmdAdminInit) askClustering(config *api.InitPreseed, d incus.InstanceServer, server *api.Server) error {
	clustering, err := c.global.asker.AskBool(i18n.G("Would you like to use clustering?")+" (yes/no) [default=no]: ", "no")
	if err != nil {
		return err
	}

	if clustering {
		config.Cluster = &api.InitClusterPreseed{}
		config.Cluster.Enabled = true

		askForServerName := func() error {
			config.Cluster.ServerName, err = c.global.asker.AskString(fmt.Sprintf(i18n.G("What member name should be used to identify this server in the cluster?")+" [default=%s]: ", c.defaultHostname()), c.defaultHostname(), nil)
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
			if util.ValueInSlice(host, []string{"", "[::]", "0.0.0.0"}) {
				return fmt.Errorf(i18n.G("Invalid IP address or DNS name"))
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

		serverAddress, err := c.global.asker.AskString(fmt.Sprintf(i18n.G("What IP address or DNS name should be used to reach this server?")+" [default=%s]: ", address), address, validateServerAddress)
		if err != nil {
			return err
		}

		serverAddress = internalUtil.CanonicalNetworkAddress(serverAddress, ports.HTTPSDefaultPort)
		config.Server.Config["core.https_address"] = serverAddress

		clusterJoin, err := c.global.asker.AskBool(i18n.G("Are you joining an existing cluster?")+" (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if clusterJoin {
			// Existing cluster
			config.Cluster.ServerAddress = serverAddress

			// Root is required to access the certificate files
			if os.Geteuid() != 0 {
				return fmt.Errorf(i18n.G("Joining an existing cluster requires root privileges"))
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

			clusterJoinToken, err := c.global.asker.AskString(i18n.G("Please provide join token:")+" ", "", validJoinToken)
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
				return fmt.Errorf(i18n.G("Unable to connect to any of the cluster members specified in join token"))
			}

			// Pass the raw join token.
			config.Cluster.ClusterToken = clusterJoinToken

			// Confirm wiping
			clusterWipeMember, err := c.global.asker.AskBool(i18n.G("All existing data is lost when joining a cluster, continue?")+" (yes/no) [default=no] ", "no")
			if err != nil {
				return err
			}

			if !clusterWipeMember {
				return fmt.Errorf(i18n.G("User aborted configuration"))
			}

			// Connect to existing cluster
			serverCert, err := internalUtil.LoadServerCert(internalUtil.VarPath(""))
			if err != nil {
				return err
			}

			err = c.setupClusterTrust(serverCert, config.Cluster.ServerName, config.Cluster.ClusterAddress, config.Cluster.ClusterCertificate, config.Cluster.ClusterToken)
			if err != nil {
				return fmt.Errorf(i18n.G("Failed to setup trust relationship with cluster: %w"), err)
			}

			// Now we have setup trust, don't send to server, othwerwise it will try and setup trust
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
				configValue, err := c.global.asker.AskString(question, "", validate.Optional())
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
	}

	return nil
}

func (c *cmdAdminInit) askNetworking(config *api.InitPreseed, d incus.InstanceServer) error {
	var err error
	localBridgeCreate := false

	if config.Cluster == nil {
		localBridgeCreate, err = c.global.asker.AskBool(i18n.G("Would you like to create a new local network bridge?")+" (yes/no) [default=yes]: ", "yes")
		if err != nil {
			return err
		}
	}

	if !localBridgeCreate {
		useExistingInterface, err := c.global.asker.AskBool(i18n.G("Would you like to use an existing bridge or host interface?")+" (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if useExistingInterface {
			for {
				interfaceName, err := c.global.asker.AskString(i18n.G("Name of the existing bridge or host interface:")+" ", "", nil)
				if err != nil {
					return err
				}

				if !util.PathExists(fmt.Sprintf("/sys/class/net/%s", interfaceName)) {
					fmt.Println(i18n.G("The requested interface doesn't exist. Please choose another one."))
					continue
				}

				// Add to the default profile
				config.Server.Profiles[0].Devices["eth0"] = map[string]string{
					"type":    "nic",
					"nictype": "macvlan",
					"name":    "eth0",
					"parent":  interfaceName,
				}

				if util.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", interfaceName)) {
					config.Server.Profiles[0].Devices["eth0"]["nictype"] = "bridged"
				}

				break
			}
		}

		return nil
	}

	for {
		// Define the network
		net := api.InitNetworksProjectPost{}
		net.Config = map[string]string{}
		net.Project = api.ProjectDefaultName

		// Network name
		net.Name, err = c.global.asker.AskString(i18n.G("What should the new bridge be called?")+" [default=incusbr0]: ", "incusbr0", validate.IsNetworkName)
		if err != nil {
			return err
		}

		_, _, err = d.GetNetwork(net.Name)
		if err == nil {
			fmt.Printf(i18n.G("The requested network bridge \"%s\" already exists. Please choose another name.")+"\n", net.Name)
			continue
		}

		// Add to the default profile
		config.Server.Profiles[0].Devices["eth0"] = map[string]string{
			"type":    "nic",
			"name":    "eth0",
			"network": net.Name,
		}

		// IPv4
		net.Config["ipv4.address"], err = c.global.asker.AskString(i18n.G("What IPv4 address should be used?")+" (CIDR subnet notation, “auto” or “none”) [default=auto]: ", "auto", func(value string) error {
			if util.ValueInSlice(value, []string{"auto", "none"}) {
				return nil
			}

			return validate.Optional(validate.IsNetworkAddressCIDRV4)(value)
		})
		if err != nil {
			return err
		}

		if !util.ValueInSlice(net.Config["ipv4.address"], []string{"auto", "none"}) {
			netIPv4UseNAT, err := c.global.asker.AskBool(i18n.G("Would you like to NAT IPv4 traffic on your bridge?")+" [default=yes]: ", "yes")
			if err != nil {
				return err
			}

			net.Config["ipv4.nat"] = fmt.Sprintf("%v", netIPv4UseNAT)
		}

		// IPv6
		net.Config["ipv6.address"], err = c.global.asker.AskString(i18n.G("What IPv6 address should be used?")+" (CIDR subnet notation, “auto” or “none”) [default=auto]: ", "auto", func(value string) error {
			if util.ValueInSlice(value, []string{"auto", "none"}) {
				return nil
			}

			return validate.Optional(validate.IsNetworkAddressCIDRV6)(value)
		})
		if err != nil {
			return err
		}

		if !util.ValueInSlice(net.Config["ipv6.address"], []string{"auto", "none"}) {
			netIPv6UseNAT, err := c.global.asker.AskBool(i18n.G("Would you like to NAT IPv6 traffic on your bridge?")+" [default=yes]: ", "yes")
			if err != nil {
				return err
			}

			net.Config["ipv6.nat"] = fmt.Sprintf("%v", netIPv6UseNAT)
		}

		// Add the new network
		config.Server.Networks = append(config.Server.Networks, net)
		break
	}

	return nil
}

func (c *cmdAdminInit) askStorage(config *api.InitPreseed, d incus.InstanceServer, server *api.Server) error {
	if config.Cluster != nil {
		localStoragePool, err := c.global.asker.AskBool(i18n.G("Do you want to configure a new local storage pool?")+" (yes/no) [default=yes]: ", "yes")
		if err != nil {
			return err
		}

		if localStoragePool {
			err := c.askStoragePool(config, d, server, internalUtil.PoolTypeLocal)
			if err != nil {
				return err
			}
		}

		remoteStoragePool, err := c.global.asker.AskBool(i18n.G("Do you want to configure a new remote storage pool?")+" (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if remoteStoragePool {
			err := c.askStoragePool(config, d, server, internalUtil.PoolTypeRemote)
			if err != nil {
				return err
			}
		}

		return nil
	}

	storagePool, err := c.global.asker.AskBool(i18n.G("Do you want to configure a new storage pool?")+" (yes/no) [default=yes]: ", "yes")
	if err != nil {
		return err
	}

	if !storagePool {
		return nil
	}

	return c.askStoragePool(config, d, server, internalUtil.PoolTypeAny)
}

func (c *cmdAdminInit) setupClusterTrust(serverCert *localtls.CertInfo, serverName string, targetAddress string, targetCert string, targetToken string) error {
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

func (c *cmdAdminInit) askStoragePool(config *api.InitPreseed, d incus.InstanceServer, server *api.Server, poolType internalUtil.PoolType) error {
	// Figure out the preferred storage driver
	availableBackends := linux.AvailableStorageDrivers(internalUtil.VarPath(), server.Environment.StorageSupportedDrivers, poolType)

	if len(availableBackends) == 0 {
		if poolType != internalUtil.PoolTypeAny {
			return fmt.Errorf(i18n.G("No storage backends available"))
		}

		return fmt.Errorf(i18n.G("No %s storage backends available"), poolType)
	}

	backingFs, err := linux.DetectFilesystem(internalUtil.VarPath())
	if err != nil {
		backingFs = "dir"
	}

	defaultStorage := "dir"
	if backingFs == "btrfs" && util.ValueInSlice("btrfs", availableBackends) {
		defaultStorage = "btrfs"
	} else if util.ValueInSlice("zfs", availableBackends) {
		defaultStorage = "zfs"
	} else if util.ValueInSlice("btrfs", availableBackends) {
		defaultStorage = "btrfs"
	}

	for {
		// Define the pool
		pool := api.StoragePoolsPost{}
		pool.Config = map[string]string{}

		if poolType == internalUtil.PoolTypeAny {
			pool.Name, err = c.global.asker.AskString(i18n.G("Name of the new storage pool")+" [default=default]: ", "default", nil)
			if err != nil {
				return err
			}
		} else {
			pool.Name = string(poolType)
		}

		_, _, err := d.GetStoragePool(pool.Name)
		if err == nil {
			if poolType == internalUtil.PoolTypeAny {
				fmt.Printf(i18n.G("The requested storage pool \"%s\" already exists. Please choose another name.")+"\n", pool.Name)
				continue
			}

			return fmt.Errorf(i18n.G("The %s storage pool already exists"), poolType)
		}

		// Add to the default profile
		if config.Server.Profiles[0].Devices["root"] == nil {
			config.Server.Profiles[0].Devices["root"] = map[string]string{
				"type": "disk",
				"path": "/",
				"pool": pool.Name,
			}
		}

		// Storage backend
		if len(availableBackends) > 1 {
			defaultBackend := defaultStorage
			if poolType == internalUtil.PoolTypeRemote {
				if util.ValueInSlice("ceph", availableBackends) {
					defaultBackend = "ceph"
				} else {
					defaultBackend = availableBackends[0] // Default to first remote driver.
				}
			}

			pool.Driver, err = c.global.asker.AskChoice(fmt.Sprintf(i18n.G("Name of the storage backend to use (%s)")+" [default=%s]: ", strings.Join(availableBackends, ", "), defaultBackend), availableBackends, defaultBackend)
			if err != nil {
				return err
			}
		} else {
			pool.Driver = availableBackends[0]
		}

		// Optimization for dir
		if pool.Driver == "dir" {
			config.Server.StoragePools = append(config.Server.StoragePools, pool)
			break
		}

		// Optimization for btrfs on btrfs
		if pool.Driver == "btrfs" && backingFs == "btrfs" {
			btrfsSubvolume, err := c.global.asker.AskBool(fmt.Sprintf(i18n.G("Would you like to create a new btrfs subvolume under %s?")+" (yes/no) [default=yes]: ", internalUtil.VarPath("")), "yes")
			if err != nil {
				return err
			}

			if btrfsSubvolume {
				pool.Config["source"] = internalUtil.VarPath("storage-pools", pool.Name)
				config.Server.StoragePools = append(config.Server.StoragePools, pool)
				break
			}
		}

		// Optimization for zfs on zfs (when using Ubuntu's bpool/rpool)
		if pool.Driver == "zfs" && backingFs == "zfs" {
			poolName, _ := subprocess.RunCommand("zpool", "get", "-H", "-o", "value", "name", "rpool")
			if strings.TrimSpace(poolName) == "rpool" {
				zfsDataset, err := c.global.asker.AskBool(i18n.G("Would you like to create a new zfs dataset under rpool/incus?")+" (yes/no) [default=yes]: ", "yes")
				if err != nil {
					return err
				}

				if zfsDataset {
					pool.Config["source"] = "rpool/incus"
					config.Server.StoragePools = append(config.Server.StoragePools, pool)
					break
				}
			}
		}

		poolCreate, err := c.global.asker.AskBool(fmt.Sprintf(i18n.G("Create a new %s pool?")+" (yes/no) [default=yes]: ", strings.ToUpper(pool.Driver)), "yes")
		if err != nil {
			return err
		}

		if poolCreate {
			if pool.Driver == "ceph" {
				// Ask for the name of the cluster
				pool.Config["ceph.cluster_name"], err = c.global.asker.AskString(i18n.G("Name of the existing CEPH cluster")+" [default=ceph]: ", "ceph", nil)
				if err != nil {
					return err
				}

				// Ask for the name of the osd pool
				pool.Config["ceph.osd.pool_name"], err = c.global.asker.AskString(i18n.G("Name of the OSD storage pool")+" [default=incus]: ", "incus", nil)
				if err != nil {
					return err
				}

				// Ask for the number of placement groups
				pool.Config["ceph.osd.pg_num"], err = c.global.asker.AskString(i18n.G("Number of placement groups")+" [default=32]: ", "32", nil)
				if err != nil {
					return err
				}
			} else if pool.Driver == "cephfs" {
				// Ask for the name of the cluster
				pool.Config["cephfs.cluster_name"], err = c.global.asker.AskString(i18n.G("Name of the existing CEPHfs cluster")+" [default=ceph]: ", "ceph", nil)
				if err != nil {
					return err
				}

				// Ask for the name of the cluster
				pool.Config["source"], err = c.global.asker.AskString(i18n.G("Name of the CEPHfs volume:")+" ", "", nil)
				if err != nil {
					return err
				}
			} else {
				useEmptyBlockDev, err := c.global.asker.AskBool(i18n.G("Would you like to use an existing empty block device (e.g. a disk or partition)?")+" (yes/no) [default=no]: ", "no")
				if err != nil {
					return err
				}

				if useEmptyBlockDev {
					pool.Config["source"], err = c.global.asker.AskString(i18n.G("Path to the existing block device:")+" ", "", func(path string) error {
						if !linux.IsBlockdevPath(path) {
							return fmt.Errorf(i18n.G("%q is not a block device"), path)
						}

						return nil
					})
					if err != nil {
						return err
					}
				} else {
					st := unix.Statfs_t{}
					err := unix.Statfs(internalUtil.VarPath(), &st)
					if err != nil {
						return fmt.Errorf(i18n.G("Couldn't statfs %s: %w"), internalUtil.VarPath(), err)
					}

					/* choose 5 GiB < x < 30GiB, where x is 20% of the disk size */
					defaultSize := uint64(st.Frsize) * st.Blocks / (1024 * 1024 * 1024) / 5
					if defaultSize > 30 {
						defaultSize = 30
					}

					if defaultSize < 5 {
						defaultSize = 5
					}

					pool.Config["size"], err = c.global.asker.AskString(
						fmt.Sprintf(i18n.G("Size in GiB of the new loop device")+" (1GiB minimum) [default=%dGiB]: ", defaultSize),
						fmt.Sprintf("%dGiB", defaultSize),
						func(input string) error {
							input = strings.Split(input, "GiB")[0]

							result, err := strconv.ParseInt(input, 10, 64)
							if err != nil {
								return err
							}

							if result < 1 {
								return fmt.Errorf(i18n.G("Minimum size is 1GiB"))
							}

							return nil
						},
					)
					if err != nil {
						return err
					}

					if !strings.HasSuffix(pool.Config["size"], "GiB") {
						pool.Config["size"] = fmt.Sprintf("%sGiB", pool.Config["size"])
					}
				}
			}
		} else {
			if pool.Driver == "ceph" {
				// ask for the name of the cluster
				pool.Config["ceph.cluster_name"], err = c.global.asker.AskString(i18n.G("Name of the existing CEPH cluster")+" [default=ceph]: ", "ceph", nil)
				if err != nil {
					return err
				}

				// ask for the name of the existing pool
				pool.Config["source"], err = c.global.asker.AskString(i18n.G("Name of the existing OSD storage pool")+" [default=incus]: ", "incus", nil)
				if err != nil {
					return err
				}

				pool.Config["ceph.osd.pool_name"] = pool.Config["source"]
			} else {
				question := fmt.Sprintf(i18n.G("Name of the existing %s pool or dataset:")+" ", strings.ToUpper(pool.Driver))
				pool.Config["source"], err = c.global.asker.AskString(question, "", nil)
				if err != nil {
					return err
				}
			}
		}

		if pool.Driver == "lvm" {
			_, err := exec.LookPath("thin_check")
			if err != nil {
				fmt.Print("\n" + i18n.G(`The LVM thin provisioning tools couldn't be found.
LVM can still be used without thin provisioning but this will disable over-provisioning,
increase the space requirements and creation time of images, instances and snapshots.

If you wish to use thin provisioning, abort now, install the tools from your Linux distribution
and make sure that your user can see and run the "thin_check" command before running "init" again.`) + "\n\n")
				lvmContinueNoThin, err := c.global.asker.AskBool(i18n.G("Do you want to continue without thin provisioning?")+" (yes/no) [default=yes]: ", "yes")
				if err != nil {
					return err
				}

				if !lvmContinueNoThin {
					return fmt.Errorf(i18n.G("The LVM thin provisioning tools couldn't be found on the system"))
				}

				pool.Config["lvm.use_thinpool"] = "false"
			}
		}

		config.Server.StoragePools = append(config.Server.StoragePools, pool)
		break
	}

	return nil
}

func (c *cmdAdminInit) askDaemon(config *api.InitPreseed, d incus.InstanceServer, server *api.Server) error {
	// Detect lack of uid/gid
	if linux.RunningInUserNS() {
		fmt.Print("\n" + i18n.G(`We detected that you are running inside an unprivileged container.
This means that unless you manually configured your host otherwise,
you will not have enough uids and gids to allocate to your containers.

Your container's own allocation can be re-used to avoid the problem.
Doing so makes your nested containers slightly less safe as they could
in theory attack their parent container and gain more privileges than
they otherwise would.`) + "\n\n")

		shareParentAllocation, err := c.global.asker.AskBool(i18n.G("Would you like to have your containers share their parent's allocation?")+" (yes/no) [default=yes]: ", "yes")
		if err != nil {
			return err
		}

		if shareParentAllocation {
			config.Server.Profiles[0].Config["security.privileged"] = "true"
		}
	}

	// Network listener
	if config.Cluster == nil {
		overNetwork, err := c.global.asker.AskBool(i18n.G("Would you like the server to be available over the network?")+" (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if overNetwork {
			isIPAddress := func(s string) error {
				if s != "all" && net.ParseIP(s) == nil {
					return fmt.Errorf(i18n.G("%q is not an IP address"), s)
				}

				return nil
			}

			netAddr, err := c.global.asker.AskString(i18n.G("Address to bind to (not including port)")+" [default=all]: ", "all", isIPAddress)
			if err != nil {
				return err
			}

			if netAddr == "all" {
				netAddr = "::"
			}

			if net.ParseIP(netAddr).To4() == nil {
				netAddr = fmt.Sprintf("[%s]", netAddr)
			}

			netPort, err := c.global.asker.AskInt(fmt.Sprintf(i18n.G("Port to bind to")+" [default=%d]: ", ports.HTTPSDefaultPort), 1, 65535, fmt.Sprintf("%d", ports.HTTPSDefaultPort), func(netPort int64) error {
				address := internalUtil.CanonicalNetworkAddressFromAddressAndPort(netAddr, int(netPort), ports.HTTPSDefaultPort)

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
			})
			if err != nil {
				return err
			}

			config.Server.Config["core.https_address"] = internalUtil.CanonicalNetworkAddressFromAddressAndPort(netAddr, int(netPort), ports.HTTPSDefaultPort)
		}
	}

	// Ask if the user wants images to be automatically refreshed
	imageStaleRefresh, err := c.global.asker.AskBool(i18n.G("Would you like stale cached images to be updated automatically?")+" (yes/no) [default=yes]: ", "yes")
	if err != nil {
		return err
	}

	if !imageStaleRefresh {
		config.Server.Config["images.auto_update_interval"] = "0"
	}

	return nil
}
