package device

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	liblxc "github.com/lxc/go-lxc"

	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/server/apparmor"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/warningtype"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/device/nictype"
	firewallDrivers "github.com/lxc/incus/internal/server/firewall/drivers"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/ip"
	"github.com/lxc/incus/internal/server/network"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/warnings"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

type proxy struct {
	deviceCommon
}

type proxyProcInfo struct {
	listenPid      string
	listenPidFd    string
	connectPid     string
	connectPidFd   string
	connectAddr    string
	listenAddr     string
	listenAddrGID  string
	listenAddrUID  string
	listenAddrMode string
	securityUID    string
	securityGID    string
	proxyProtocol  string
	inheritFds     []*os.File
}

// CanHotPlug returns whether the device can be managed whilst the instance is running.
func (d *proxy) CanHotPlug() bool {
	return true
}

// validateConfig checks the supplied config for correctness.
func (d *proxy) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	validateAddr := func(input string) error {
		_, err := network.ProxyParseAddr(input)
		return err
	}

	// Supported bind types are: "host" or "instance" (or "guest" or "container", legacy options equivalent to "instance").
	// If an empty value is supplied the default behavior is to assume "host" bind mode.
	validateBind := func(input string) error {
		if !util.ValueInSlice(d.config["bind"], []string{"host", "instance", "guest", "container"}) {
			return fmt.Errorf("Invalid binding side given. Must be \"host\" or \"instance\"")
		}

		return nil
	}

	rules := map[string]func(string) error{
		"listen":         validate.Required(validateAddr),
		"connect":        validate.Required(validateAddr),
		"bind":           validate.Optional(validateBind),
		"mode":           validate.Optional(unixValidOctalFileMode),
		"nat":            validate.Optional(validate.IsBool),
		"gid":            validate.Optional(unixValidUserID),
		"uid":            validate.Optional(unixValidUserID),
		"security.uid":   validate.Optional(unixValidUserID),
		"security.gid":   validate.Optional(unixValidUserID),
		"proxy_protocol": validate.Optional(validate.IsBool),
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	if instConf.Type() == instancetype.VM && util.IsFalseOrEmpty(d.config["nat"]) {
		return fmt.Errorf("Only NAT mode is supported for proxies on VM instances")
	}

	listenAddr, err := network.ProxyParseAddr(d.config["listen"])
	if err != nil {
		return err
	}

	connectAddr, err := network.ProxyParseAddr(d.config["connect"])
	if err != nil {
		return err
	}

	err = d.validateListenAddressConflicts(net.ParseIP(listenAddr.Address))
	if err != nil {
		return err
	}

	if (listenAddr.ConnType != "unix" && len(connectAddr.Ports) > len(listenAddr.Ports)) || (listenAddr.ConnType == "unix" && len(connectAddr.Ports) > 1) {
		// Cannot support single address (or port) -> multiple port.
		return fmt.Errorf("Mismatch between listen port(s) and connect port(s) count")
	}

	if util.IsTrue(d.config["proxy_protocol"]) && (!strings.HasPrefix(d.config["connect"], "tcp") || util.IsTrue(d.config["nat"])) {
		return fmt.Errorf("The PROXY header can only be sent to tcp servers in non-nat mode")
	}

	if (!strings.HasPrefix(d.config["listen"], "unix:") || strings.HasPrefix(d.config["listen"], "unix:@")) &&
		(d.config["uid"] != "" || d.config["gid"] != "" || d.config["mode"] != "") {
		return fmt.Errorf("Only proxy devices for non-abstract unix sockets can carry uid, gid, or mode properties")
	}

	if util.IsTrue(d.config["nat"]) {
		if d.inst != nil {
			// Default project always has networks feature so don't bother loading the project config
			// in that case.
			instProject := d.inst.Project()
			if instProject.Name != api.ProjectDefaultName && util.IsTrue(instProject.Config["features.networks"]) {
				// Prevent use of NAT mode on non-default projects with networks feature.
				// This is because OVN networks don't allow the host to communicate directly with
				// instance NICs and so DNAT rules on the host won't work.
				return fmt.Errorf("NAT mode cannot be used in projects that have the networks feature")
			}
		}

		if d.config["bind"] != "" && d.config["bind"] != "host" {
			return fmt.Errorf("Only host-bound proxies can use NAT")
		}

		// Support TCP <-> TCP and UDP <-> UDP only.
		if listenAddr.ConnType == "unix" || connectAddr.ConnType == "unix" || listenAddr.ConnType != connectAddr.ConnType {
			return fmt.Errorf("Proxying %s <-> %s is not supported when using NAT", listenAddr.ConnType, connectAddr.ConnType)
		}

		listenAddress := net.ParseIP(listenAddr.Address)

		if listenAddress.Equal(net.IPv4zero) || listenAddress.Equal(net.IPv6zero) {
			return fmt.Errorf("Cannot listen on wildcard address %q when in nat mode", listenAddress.String())
		}

		// Records which listen address IP version, as these cannot be mixed in NAT mode.
		listenIPVersion := uint(4)
		if listenAddress.To4() == nil {
			listenIPVersion = 6
		}

		// Check connect address against the listen address IP version and check they match.
		connectAddress := net.ParseIP(connectAddr.Address)
		connectIPVersion := uint(4)
		if connectAddress.To4() == nil {
			connectIPVersion = 6
		}

		if listenIPVersion != connectIPVersion {
			return fmt.Errorf("Cannot mix IP versions between listen and connect in nat mode")
		}
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *proxy) validateEnvironment() error {
	if d.name == "" {
		return fmt.Errorf("Device name cannot be empty")
	}

	return nil
}

// validateListenAddressConflicts checks that the proxy device about to be created does not
// overlap on existing network forward (both entities can't have the same listening address with
// the same port number).
func (d *proxy) validateListenAddressConflicts(proxyListenAddr net.IP) error {
	return d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNetworksForwardsOnUplink, err := tx.GetProjectNetworkForwardListenAddressesOnMember(ctx)
		if err != nil {
			return fmt.Errorf("Failed loading network forward listen addresses: %w", err)
		}

		for _, networks := range projectNetworksForwardsOnUplink {
			for _, listenAddresses := range networks {
				for _, netFwdAddr := range listenAddresses {
					if proxyListenAddr.Equal(net.ParseIP(netFwdAddr)) {
						return fmt.Errorf("Listen address %q conflicts with existing network forward", netFwdAddr)
					}
				}
			}
		}

		return nil
	})
}

