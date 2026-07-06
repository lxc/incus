package main

/*
#include "config.h"

#include <errno.h>
#include <fcntl.h>
#include <poll.h>
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
int forknet_dhcp_readyfd = -1;

static void forkdonetdhcp(char *logfilestr) {
	char *pidstr;
	char path[PATH_MAX];
	int pipefd[2];
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

	// Setup a pipe to wait for the initial network configuration.
	if (pipe(pipefd) < 0) {
		fprintf(stderr, "%s - Failed to create pipe\n", strerror(errno));
		_exit(EXIT_FAILURE);
	}

	// Run in the background.
	pid = fork();
	if (pid < 0) {
		fprintf(stderr, "%s - Failed to create new process\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	if (pid > 0) {
		struct pollfd pfd = {0};

		// Wait up to 5s for the initial network configuration.
		close(pipefd[1]);
		pfd.fd = pipefd[0];
		pfd.events = POLLIN;
		(void)poll(&pfd, 1, 5000);

		_exit(EXIT_SUCCESS);
	}

	close(pipefd[0]);
	forknet_dhcp_readyfd = pipefd[1];

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

	// Get the netns file path.
	cur = advance_arg(false);
	if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0)) {
		return;
	}

	// Check that we're root
	if (geteuid() != 0) {
		fprintf(stderr, "Error: forknet requires root privileges\n");
		_exit(1);
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
	"math/rand"
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
	"github.com/mdlayher/packet"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	instanceDrivers "github.com/lxc/incus/v7/internal/server/instance/drivers"
	"github.com/lxc/incus/v7/internal/server/ip"
	_ "github.com/lxc/incus/v7/shared/cgo" // Used by cgo
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/subprocess"
	"github.com/lxc/incus/v7/shared/util"
)

type cmdForknet struct {
	global *cmdGlobal

	applyDNSMu      sync.Mutex
	dhcpv4Leases    map[string]*nclient4.Lease
	dhcpv6Leases    map[string]*dhcpv6.Message
	instNetworkPath string

	// Initial DNS configuration parsed from the pre-existing resolv.conf.
	initialNameservers []string
	initialSearch      []string
	initialDomain      string
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

// RunDHCP spawns the DHCP client(s) and applies address, route and DNS configuration.
func (c *cmdForknet) runDHCP(_ *cobra.Command, args []string) error {
	l := logrus.New()
	l.Level = logrus.DebugLevel

	c.instNetworkPath = args[0]

	if C.forknet_dhcp_logfile >= 0 {
		l.SetOutput(os.NewFile(uintptr(C.forknet_dhcp_logfile), "incus-dhcp-logfile"))
	} else {
		l.SetOutput(io.Discard)
	}

	// Prepare to signal the parent that the initial configuration is complete.
	var readyFile *os.File
	if C.forknet_dhcp_readyfd >= 0 {
		readyFile = os.NewFile(uintptr(C.forknet_dhcp_readyfd), "incus-dhcp-ready")
	}

	notifyReady := sync.OnceFunc(func() {
		if readyFile != nil {
			_ = readyFile.Close()
		}
	})

	defer notifyReady()

	// Read the hostname.
	bb, err := os.ReadFile(filepath.Join(c.instNetworkPath, "hostname"))
	if err != nil {
		l.WithError(err).Error("Unable to read hostname file")
	}

	hostname := strings.TrimSpace(string(bb))

	// Parse any pre-existing resolv.conf so its values are preserved alongside DHCP provided ones.
	c.parseInitialResolvConf()

	// Load the expected per-interface network configuration.
	ifaceConfigs := c.loadInterfaces(l)

	// Create PID file.
	err = os.WriteFile(filepath.Join(c.instNetworkPath, "dhcp.pid"), []byte(fmt.Sprintf("%d", os.Getpid())), 0o644)
	if err != nil {
		l.WithError(err).Error("Giving up on DHCP, couldn't write PID file")
		return err
	}

	// Enumerate network interfaces and skip loopback.
	ifaces, err := net.Interfaces()
	if err != nil {
		l.WithError(err).Error("Giving up on DHCP, couldn't list interfaces")
		return err
	}

	var names []string
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagLoopback != 0 {
			continue
		}

		names = append(names, ifi.Name)
	}

	if len(names) == 0 {
		l.Info("No non-loopback interfaces found; nothing to do for DHCP")
		return nil
	}

	// Initialize per-interface lease maps.
	c.applyDNSMu.Lock()
	c.dhcpv4Leases = map[string]*nclient4.Lease{}
	c.dhcpv6Leases = map[string]*dhcpv6.Message{}
	c.applyDNSMu.Unlock()

	// Get the DUID.
	duid, err := c.loadOrCreateDUID(ifaces)
	if err != nil {
		l.WithError(err).Error("Giving up on DHCPv6, couldn't load or create DUID")
		return err
	}

	// Buffer size is 2 goroutines per iface.
	errorChannel := make(chan error, len(names)*2)

	// Launch DHCP clients for each iface.
	readyWg := sync.WaitGroup{}
	launched := 0
	for _, iface := range names {
		l := l.WithField("interface", iface).Logger

		// Get the expected interface configuration, defaulting to a fully dynamic one.
		config, ok := ifaceConfigs[iface]
		if !ok {
			config = instanceDrivers.OCINetworkInterface{DHCP4: true, DHCP6: true, Route4: true, Route6: true}
		}

		// Prevent router advertisements from providing a default gateway.
		if !config.Route6 {
			err := c.disableIPv6Gateway(iface)
			if err != nil {
				l.WithError(err).Warning("Couldn't disable the IPv6 default gateway")
			}
		}

		// Skip interfaces that are fully statically configured.
		if !config.DHCP4 && !config.DHCP6 {
			l.Info("skipping dhcp on statically configured interface")
			continue
		}

		l.Info("running dhcp on interface")

		link := &ip.Link{
			Name: iface,
		}

		err := link.SetUp()
		if err != nil {
			l.WithField("interface", iface).WithError(err).Error("Giving up on DHCP for this interface, couldn't bring up interface")

			// continue to try other interfaces
			continue
		}

		if config.DHCP4 {
			readyWg.Add(1)
			go c.dhcpRunV4(errorChannel, sync.OnceFunc(readyWg.Done), iface, hostname, config, l)
			launched++
		}

		if config.DHCP6 {
			readyWg.Add(1)
			go c.dhcpRunV6(errorChannel, sync.OnceFunc(readyWg.Done), iface, hostname, duid, l)
			launched++
		}
	}

	// Notify the parent once all interfaces have completed their initial configuration.
	go func() {
		readyWg.Wait()
		notifyReady()
	}()

	// Wait for all launched goroutines to return.
	var finalErr error
	for i := 0; i < launched; i++ {
		err := <-errorChannel
		if err != nil {
			l.WithError(err).Error("DHCP client failed")
			finalErr = fmt.Errorf("some DHCP clients failed (one or more)")
		}
	}

	return finalErr
}

// newDHCPv4Conn opens a raw packet socket for DHCPv4 and uses BPF to filter the packets.
func newDHCPv4Conn(iface string) (net.PacketConn, net.HardwareAddr, error) {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, nil, err
	}

	conn, err := packet.Listen(ifc, packet.Datagram, unix.ETH_P_IP, nil)
	if err != nil {
		return nil, nil, err
	}

	// Setup the filter.
	filter, err := bpf.Assemble([]bpf.Instruction{
		// Load the IPv4 protocol field and only keep UDP (17).
		bpf.LoadAbsolute{Off: 9, Size: 1},
		bpf.JumpIf{Cond: bpf.JumpNotEqual, Val: unix.IPPROTO_UDP, SkipTrue: 6},

		// Load the flags and fragment offset and drop any fragment, as
		// the transport header is only present in the first one.
		bpf.LoadAbsolute{Off: 6, Size: 2},
		bpf.JumpIf{Cond: bpf.JumpBitsSet, Val: 0x1fff, SkipTrue: 4},

		// Set X to the IPv4 header length (4 * IHL).
		bpf.LoadMemShift{Off: 0},

		// Load the UDP destination port and only keep packets where it matches the DHCP client port (68).
		bpf.LoadIndirect{Off: 2, Size: 2},
		bpf.JumpIf{Cond: bpf.JumpNotEqual, Val: nclient4.ClientPort, SkipTrue: 1},

		// Accept the packet, otherwise drop it.
		bpf.RetConstant{Val: 0xffffffff},
		bpf.RetConstant{Val: 0},
	})
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	err = conn.SetBPF(filter)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	return nclient4.NewBroadcastUDPConn(conn, &net.UDPAddr{Port: nclient4.ClientPort}), ifc.HardwareAddr, nil
}

func (c *cmdForknet) dhcpRunV4(errorChannel chan error, ready func(), iface string, hostname string, config instanceDrivers.OCINetworkInterface, l *logrus.Logger) {
	var client *nclient4.Client

	defer ready()

	// Try to open a raw socket with a kernel-level BPF filter attached.
	conn, hwAddr, err := newDHCPv4Conn(iface)
	if err != nil {
		l.WithError(err).Warning("Couldn't set up filtered DHCPv4 socket, falling back to userspace filtering")

		client, err = nclient4.New(iface)
		if err != nil {
			l.WithError(err).Error("Giving up on DHCPv4, couldn't set up client")
			errorChannel <- err
			return
		}
	} else {
		client, err = nclient4.NewWithConn(conn, hwAddr)
		if err != nil {
			l.WithError(err).Error("Giving up on DHCPv4, couldn't set up client")
			_ = conn.Close()
			errorChannel <- err
			return
		}
	}

	defer logger.WarnOnError(client.Close, "Failed to close DHCP client")

	// Setup a 30s timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lease, err := client.Request(ctx,
		dhcpv4.WithoutOption(dhcpv4.OptionIPAddressLeaseTime),
		dhcpv4.WithRequestedOptions(
			dhcpv4.OptionSubnetMask,           // 1
			dhcpv4.OptionRouter,               // 3
			dhcpv4.OptionDomainNameServer,     // 6
			dhcpv4.OptionDomainName,           // 15
			dhcpv4.OptionClasslessStaticRoute, // 121 (if present)
			dhcpv4.OptionIPAddressLeaseTime,   // 51
			dhcpv4.OptionRenewTimeValue,       // 58 (T1)
			dhcpv4.OptionRebindingTimeValue,   // 59 (T2)
		),
		dhcpv4.WithOption(dhcpv4.OptHostName(hostname)))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			l.WithField("hostname", hostname).
				Info("No DHCPv4 server responded in time; giving up on DHCPv4")
			errorChannel <- nil
			return
		}

		l.WithError(err).WithField("hostname", hostname).
			Error("Giving up on DHCPv4, couldn't get a lease")
		errorChannel <- err
		return
	}

	// Parse the response.
	if lease.Offer == nil {
		l.WithField("hostname", hostname).
			Error("Giving up on DHCPv4, couldn't get a lease")
		errorChannel <- errors.New("Giving up on DHCPv4, couldn't get a lease")
		return
	}

	if lease.Offer.YourIPAddr == nil || lease.Offer.YourIPAddr.Equal(net.IPv4zero) || lease.Offer.SubnetMask() == nil || (config.Route4 && len(lease.Offer.Router()) != 1) {
		l.Error("Giving up on DHCPv4, lease didn't contain required fields")
		errorChannel <- errors.New("Giving up on DHCPv4, lease didn't contain required fields")
		return
	}

	c.applyDNSMu.Lock()
	c.dhcpv4Leases[iface] = lease
	c.applyDNSMu.Unlock()

	err = c.dhcpApplyDNS(l)
	if err != nil {
		l.WithError(err).Error("Giving up on DHCPv4, error applying DNS")
		errorChannel <- err
		return
	}

	// Network configuration.
	addr := &ip.Addr{
		DevName: iface,
		Address: &net.IPNet{
			IP:   lease.Offer.YourIPAddr,
			Mask: lease.Offer.SubnetMask(),
		},
		Family: ip.FamilyV4,
	}

	err = addr.Add()
	if err != nil {
		l.WithError(err).Error("Giving up on DHCPv4, couldn't add IP")
		errorChannel <- err
		return
	}

	if lease.Offer.Options.Has(dhcpv4.OptionClasslessStaticRoute) {
		for _, staticRoute := range lease.Offer.ClasslessStaticRoute() {
			// Skip any default route when the gateway is disabled.
			if !config.Route4 {
				if staticRoute.Dest == nil {
					continue
				}

				ones, _ := staticRoute.Dest.Mask.Size()
				if ones == 0 {
					continue
				}
			}

			route := &ip.Route{
				DevName: iface,
				Route:   staticRoute.Dest,
				Family:  ip.FamilyV4,
			}

			if !staticRoute.Router.IsUnspecified() {
				route.Via = staticRoute.Router
			}

			err = route.Add()
			if err != nil {
				l.WithError(err).Error("Giving up on DHCPv4, couldn't add classless static route")
				errorChannel <- err
				return
			}
		}
	} else {
		gws := lease.Offer.Router()

		if !config.Route4 {
			l.WithField("interface", iface).Info("Default gateway disabled on interface; skipping default route")
		} else if len(gws) == 0 || gws[0] == nil || gws[0].IsUnspecified() {
			l.WithField("interface", iface).Info("No default gateway provided by DHCPv4; skipping default route")
		} else {
			err := c.installDefaultRouteV4(iface, gws[0])
			if err != nil {
				l.WithError(err).Error("Giving up on DHCPv4, couldn't add default route")
				errorChannel <- err
				return
			}
		}
	}

	// Initial configuration is complete.
	ready()

	// Handle DHCP renewal.
	for {
		// Calculate the renewal time.
		var t1 time.Duration

		if lease.ACK != nil {
			t1 = lease.ACK.IPAddressRenewalTime(0)
		}

		if t1 == 0 && lease.Offer != nil {
			t1 = lease.Offer.IPAddressRenewalTime(0)
		}

		if t1 == 0 && lease.Offer != nil {
			lt := lease.Offer.IPAddressLeaseTime(0)
			if lt > 0 {
				t1 = lt / 2
			}
		}

		if t1 == 0 {
			t1 = time.Minute
		}

		j := time.Duration(int64(t1) / 20) // 5%
		if j > 0 {
			t1 += time.Duration(rand.Int63n(int64(2*j))) - j
		}

		// Wait until it's renewal time.
		time.Sleep(t1)

		// Renew the lease.
		newLease, err := client.Renew(context.Background(), lease,
			dhcpv4.WithRequestedOptions(
				dhcpv4.OptionIPAddressLeaseTime, // 51
				dhcpv4.OptionRenewTimeValue,     // 58
				dhcpv4.OptionRebindingTimeValue, // 59
			),
			dhcpv4.WithOption(dhcpv4.OptHostName(hostname)))
		if err != nil {
			l.WithError(err).Error("Giving up on DHCPv4, couldn't renew the lease")
			errorChannel <- err
			return
		}

		lease = newLease
	}
}

func (c *cmdForknet) dhcpRunV6(errorChannel chan error, ready func(), iface string, hostname string, duid dhcpv6.DUID, l *logrus.Logger) {
	defer ready()

	// Wait a couple of seconds for IPv6 link-local.
	time.Sleep(2 * time.Second)

	// Get a new DHCPv6 client.
	client, err := nclient6.New(iface)
	if err != nil {
		l.WithError(err).Error("Giving up on DHCPv6, couldn't set up client")
		errorChannel <- err
		return
	}

	defer logger.WarnOnError(client.Close, "Failed to close DHCP client")

	// Setup a 30s timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Try to get a lease.
	advertisement, err := client.Solicit(ctx,
		dhcpv6.WithClientID(duid),
		dhcpv6.WithFQDN(0, hostname))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			l.Info("No DHCPv6 server responded in time; giving up on DHCPv6")
			errorChannel <- nil
			return
		}

		l.WithError(err).Error("Giving up on DHCPv6, error during DHCPv6 Solicit")
		errorChannel <- err
		return
	}

	// Check if we're dealing with stateless DHCPv6.
	if advertisement.Options.Status() == nil || advertisement.Options.Status().StatusCode == iana.StatusNoAddrsAvail {
		// Get interface details.
		i, err := net.InterfaceByName(iface)
		if err != nil {
			l.WithError(err).Error("Giving up on DHCPv6, couldn't get interface details")
			errorChannel <- err
			return
		}

		// Try to get some information.
		infoRequest, err := dhcpv6.NewSolicit(i.HardwareAddr,
			dhcpv6.WithClientID(duid),
			dhcpv6.WithFQDN(0, hostname))
		if err != nil {
			l.WithError(err).Error("Giving up on DHCPv6, error preparing DHCPv6 Info Request")
			errorChannel <- err
			return
		}

		infoRequest.MessageType = dhcpv6.MessageTypeInformationRequest
		infoRequest.Options.Del(dhcpv6.OptionIANA)
		reply, err := client.SendAndRead(ctx, nclient6.AllDHCPRelayAgentsAndServers, infoRequest, nclient6.IsMessageType(dhcpv6.MessageTypeReply))
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				l.Info("No DHCPv6 server responded in time; giving up on DHCPv6")
				errorChannel <- nil
				return
			}

			l.WithError(err).Error("Giving up on DHCPv6, error during DHCPv6 Info Request")
			errorChannel <- err
			return
		}

		// Update DNS.
		c.applyDNSMu.Lock()
		c.dhcpv6Leases[iface] = reply
		c.applyDNSMu.Unlock()

		err = c.dhcpApplyDNS(l)
		if err != nil {
			l.WithError(err).Error("Giving up on DHCPv6, error applying DNS")
			errorChannel <- err
			return
		}

		// We're dealing with stateless DHCPv6, no need to keep running.
		errorChannel <- nil
		return
	}

	reply, err := client.Request(ctx, advertisement,
		dhcpv6.WithClientID(duid),
		dhcpv6.WithFQDN(0, hostname))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			l.Info("No DHCPv6 server responded in time; giving up on DHCPv6")
			errorChannel <- nil
			return
		}

		l.WithError(err).Error("Giving up on DHCPv6, error during DHCPv6 Request")
		errorChannel <- err
		return
	}

	c.applyDNSMu.Lock()
	c.dhcpv6Leases[iface] = reply
	c.applyDNSMu.Unlock()

	err = c.dhcpApplyDNS(l)
	if err != nil {
		l.WithError(err).Error("Giving up on DHCPv6, error applying DNS")
		errorChannel <- err
		return
	}

	// Network configuration.
	ia := reply.Options.OneIANA()
	if ia == nil {
		l.Error("Giving up on DHCPv6 renewal, reply missing IANA")
		errorChannel <- errors.New("Giving up on DHCPv6 renewal, reply missing IANA")
		return
	}

	for _, iaaddr := range ia.Options.Addresses() {
		addr := &ip.Addr{
			DevName: iface,
			Address: &net.IPNet{
				IP:   iaaddr.IPv6Addr,
				Mask: net.CIDRMask(64, 128),
			},
			Family: ip.FamilyV6,
		}

		err = addr.Add()
		if err != nil {
			l.WithError(err).Error("Giving up on DHCPv6, couldn't add IP")
			errorChannel <- err
			return
		}
	}

	// Initial configuration is complete.
	ready()

	// Handle DHCP Renewal.
	for {
		// Calculate the renewal time.
		t1 := ia.T1

		if t1 == 0 {
			for _, iaaddr := range ia.Options.Addresses() {
				if iaaddr.PreferredLifetime <= 0 {
					continue
				}

				renew := iaaddr.PreferredLifetime / 2
				if t1 == 0 || renew < t1 {
					t1 = renew
				}
			}
		}

		if t1 == 0 {
			t1 = time.Minute
		}

		j := time.Duration(int64(t1) / 20) // 5%
		if j > 0 {
			t1 += time.Duration(rand.Int63n(int64(2*j))) - j
		}

		// Wait until it's renewal time.
		time.Sleep(t1)

		// Build the renewal message from the current lease.
		var optIAAddrs []dhcpv6.OptIAAddress
		for _, optIAAddr := range ia.Options.Addresses() {
			optIAAddrs = append(optIAAddrs, *optIAAddr)
		}

		modifiers := []dhcpv6.Modifier{
			dhcpv6.WithClientID(duid),
			dhcpv6.WithServerID(reply.Options.ServerID()),
			dhcpv6.WithIAID(ia.IaId),
			dhcpv6.WithIANA(optIAAddrs...),
			dhcpv6.WithFQDN(0, hostname),
		}

		renew, err := dhcpv6.NewMessage(modifiers...)
		if err != nil {
			l.WithError(err).Error("Giving up on DHCPv6, couldn't create renew message")
			errorChannel <- err
			return
		}

		renew.MessageType = dhcpv6.MessageTypeRenew
		renew.AddOption(dhcpv6.OptElapsedTime(0))

		// Send the renewal.
		newReply, err := client.SendAndRead(context.Background(),
			nclient6.AllDHCPRelayAgentsAndServers,
			renew,
			nclient6.IsMessageType(dhcpv6.MessageTypeReply))
		if err != nil {
			l.WithError(err).Error("Giving up on DHCPv6, couldn't renew the lease")
			errorChannel <- err
			return
		}

		newIA := newReply.Options.OneIANA()
		if newIA == nil {
			l.Error("Giving up on DHCPv6 renewal, reply missing IANA")
			errorChannel <- errors.New("Giving up on DHCPv6 renewal, reply missing IANA")
			return
		}

		reply = newReply
		ia = newIA

		// Refresh DNS in case the server returned new options.
		c.applyDNSMu.Lock()
		c.dhcpv6Leases[iface] = reply
		c.applyDNSMu.Unlock()

		err = c.dhcpApplyDNS(l)
		if err != nil {
			l.WithError(err).Error("Giving up on DHCPv6, error applying DNS after renewal")
			errorChannel <- err
			return
		}
	}
}

// Get a stable DUID, recording it on disk.
func (c *cmdForknet) loadOrCreateDUID(ifaces []net.Interface) (dhcpv6.DUID, error) {
	duidPath := filepath.Join(c.instNetworkPath, "dhcp6.duid")

	bb, err := os.ReadFile(duidPath)
	if err == nil {
		duid, err := dhcpv6.DUIDFromBytes(bb)
		if err == nil {
			return duid, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	var hwAddr net.HardwareAddr
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagLoopback != 0 {
			continue
		}

		if len(ifi.HardwareAddr) < 4 {
			continue
		}

		hwAddr = ifi.HardwareAddr
		break
	}

	if hwAddr == nil {
		return nil, errors.New("No suitable hardware address available for DUID generation")
	}

	duid := &dhcpv6.DUIDLLT{
		HWType:        iana.HWTypeEthernet,
		Time:          dhcpv6.GetTime(),
		LinkLayerAddr: hwAddr,
	}

	err = os.WriteFile(duidPath, duid.ToBytes(), 0o600)
	if err != nil {
		return nil, err
	}

	return duid, nil
}

// parseInitialResolvConf records any pre-existing resolv.conf values for merging with DHCP ones.
func (c *cmdForknet) parseInitialResolvConf() {
	content, err := os.ReadFile(filepath.Join(c.instNetworkPath, "resolv.conf"))
	if err != nil {
		return
	}

	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		switch fields[0] {
		case "nameserver":
			c.initialNameservers = append(c.initialNameservers, fields[1])
		case "search":
			c.initialSearch = append(c.initialSearch, fields[1:]...)
		case "domain":
			c.initialDomain = fields[1]
		}
	}
}

// loadInterfaces reads the expected per-interface network configuration.
func (c *cmdForknet) loadInterfaces(l *logrus.Logger) map[string]instanceDrivers.OCINetworkInterface {
	ifaces := map[string]instanceDrivers.OCINetworkInterface{}

	content, err := os.ReadFile(filepath.Join(c.instNetworkPath, "interfaces.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			l.WithError(err).Warning("Unable to read interfaces.json file")
		}

		return ifaces
	}

	err = json.Unmarshal(content, &ifaces)
	if err != nil {
		l.WithError(err).Warning("Unable to parse interfaces.json file")

		return map[string]instanceDrivers.OCINetworkInterface{}
	}

	return ifaces
}

func (c *cmdForknet) dhcpApplyDNS(l *logrus.Logger) error {
	nameservers := map[string]struct{}{}
	searchLabels := []string{}
	domainNames := []string{}

	// Seed with the values parsed from the initial resolv.conf.
	for _, ns := range c.initialNameservers {
		nameservers[ns] = struct{}{}
	}

	searchLabels = append(searchLabels, c.initialSearch...)

	if c.initialDomain != "" {
		domainNames = append(domainNames, c.initialDomain)
	}

	c.applyDNSMu.Lock()

	// IPv4 leases.
	for _, lease := range c.dhcpv4Leases {
		if lease == nil || lease.Offer == nil {
			continue
		}

		// Nameservers from DHCPv4.
		for _, ns := range lease.Offer.DNS() {
			nameservers[ns.String()] = struct{}{}
		}

		// Domain name (option 15).
		dn := lease.Offer.DomainName()
		if dn != "" {
			domainNames = append(domainNames, dn)
		}

		// Domain search list (option 119).
		ds := lease.Offer.DomainSearch()
		if ds != nil && len(ds.Labels) > 0 {
			searchLabels = append(searchLabels, ds.Labels...)
		}
	}

	// IPv6 leases.
	for _, reply := range c.dhcpv6Leases {
		if reply == nil {
			continue
		}

		// Nameservers from DHCPv6.
		for _, ns := range reply.Options.DNS() {
			nameservers[ns.String()] = struct{}{}
		}

		// Domain search list.
		dsl := reply.Options.DomainSearchList()
		if dsl != nil && len(dsl.Labels) > 0 {
			searchLabels = append(searchLabels, dsl.Labels...)
		}
	}

	c.applyDNSMu.Unlock()

	// Create resolv.conf.
	f, err := os.Create(filepath.Join(c.instNetworkPath, "resolv.conf"))
	if err != nil {
		l.WithError(err).Error("Giving up on DHCP, couldn't create resolv.conf")
		return err
	}

	defer logger.WarnOnError(f.Close, "Failed to close resolv.conf")

	// Write unique nameservers.
	for ns := range nameservers {
		_, err = fmt.Fprintf(f, "nameserver %s\n", ns)
		if err != nil {
			l.WithError(err).Error("Giving up on DHCP, couldn't write resolv.conf")
			return err
		}
	}

	// Prefer a search list if present; otherwise write a single domain if available.
	if len(searchLabels) > 0 {
		seen := map[string]struct{}{}
		out := []string{}

		for _, s := range searchLabels {
			if s == "" {
				continue
			}

			_, ok := seen[s]
			if ok {
				continue
			}

			seen[s] = struct{}{}
			out = append(out, s)
		}

		if len(out) > 0 {
			_, err = fmt.Fprintf(f, "search %s\n", strings.Join(out, " "))
			if err != nil {
				l.WithError(err).Error("Giving up on DHCP, couldn't write resolv.conf")
				return err
			}
		}
	} else if len(domainNames) > 0 {
		_, err = fmt.Fprintf(f, "domain %s\n", domainNames[0])
		if err != nil {
			l.WithError(err).Error("Giving up on DHCP, couldn't write resolv.conf")
			return err
		}
	}

	return nil
}

// disableIPv6Gateway prevents router advertisements from providing a default gateway on the interface.
func (c *cmdForknet) disableIPv6Gateway(iface string) error {
	// Don't accept default routes from router advertisements.
	err := os.WriteFile(filepath.Join("/proc/sys/net/ipv6/conf", iface, "accept_ra_defrtr"), []byte("0"), 0o644)
	if err != nil {
		return err
	}

	// Remove any existing default route on the interface.
	routes, err := (&ip.Route{
		DevName: iface,
		Family:  ip.FamilyV6,
		Table:   "main",
	}).List()
	if err != nil {
		return err
	}

	for _, route := range routes {
		if route.Route != nil {
			continue
		}

		err = route.Delete()
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *cmdForknet) installDefaultRouteV4(iface string, gw net.IP) error {
	// List all IPv4 routes in the main table; we'll filter default routes (Dst == nil) locally.
	routes, err := (&ip.Route{
		Family: ip.FamilyV4,
		Table:  "main",
	}).List()
	if err != nil {
		return err
	}

	var currentOwnerIf string
	var currentOwnerGw net.IP

	for _, r := range routes {
		// Only consider default routes (no destination)
		if r.Route != nil {
			continue
		}

		// r.DevName may be empty if not resolvable; skip such entries
		if r.DevName == "" {
			continue
		}

		if currentOwnerIf == "" || r.DevName < currentOwnerIf {
			currentOwnerIf = r.DevName
			currentOwnerGw = r.Via
		}
	}

	// Decide based on lexical order.
	switch {
	case currentOwnerIf == "":
		// No default route yet; we can install ours.

	case currentOwnerIf == iface:
		// We already own the default; if gateway unchanged, nothing to do.
		if currentOwnerGw != nil && gw != nil && currentOwnerGw.Equal(gw) {
			return nil
		}

	case iface < currentOwnerIf:
		// We win; replace the current default with ours.

	default:
		// We lose; keep existing default route.
		return nil
	}

	defRoute := &ip.Route{
		DevName: iface,
		Route:   nil,
		Via:     gw,
		Family:  ip.FamilyV4,
		Proto:   "dhcp",
	}

	err = defRoute.Replace()
	if err != nil {
		return err
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
