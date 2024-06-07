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

static void forkdonetdhcp() {
	char *pidstr;
	char path[PATH_MAX];

	pidstr = getenv("LXC_PID");
	if (!pidstr) {
		fprintf(stderr, "No LXC_PID in environment\n");
		_exit(1);
	}

	snprintf(path, sizeof(path), "/proc/%s/ns/net", pidstr);

	// Attach to the network namespace.
	if (dosetns_file(path, "net") < 0) {
		fprintf(stderr, "Failed setns to container network namespace: %s\n", strerror(errno));
		_exit(1);
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
		forkdonetdhcp();
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
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/client4"
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/netutils"
	"github.com/lxc/incus/v6/internal/server/ip"
	_ "github.com/lxc/incus/v6/shared/cgo" // Used by cgo
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

type cmdForknet struct {
	global *cmdGlobal
}

func (c *cmdForknet) Command() *cobra.Command {
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
	cmdInfo.RunE = c.RunInfo
	cmd.AddCommand(cmdInfo)

	// detach
	cmdDetach := &cobra.Command{}
	cmdDetach.Use = "detach <netns file> <daemon PID> <ifname> <hostname>"
	cmdDetach.Args = cobra.ExactArgs(4)
	cmdDetach.RunE = c.RunDetach
	cmd.AddCommand(cmdDetach)

	// dhclient
	cmdDHCP := &cobra.Command{}
	cmdDHCP.Use = "dhcp <path>"
	cmdDHCP.Args = cobra.ExactArgs(1)
	cmdDHCP.RunE = c.RunDHCP
	cmd.AddCommand(cmdDHCP)

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

func (c *cmdForknet) RunInfo(cmd *cobra.Command, args []string) error {
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

// RunDHCP runs a one time DHCPv4 client and applies address, route and DNS configuration.
func (c *cmdForknet) RunDHCP(cmd *cobra.Command, args []string) error {
	var messages []*dhcpv4.DHCPv4
	iface := "eth0"

	link := &ip.Link{
		Name: iface,
	}

	err := link.SetUp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Giving up on DHCP, couldn't bring up %q\n", iface)
		return nil
	}

	// Try to get a lease.
	client := client4.NewClient()
	for i := 0; i < 10; i++ {
		var err error

		messages, err = client.Exchange(iface)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Parse the response.
	var reply *dhcpv4.DHCPv4
	for _, m := range messages {
		if m.OpCode == dhcpv4.OpcodeBootReply && m.MessageType() == dhcpv4.MessageTypeOffer {
			reply = m
			break
		}
	}

	if reply == nil {
		fmt.Fprintf(os.Stderr, "Giving up on DHCP, couldn't get a lease on %q after 5s\n", iface)
		return nil
	}

	if reply.YourIPAddr == nil || reply.YourIPAddr.Equal(net.IPv4zero) || reply.SubnetMask() == nil || len(reply.Router()) != 1 || len(reply.DNS()) < 1 {
		fmt.Fprintf(os.Stderr, "Giving up on DHCP, lease for %q didn't contain required fields\n", iface)
		return nil
	}

	// Turn into usable configuration.
	netMask, _ := reply.SubnetMask().Size()

	addr := &ip.Addr{
		DevName: iface,
		Address: fmt.Sprintf("%s/%d", reply.YourIPAddr, netMask),
		Family:  ip.FamilyV4,
	}

	err = addr.Add()
	if err != nil {
		return err
	}

	route := &ip.Route{
		DevName: iface,
		Route:   "default",
		Via:     reply.Router()[0].String(),
		Family:  ip.FamilyV4,
	}

	err = route.Add()
	if err != nil {
		return err
	}

	// DNS configuration.
	f, err := os.Create(filepath.Join(args[0], "resolv.conf"))
	if err != nil {
		return err
	}

	defer f.Close()

	for _, nameserver := range reply.DNS() {
		_, err = f.Write([]byte(fmt.Sprintf("nameserver %s\n", nameserver)))
		if err != nil {
			return err
		}
	}

	if reply.DomainName() != "" {
		_, err = f.Write([]byte(fmt.Sprintf("domain %s\n", reply.DomainName())))
		if err != nil {
			return err
		}
	}

	if reply.DomainSearch() != nil && len(reply.DomainSearch().Labels) > 0 {
		_, err = f.Write([]byte(fmt.Sprintf("search %s\n", strings.Join(reply.DomainSearch().Labels, ", "))))
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *cmdForknet) RunDetach(cmd *cobra.Command, args []string) error {
	daemonPID := args[1]
	ifName := args[2]
	hostName := args[3]

	if daemonPID == "" {
		return fmt.Errorf("Daemon PID argument is required")
	}

	if ifName == "" {
		return fmt.Errorf("ifname argument is required")
	}

	if hostName == "" {
		return fmt.Errorf("hostname argument is required")
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