// Start is run when the device is added to the instance.
func (d *proxy) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Proxy devices have to be setup once the instance is running.
	runConf := deviceConfig.RunConfig{}
	runConf.PostHooks = []func() error{
		func() error {
			if util.IsTrue(d.config["nat"]) {
				err = d.setupNAT()
				if err != nil {
					return fmt.Errorf("Failed to start device %q: %w", d.name, err)
				}

				return nil // Don't proceed with forkproxy setup.
			}

			proxyValues, err := d.setupProxyProcInfo()
			if err != nil {
				return err
			}

			devFileName := fmt.Sprintf("proxy.%s", d.name)
			pidPath := filepath.Join(d.inst.DevicesPath(), devFileName)
			logFileName := fmt.Sprintf("proxy.%s.log", d.name)
			logPath := filepath.Join(d.inst.LogPath(), logFileName)

			// Load the apparmor profile
			err = apparmor.ForkproxyLoad(d.state.OS, d.inst, d)
			if err != nil {
				return fmt.Errorf("Failed to start device %q: %w", d.name, err)
			}

			// Spawn the daemon using subprocess
			command := d.state.OS.ExecPath
			forkproxyargs := []string{"forkproxy",
				"--",
				proxyValues.listenPid,
				proxyValues.listenPidFd,
				proxyValues.listenAddr,
				proxyValues.connectPid,
				proxyValues.connectPidFd,
				proxyValues.connectAddr,
				proxyValues.listenAddrGID,
				proxyValues.listenAddrUID,
				proxyValues.listenAddrMode,
				proxyValues.securityGID,
				proxyValues.securityUID,
				proxyValues.proxyProtocol,
			}

			p, err := subprocess.NewProcess(command, forkproxyargs, logPath, logPath)
			if err != nil {
				return fmt.Errorf("Failed to start device %q: Failed to creating subprocess: %w", d.name, err)
			}

			p.SetApparmor(apparmor.ForkproxyProfileName(d.inst, d))

			err = p.StartWithFiles(context.Background(), proxyValues.inheritFds)
			if err != nil {
				return fmt.Errorf("Failed to start device %q: Failed running: %s %s: %w", d.name, command, strings.Join(forkproxyargs, " "), err)
			}

			for _, file := range proxyValues.inheritFds {
				_ = file.Close()
			}

			// Poll log file a few times until we see "Started" to indicate successful start.
			for i := 0; i < 10; i++ {
				started, err := d.checkProcStarted(logPath)
				if err != nil {
					_ = p.Stop()
					return fmt.Errorf("Error occurred when starting proxy device: %s", err)
				}

				if started {
					err = p.Save(pidPath)
					if err != nil {
						// Kill Process if started, but could not save the file
						err2 := p.Stop()
						if err != nil {
							return fmt.Errorf("Could not kill subprocess while handling saving error: %s: %s", err, err2)
						}

						return fmt.Errorf("Failed to start device %q: Failed saving subprocess details: %w", d.name, err)
					}

					return nil
				}

				time.Sleep(time.Second)
			}

			_ = p.Stop()
			return fmt.Errorf("Failed to start device %q: Please look in %s", d.name, logPath)
		},
	}

	return &runConf, nil
}

