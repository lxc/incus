package main

/*
#include "config.h"

#include <errno.h>
#include <fcntl.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#include "incus.h"
#include "macro.h"
#include "memory_utils.h"
#include "process_utils.h"

static void forkdonetinfo(int pidfd, int ns_fd)
{
	if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNET)) {
		fprintf(stderr, "Failed setns to container network namespace: %s\n", strerror(errno));
		_exit(1);
	}

	// Jump back to Go for the rest
}

static int dosetns_file(char *file, char *nstype)
{
	__do_close int ns_fd = -EBADF;

	ns_fd = open(file, O_RDONLY);
	if (ns_fd < 0) {
		fprintf(stderr, "%m - Failed to open \"%s\"", file);
		return -1;
	}

	if (setns(ns_fd, 0) < 0) {
		fprintf(stderr, "%m - Failed to attach to namespace \"%s\"", file);
		return -1;
	}

	return 0;
}

static void forkdonetdetach(char *file) {
	// Attach to the network namespace.
	if (dosetns_file(file, "net") < 0) {
		fprintf(stderr, "Failed setns to container network namespace: %s\n", strerror(errno));
		_exit(1);
	}

	if (unshare(CLONE_NEWNS) < 0) {
		fprintf(stderr, "Failed to create new mount namespace: %s\n", strerror(errno));
		_exit(1);
	}

	if (mount(NULL, "/", NULL, MS_REC | MS_PRIVATE, NULL) < 0) {
		fprintf(stderr, "Failed to mark / private: %s\n", strerror(errno));
		_exit(1);
	}

	if (mount("sysfs", "/sys", "sysfs", 0, NULL) < 0) {
		fprintf(stderr, "Failed mounting new sysfs: %s\n", strerror(errno));
		_exit(1);
	}

	// Jump back to Go for the rest
}

int forknet_dhcp_logfile = -1;

static void forkdonetdhcp(char *logfilestr) {
	char *pidstr;
	char path[PATH_MAX];
	pid_t pid;

	pidstr = getenv("LXC_PID");
	if (!pidstr) {
		fprintf(stderr, "No LXC_PID in environment\n");
		_exit(1);
	}

	// Attach to the network namespace.
	snprintf(path, sizeof(path), "/proc/%s/ns/net", pidstr);
	if (dosetns_file(path, "net") < 0) {
		fprintf(stderr, "Failed setns to container network namespace: %s\n", strerror(errno));
		_exit(1);
	}


	forknet_dhcp_logfile = open(logfilestr, O_WRONLY | O_APPEND);
	if (forknet_dhcp_logfile < 0) {
		fprintf(stderr, "Failed to open logfile %s: %s\n", logfilestr, strerror(errno));
		fprintf(stderr, "Execution will continue but log output will be lost after daemonize\n");
	}

	// Run in the background.
	pid = fork();
	if (pid < 0) {
		fprintf(stderr, "%s - Failed to create new process\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	if (pid > 0) {
		_exit(EXIT_SUCCESS);
	}

	if (!freopen("/dev/null", "r", stdin)) {
		fprintf(stderr, "Failed to reconfigure stdin: %s\n", strerror(errno));
		_exit(1);
	}

	if (!freopen("/dev/null", "w", stdout)) {
		fprintf(stderr, "Failed to reconfigure stdout: %s\n", strerror(errno));
		_exit(1);
	}

	if (!freopen("/dev/null", "w", stderr)) {
		fprintf(stderr, "Failed to reconfigure stderr: %s\n", strerror(errno));
		_exit(1);
	}

	if (setsid() < 0) {
		fprintf(stderr, "%s - Failed to setup new session\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	pid = fork();
	if (pid < 0) {
		fprintf(stderr, "%s - Failed to create new process\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	if (pid > 0) {
		_exit(EXIT_SUCCESS);
	}

	// Set the process title.
	char *workdir = advance_arg(false);
	if (workdir != NULL) {
		char *title = malloc(sizeof(char)*strlen(workdir)+19);
		if (title != NULL) {
			sprintf(title, "[incus dhcp] %s eth0", workdir);
			(void)setproctitle(title);
		}
	}

	// Jump back to Go for the rest
}

void forknet(void)
{
	char *command = NULL;
	char *cur = NULL;
	pid_t pid = 0;


	// Get the subcommand
	command = advance_arg(false);
	if (command == NULL || (strcmp(command, "--help") == 0 || strcmp(command, "--version") == 0 || strcmp(command, "-h") == 0)) {
		return;
	}

	if (strcmp(command, "dhcp") == 0) {
		advance_arg(false); // skip instance directory
		cur = advance_arg(false); // get the logfile path
		forkdonetdhcp(cur);
		return;
	}

	// skip "--"
	advance_arg(true);

	// Get the pid
	cur = advance_arg(false);
	if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0)) {
		return;
	}

	// Check that we're root
	if (geteuid() != 0) {
		fprintf(stderr, "Error: forknet requires root privileges\n");
		_exit(1);
	}

	// Call the subcommands
	if (strcmp(command, "info") == 0) {
		int ns_fd, pidfd;
		pid = atoi(cur);

		pidfd = atoi(advance_arg(true));
		ns_fd = pidfd_nsfd(pidfd, pid);
		if (ns_fd < 0)
			_exit(1);

		forkdonetinfo(pidfd, ns_fd);
	}

	if (strcmp(command, "detach") == 0)
		forkdonetdetach(cur);
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/dhcpv6/nclient6"
	"github.com/insomniacslk/dhcp/iana"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/netutils"
	"github.com/lxc/incus/v6/internal/server/ip"
	_ "github.com/lxc/incus/v6/shared/cgo" // Used by cgo
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

type cmdForknet struct {
	global *cmdGlobal

	applyDNSMu      sync.Mutex
	dhcpv4Lease     *nclient4.Lease
	dhcpv6Lease     *dhcpv6.Message
	instNetworkPath string
}

func (c *cmdForknet) command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forknet"
	cmd.Short = "Perform container network operations"
	cmd.Long = `Description:
  Perform container network operations

  This set of internal commands are used for some container network
  operations which require attaching to the container's network namespace.
`
	cmd.Hidden = true

	// info
	cmdInfo := &cobra.Command{}
	cmdInfo.Use = "info <PID> <PidFd>"
	cmdInfo.Args = cobra.ExactArgs(2)
	cmdInfo.RunE = c.runInfo
	cmd.AddCommand(cmdInfo)

	// detach
	cmdDetach := &cobra.Command{}
	cmdDetach.Use = "detach <netns file> <daemon PID> <ifname> <hostname>"
	cmdDetach.Args = cobra.ExactArgs(4)
	cmdDetach.RunE = c.runDetach
	cmd.AddCommand(cmdDetach)

	// dhclient
	cmdDHCP := &cobra.Command{}
	cmdDHCP.Use = "dhcp <path> <logfile>"
	cmdDHCP.Args = cobra.ExactArgs(2)
	cmdDHCP.RunE = c.runDHCP
	cmd.AddCommand(cmdDHCP)

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

func (c *cmdForknet) runInfo(_ *cobra.Command, _ []string) error {
	hostInterfaces, _ := net.Interfaces()
	networks, err := netutils.NetnsGetifaddrs(-1, hostInterfaces)
	if err != nil {
		return err
	}

	buf, err := json.Marshal(networks)
	if err != nil {
		return err
	}

	fmt.Printf("%s\n", buf)

	return nil
}

// RunDHCP spawns the DHCP client(s) and applies address, route and DNS configuration.
func (c *cmdForknet) runDHCP(_ *cobra.Command, args []string) error {
	logger := logrus.New()
	logger.Level = logrus.DebugLevel

	c.instNetworkPath = args[0]

	if C.forknet_dhcp_logfile >= 0 {
		logger.SetOutput(os.NewFile(uintptr(C.forknet_dhcp_logfile), "incus-dhcp-logfile"))
	} else {
		logger.SetOutput(io.Discard)
	}

	iface := "eth0"

	logger.WithField("interface", iface).Info("running dhcp")

	// Bring the interface up.
	link := &ip.Link{
		Name: iface,
	}

	err := link.SetUp()
	if err != nil {
		logger.WithField("interface", iface).Error("Giving up on DHCP, couldn't bring up interface")
		return nil
	}

	// Read the hostname.
	bb, err := os.ReadFile(filepath.Join(c.instNetworkPath, "hostname"))
	if err != nil {
		logger.WithError(err).Error("Unable to read hostname file")
	}

	hostname := strings.TrimSpace(string(bb))

	// Create PID file.
	err = os.WriteFile(filepath.Join(c.instNetworkPath, "dhcp.pid"), []byte(fmt.Sprintf("%d", os.Getpid())), 0o644)
	if err != nil {
		logger.WithError(err).Error("Giving up on DHCP, couldn't write PID file")
		return err
	}

	errorChannel := make(chan error, 2)
	go c.dhcpRunV4(errorChannel, iface, hostname, logger)
	go c.dhcpRunV6(errorChannel, iface, hostname, logger)

	err1 := <-errorChannel
	if err1 != nil {
		logger.WithError(err1).Error("DHCP client failed")
	}

	err2 := <-errorChannel
	if err2 != nil {
		logger.WithError(err2).Error("DHCP client failed")
	}

	if err1 == nil && err2 == nil {
		return nil
	}

	return fmt.Errorf("DHCP failure, client1=%v client2=%v", err1, err2)
}

func (c *cmdForknet) dhcpRunV4(errorChannel chan error, iface string, hostname string, logger *logrus.Logger) {
	// Try to get a lease.
	client, err := nclient4.New(iface)
	if err != nil {
		logger.WithError(err).Error("Giving up on DHCPv4, couldn't set up client")
		errorChannel <- err
		return
	}

	defer func() { _ = client.Close() }()

	lease, err := client.Request(context.Background(), dhcpv4.WithOption(dhcpv4.OptHostName(hostname)))
	if err != nil {
		logger.WithError(err).WithField("hostname", hostname).
			Error("Giving up on DHCPv4, couldn't get a lease")
		errorChannel <- err
		return
	}

	// Parse the response.
	if lease.Offer == nil {
		logger.WithField("hostname", hostname).
			Error("Giving up on DHCPv4, couldn't get a lease after 5s")
		errorChannel <- err
		return
	}

	if lease.Offer.YourIPAddr == nil || lease.Offer.YourIPAddr.Equal(net.IPv4zero) || lease.Offer.SubnetMask() == nil || len(lease.Offer.Router()) != 1 {
		logger.Error("Giving up on DHCPv4, lease didn't contain required fields")
		errorChannel <- errors.New("Giving up on DHCPv4, lease didn't contain required fields")
		return
	}

	c.applyDNSMu.Lock()
	c.dhcpv4Lease = lease
	c.applyDNSMu.Unlock()

	err = c.dhcpApplyDNS(logger)
	if err != nil {
		logger.WithError(err).Error("Giving up on DHCPv4, error applying DNS")
		errorChannel <- err
		return
	}

	// Network configuration.
	netMask, _ := lease.Offer.SubnetMask().Size()

	addr := &ip.Addr{
		DevName: iface,
		Address: fmt.Sprintf("%s/%d", lease.Offer.YourIPAddr, netMask),
		Family:  ip.FamilyV4,
	}

	err = addr.Add()
	if err != nil {
		logger.WithError(err).Error("Giving up on DHCPv4, couldn't add IP")
		errorChannel <- err
		return
	}

	if lease.Offer.Options.Has(dhcpv4.OptionClasslessStaticRoute) {
		for _, staticRoute := range lease.Offer.ClasslessStaticRoute() {
			route := &ip.Route{
				DevName: iface,
				Route:   staticRoute.Dest.String(),
				Family:  ip.FamilyV4,
			}

			if !staticRoute.Router.IsUnspecified() {
				route.Via = staticRoute.Router.String()
			}

			err = route.Add()
			if err != nil {
				logger.WithError(err).Error("Giving up on DHCPv4, couldn't add classless static route")
				errorChannel <- err
				return
			}
		}
	} else {
		route := &ip.Route{
			DevName: iface,
			Route:   "default",
			Via:     lease.Offer.Router()[0].String(),
			Family:  ip.FamilyV4,
		}

		err = route.Add()
		if err != nil {
			logger.WithError(err).Error("Giving up on DHCPv4, couldn't add default route")
			errorChannel <- err
			return
		}
	}

	// Handle DHCP renewal.
	for {
		// Wait until it's renewal time.
		time.Sleep(lease.Offer.IPAddressRenewalTime(time.Minute))

		// Renew the lease.
		newLease, err := client.Renew(context.Background(), lease, dhcpv4.WithOption(dhcpv4.OptHostName(hostname)))
		if err != nil {
			logger.WithError(err).Error("Giving up on DHCPv4, couldn't renew the lease")
			errorChannel <- err
			return
		}

		lease = newLease
	}
}

func (c *cmdForknet) dhcpRunV6(errorChannel chan error, iface string, hostname string, logger *logrus.Logger) {
	// Wait a couple of seconds for IPv6 link-local.
	time.Sleep(2 * time.Second)

	// Get a new DHCPv6 client.
	client, err := nclient6.New(iface)
	if err != nil {
		logger.WithError(err).Error("Giving up on DHCPv6, couldn't set up client")
		errorChannel <- err
		return
	}

	defer func() { _ = client.Close() }()

	// Try to get a lease.
	advertisement, err := client.Solicit(context.Background(), dhcpv6.WithFQDN(0, hostname))
	if err != nil {
		logger.WithError(err).Error("Giving up on DHCPv6, error during DHCPv6 Solicit")
		errorChannel <- err
		return
	}

	// Check if we're dealing with stateless DHCPv6.
	if advertisement.Options.Status().StatusCode == iana.StatusNoAddrsAvail {
		// Get interface details.
		i, err := net.InterfaceByName(iface)
		if err != nil {
			logger.WithError(err).Error("Giving up on DHCPv6, couldn't get interface details")
			errorChannel <- err
			return
		}

		// Try to get some information.
		infoRequest, err := dhcpv6.NewSolicit(i.HardwareAddr)
		if err != nil {
			logger.WithError(err).Error("Giving up on DHCPv6, error preparing DHCPv6 Info Request")
			errorChannel <- err
			return
		}

		infoRequest.MessageType = dhcpv6.MessageTypeInformationRequest
		infoRequest.Options.Del(dhcpv6.OptionIANA)
		reply, err := client.SendAndRead(context.Background(), nclient6.AllDHCPRelayAgentsAndServers, infoRequest, nclient6.IsMessageType(dhcpv6.MessageTypeReply))
		if err != nil {
			logger.WithError(err).Error("Giving up on DHCPv6, error during DHCPv6 Info Request")
			errorChannel <- err
			return
		}

		// Update DNS.
		c.applyDNSMu.Lock()
		c.dhcpv6Lease = reply
		c.applyDNSMu.Unlock()

		err = c.dhcpApplyDNS(logger)
		if err != nil {
			logger.WithError(err).Error("Giving up on DHCPv6, error applying DNS")
			errorChannel <- err
			return
		}

		// We're dealing with stateless DHCPv6, no need to keep running.
		errorChannel <- nil
		return
	}

	reply, err := client.Request(context.Background(), advertisement, dhcpv6.WithFQDN(0, hostname))
	if err != nil {
		logger.WithError(err).Error("Giving up on DHCPv6, error during DHCPv6 Request")
		errorChannel <- err
		return
	}

	c.applyDNSMu.Lock()
	c.dhcpv6Lease = reply
	c.applyDNSMu.Unlock()

	err = c.dhcpApplyDNS(logger)
	if err != nil {
		logger.WithError(err).Error("Giving up on DHCPv6, error applying DNS")
		errorChannel <- err
		return
	}

	// Network configuration.
	ia := reply.Options.OneIANA()
	if ia == nil {
		logger.Error("Giving up on DHCPv6 renewal, reply missing IANA")
		errorChannel <- errors.New("Giving up on DHCPv6 renewal, reply missing IANA")
		return
	}

	for _, iaaddr := range ia.Options.Addresses() {
		addr := &ip.Addr{
			DevName: iface,
			Address: fmt.Sprintf("%s/64", iaaddr.IPv6Addr),
			Family:  ip.FamilyV6,
		}

		err = addr.Add()
		if err != nil {
			logger.WithError(err).Error("Giving up on DHCPv6, couldn't add IP")
			errorChannel <- err
			return
		}
	}

	// Handle DHCP Renewal.
	for {
		// Wait until it's renewal time.
		t1 := ia.T1
		time.Sleep(t1)

		// Renew the lease.
		var optIAAddrs []dhcpv6.OptIAAddress
		for _, optIAAddr := range ia.Options.Addresses() {
			optIAAddrs = append(optIAAddrs, *optIAAddr)
		}

		modifiers := []dhcpv6.Modifier{
			dhcpv6.WithClientID(reply.Options.ClientID()),
			dhcpv6.WithServerID(reply.Options.ServerID()),
			dhcpv6.WithIAID(ia.IaId),
			dhcpv6.WithIANA(optIAAddrs...),
		}

		renew, err := dhcpv6.NewMessage(modifiers...)
		if err != nil {
			logger.WithError(err).Error("Giving up on DHCv6, couldn't create renew message")
			errorChannel <- err
			return
		}

		renew.MessageType = dhcpv6.MessageTypeRenew

		newReply, err := dhcpv6.NewReplyFromMessage(renew, dhcpv6.WithFQDN(0, hostname))
		if err != nil {
			logger.WithError(err).Error("Giving up on DHCPv6, couldn't renew the lease")
			errorChannel <- err
			return
		}

		reply = newReply
	}
}

func (c *cmdForknet) dhcpApplyDNS(logger *logrus.Logger) error {
	c.applyDNSMu.Lock()
	defer c.applyDNSMu.Unlock()

	// Skip touching resolv.conf if no leases.
	if c.dhcpv4Lease == nil && c.dhcpv6Lease == nil {
		return nil
	}

	// Create resolv.conf.
	f, err := os.Create(filepath.Join(c.instNetworkPath, "resolv.conf"))
	if err != nil {
		logger.WithError(err).Error("Giving up on DHCP, couldn't create resolv.conf")
		return err
	}

	defer f.Close()

	// IPv4 addresses.
	if c.dhcpv4Lease != nil {
		if len(c.dhcpv4Lease.Offer.DNS()) > 0 {
			for _, nameserver := range c.dhcpv4Lease.Offer.DNS() {
				_, err = fmt.Fprintf(f, "nameserver %s\n", nameserver)
				if err != nil {
					logger.WithError(err).Error("Giving up on DHCPv4, couldn't prepare resolv.conf")
					return err
				}
			}

			if c.dhcpv4Lease.Offer.DomainName() != "" {
				_, err = fmt.Fprintf(f, "domain %s\n", c.dhcpv4Lease.Offer.DomainName())
				if err != nil {
					logger.WithError(err).Error("Giving up on DHCPv4, couldn't prepare resolv.conf")
					return err
				}
			}

			if c.dhcpv4Lease.Offer.DomainSearch() != nil && len(c.dhcpv4Lease.Offer.DomainSearch().Labels) > 0 {
				_, err = fmt.Fprintf(f, "search %s\n", strings.Join(c.dhcpv4Lease.Offer.DomainSearch().Labels, ", "))
				if err != nil {
					logger.WithError(err).Error("Giving up on DHCPv4, couldn't prepare resolv.conf")
					return err
				}
			}
		}
	}

	// IPv6 addresses.
	if c.dhcpv6Lease != nil {
		if len(c.dhcpv6Lease.Options.DNS()) > 0 {
			for _, nameserver := range c.dhcpv6Lease.Options.DNS() {
				_, err = fmt.Fprintf(f, "nameserver %s\n", nameserver)
				if err != nil {
					logger.WithError(err).Error("Giving up on DHCPv6, couldn't prepare resolv.conf")
					return err
				}
			}

			if c.dhcpv6Lease.Options.DomainSearchList() != nil && len(c.dhcpv6Lease.Options.DomainSearchList().Labels) > 0 {
				_, err = fmt.Fprintf(f, "search %s\n", strings.Join(c.dhcpv6Lease.Options.DomainSearchList().Labels, ", "))
				if err != nil {
					logger.WithError(err).Error("Giving up on DHCPv6, couldn't prepare resolv.conf")
					return err
				}
			}
		}
	}

	return nil
}

func (c *cmdForknet) runDetach(_ *cobra.Command, args []string) error {
	daemonPID := args[1]
	ifName := args[2]
	hostName := args[3]

	if daemonPID == "" {
		return errors.New("Daemon PID argument is required")
	}

	if ifName == "" {
		return errors.New("ifname argument is required")
	}

	if hostName == "" {
		return errors.New("hostname argument is required")
	}

	// Check if the interface exists.
	if !util.PathExists(fmt.Sprintf("/sys/class/net/%s", ifName)) {
		return fmt.Errorf("Couldn't restore host interface %q as container interface %q couldn't be found", hostName, ifName)
	}

	// Remove all IP addresses from interface before moving to parent netns.
	// This is to avoid any container address config leaking into host.
	addr := &ip.Addr{
		DevName: ifName,
	}

	err := addr.Flush()
	if err != nil {
		return err
	}

	// Set interface down.
	link := &ip.Link{Name: ifName}
	err = link.SetDown()
	if err != nil {
		return err
	}

	// Rename it back to the host name.
	err = link.SetName(hostName)
	if err != nil {
		// If the interface has an altname that matches the target name, this can prevent rename of the
		// interface, so try removing it and trying the rename again if succeeds.
		_, altErr := subprocess.RunCommand("ip", "link", "property", "del", "dev", ifName, "altname", hostName)
		if altErr == nil {
			err = link.SetName(hostName)
		}

		return err
	}

	// Move it back to the host.
	phyPath := fmt.Sprintf("/sys/class/net/%s/phy80211/name", hostName)
	if util.PathExists(phyPath) {
		// Get the phy name.
		phyName, err := os.ReadFile(phyPath)
		if err != nil {
			return err
		}

		// Wifi cards (move the phy instead).
		_, err = subprocess.RunCommand("iw", "phy", strings.TrimSpace(string(phyName)), "set", "netns", daemonPID)
		if err != nil {
			return err
		}
	} else {
		// Regular NICs.
		link = &ip.Link{Name: hostName}
		err = link.SetNetns(daemonPID)
		if err != nil {
			return err
		}
	}

	return nil
}