// checkProcStarted checks for the "Started" line in the log file. Returns true if found, false
// if not, and error if any other error occurs.
func (d *proxy) checkProcStarted(logPath string) (bool, error) {
	file, err := os.Open(logPath)
	if err != nil {
		return false, err
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "Status: Started" {
			return true, nil
		}

		if strings.HasPrefix(line, "Error:") {
			return false, fmt.Errorf("%s", line)
		}
	}

	err = scanner.Err()
	if err != nil {
		return false, err
	}

	return false, nil
}

// Stop is run when the device is removed from the instance.
func (d *proxy) Stop() (*deviceConfig.RunConfig, error) {
	// Remove possible iptables entries
	err := d.state.Firewall.InstanceClearProxyNAT(d.inst.Project().Name, d.inst.Name(), d.name)
	if err != nil {
		logger.Errorf("Failed to remove proxy NAT filters: %v", err)
	}

	devFileName := fmt.Sprintf("proxy.%s", d.name)
	devPath := filepath.Join(d.inst.DevicesPath(), devFileName)

	if !util.PathExists(devPath) {
		// There's no proxy process if NAT is enabled
		return nil, nil
	}

	err = d.killProxyProc(devPath)
	if err != nil {
		return nil, err
	}

	// Unload apparmor profile.
	err = apparmor.ForkproxyUnload(d.state.OS, d.inst, d)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (d *proxy) setupNAT() error {
	listenAddr, err := network.ProxyParseAddr(d.config["listen"])
	if err != nil {
		return err
	}

	connectAddr, err := network.ProxyParseAddr(d.config["connect"])
	if err != nil {
		return err
	}

	ipVersion := uint(4)
	if strings.Contains(listenAddr.Address, ":") {
		ipVersion = 6
	}

	var connectIP net.IP
	var hostName string

	for devName, devConfig := range d.inst.ExpandedDevices() {
		if devConfig["type"] != "nic" {
			continue
		}

		nicType, err := nictype.NICType(d.state, d.inst.Project().Name, devConfig)
		if err != nil {
			return err
		}

		// Check if the instance has a NIC with a static IP that is reachable from the host.
		if !util.ValueInSlice(nicType, []string{"bridged", "routed"}) {
			continue
		}

		// Ensure the connect IP matches one of the NIC's static IPs otherwise we could mess with other
		// instance's network traffic. If the wildcard address is supplied as the connect host then the
		// first bridged NIC which has a static IP address defined is selected as the connect host IP.
		if ipVersion == 4 && devConfig["ipv4.address"] != "" {
			if connectAddr.Address == devConfig["ipv4.address"] || connectAddr.Address == "0.0.0.0" {
				connectIP = net.ParseIP(devConfig["ipv4.address"])
			}
		} else if ipVersion == 6 && devConfig["ipv6.address"] != "" {
			if connectAddr.Address == devConfig["ipv6.address"] || connectAddr.Address == "::" {
				connectIP = net.ParseIP(devConfig["ipv6.address"])
			}
		}

		if connectIP != nil {
			// Get host_name of device so we can enable hairpin mode on bridge port.
			hostName = d.inst.ExpandedConfig()[fmt.Sprintf("volatile.%s.host_name", devName)]
			break // Found a match, stop searching.
		}
	}

	if connectIP == nil {
		if connectAddr.Address == "0.0.0.0" || connectAddr.Address == "::" {
			return fmt.Errorf("Instance has no static IPv%d address assigned to be used as the connect IP", ipVersion)
		}

		return fmt.Errorf("Connect IP %q must be one of the instance's static IPv%d addresses", connectAddr.Address, ipVersion)
	}

	// Override the host part of the connectAddr.Addr to the chosen connect IP.
	connectAddr.Address = connectIP.String()

	err = network.BridgeNetfilterEnabled(ipVersion)
	if err != nil {
		msg := fmt.Sprintf("IPv%d bridge netfilter not enabled. Instances using the bridge will not be able to connect to the proxy listen IP", ipVersion)
		d.logger.Warn(msg, logger.Ctx{"err": err})
		err := d.state.DB.Cluster.UpsertWarningLocalNode(d.inst.Project().Name, cluster.TypeInstance, d.inst.ID(), warningtype.ProxyBridgeNetfilterNotEnabled, fmt.Sprintf("%s: %v", msg, err))
		if err != nil {
			logger.Warn("Failed to create warning", logger.Ctx{"err": err})
		}
	} else {
		err = warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(d.state.DB.Cluster, d.inst.Project().Name, warningtype.ProxyBridgeNetfilterNotEnabled, cluster.TypeInstance, d.inst.ID())
		if err != nil {
			logger.Warn("Failed to resolve warning", logger.Ctx{"err": err})
		}

		if hostName == "" {
			return fmt.Errorf("Proxy cannot find bridge port host_name to enable hairpin mode")
		}

		// br_netfilter is enabled, so we need to enable hairpin mode on instance's bridge port otherwise
		// the instances on the bridge will not be able to connect to the proxy device's listen IP and the
		// NAT rule added by the firewall below to allow instance <-> instance traffic will also not work.
		link := &ip.Link{Name: hostName}
		err = link.BridgeLinkSetHairpin(true)
		if err != nil {
			return fmt.Errorf("Error enabling hairpin mode on bridge port %q: %w", hostName, err)
		}
	}

	// Convert proxy listen & connect addresses for firewall AddressForward.
	addressForward := firewallDrivers.AddressForward{
		Protocol:      listenAddr.ConnType,
		ListenAddress: net.ParseIP(listenAddr.Address),
		ListenPorts:   listenAddr.Ports,
		TargetAddress: net.ParseIP(connectAddr.Address),
		TargetPorts:   connectAddr.Ports,
	}

	err = d.state.Firewall.InstanceSetupProxyNAT(d.inst.Project().Name, d.inst.Name(), d.name, &addressForward)
	if err != nil {
		return err
	}

	return nil
}

func (d *proxy) rewriteHostAddr(addr string) string {
	fields := strings.SplitN(addr, ":", 2)
	proto := fields[0]
	addr = fields[1]

	return fmt.Sprintf("%s:%s", proto, addr)
}

func (d *proxy) setupProxyProcInfo() (*proxyProcInfo, error) {
	cname := project.Instance(d.inst.Project().Name, d.inst.Name())
	cc, err := liblxc.NewContainer(cname, d.state.OS.LxcPath)
	if err != nil {
		return nil, err
	}

	defer func() { _ = cc.Release() }()

	containerPid := strconv.Itoa(cc.InitPid())
	daemonPid := strconv.Itoa(os.Getpid())

	containerPidFd := -1
	daemonPidFd := -1
	var inheritFd []*os.File
	if d.state.OS.PidFds {
		cPidFd, err := cc.InitPidFd()
		if err == nil {
			dPidFd, err := linux.PidFdOpen(os.Getpid(), 0)
			if err == nil {
				inheritFd = []*os.File{cPidFd, dPidFd}
				containerPidFd = 3
				daemonPidFd = 4
			}
		}
	}

	var listenPid, listenPidFd, connectPid, connectPidFd string

	connectAddr := d.config["connect"]
	listenAddr := d.config["listen"]

	switch d.config["bind"] {
	case "host", "":
		listenPid = daemonPid
		listenPidFd = fmt.Sprintf("%d", daemonPidFd)

		connectPid = containerPid
		connectPidFd = fmt.Sprintf("%d", containerPidFd)

		listenAddr = d.rewriteHostAddr(listenAddr)
	case "instance", "guest", "container":
		listenPid = containerPid
		listenPidFd = fmt.Sprintf("%d", containerPidFd)

		connectPid = daemonPid
		connectPidFd = fmt.Sprintf("%d", daemonPidFd)

		connectAddr = d.rewriteHostAddr(connectAddr)
	default:
		return nil, fmt.Errorf("Invalid binding side given. Must be \"host\" or \"instance\"")
	}

	listenAddrMode := "0644"
	if d.config["mode"] != "" {
		listenAddrMode = d.config["mode"]
	}

	p := &proxyProcInfo{
		listenPid:      listenPid,
		listenPidFd:    listenPidFd,
		connectPid:     connectPid,
		connectPidFd:   connectPidFd,
		connectAddr:    connectAddr,
		listenAddr:     listenAddr,
		listenAddrGID:  d.config["gid"],
		listenAddrUID:  d.config["uid"],
		listenAddrMode: listenAddrMode,
		securityGID:    d.config["security.gid"],
		securityUID:    d.config["security.uid"],
		proxyProtocol:  d.config["proxy_protocol"],
		inheritFds:     inheritFd,
	}

	return p, nil
}

func (d *proxy) killProxyProc(pidPath string) error {
	// If the pid file doesn't exist, there is no process to kill.
	if !util.PathExists(pidPath) {
		return nil
	}

	p, err := subprocess.ImportProcess(pidPath)
	if err != nil {
		return fmt.Errorf("Could not read pid file: %s", err)
	}

	err = p.Stop()
	if err != nil && err != subprocess.ErrNotRunning {
		return fmt.Errorf("Unable to kill forkproxy: %s", err)
	}

	_ = os.Remove(pidPath)
	return nil
}

func (d *proxy) Remove() error {
	err := warnings.DeleteWarningsByLocalNodeAndProjectAndTypeAndEntity(d.state.DB.Cluster, d.inst.Project().Name, warningtype.ProxyBridgeNetfilterNotEnabled, cluster.TypeInstance, d.inst.ID())
	if err != nil {
		logger.Warn("Failed to delete warning", logger.Ctx{"err": err})
	}

	// Delete apparmor profile.
	err = apparmor.ForkproxyDelete(d.state.OS, d.inst, d)
	if err != nil {
		return err
	}

	return nil
}
