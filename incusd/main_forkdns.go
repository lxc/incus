// forkdns provides a specialised DNS server designed for relaying A and PTR queries.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/miekg/dns"
	"github.com/spf13/cobra"
	"k8s.io/utils/inotify"

	"github.com/lxc/incus/incusd/network"
	"github.com/lxc/incus/shared"
	"github.com/lxc/incus/internal/dnsutil"
	"github.com/lxc/incus/shared/logger"
)

type cmdForkDNS struct {
	global *cmdGlobal
}

type dnsHandler struct {
	domain    string
	leaseFile string
}

var dnsServersFileLock sync.Mutex
var dnsServersList []string

// serversFileMonitor performs an initial load of the server list and then waits for the file to be
// modified before triggering a reload.
func serversFileMonitor(watcher *inotify.Watcher, watcherPath string, networkName string) {
	err := loadServersList(networkName)
	if err != nil {
		logger.Errorf("Server list load error: %v", err)
	}

	for {
		select {
		case ev := <-watcher.Event:
			// Ignore files events that dont concern the servers list file.
			if ev.Name != filepath.Join(watcherPath, network.ForkdnsServersListFile) {
				continue
			}

			err := loadServersList(networkName)
			if err != nil {
				logger.Errorf("Server list load error: %v", err)
				continue
			}

		case err := <-watcher.Error:
			logger.Errorf("Inotify error: %v", err)
		}
	}
}

// loadServersList reads the server list path and updates the internal servers list slice.
func loadServersList(networkName string) error {
	servers, err := network.ForkdnsServersList(networkName)
	if err != nil {
		return err
	}

	// Safely apply new servers list to global list.
	dnsServersFileLock.Lock()
	dnsServersList = servers
	dnsServersFileLock.Unlock()
	logger.Infof("Server list loaded: %v", servers)
	return nil
}

// ServeDNS handles each DNS request.
func (h *dnsHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	var err error
	msg := dns.Msg{}
	msg.SetReply(r)

	// We only support single questions for now
	if len(r.Question) != 1 {
		msg.SetRcode(r, dns.RcodeNameError)
	} else if r.Question[0].Qtype == dns.TypePTR {
		msg, err = h.handlePTR(r)
		if err != nil {
			logger.Errorf("PTR record lookup failed for %s: %v", r.Question[0].Name, err)
			msg.SetRcode(r, dns.RcodeNameError)
		}
	} else if r.Question[0].Qtype == dns.TypeA || r.Question[0].Qtype == dns.TypeAAAA {
		msg, err = h.handleA(r)
		if err != nil {
			logger.Errorf("A record lookup failed for %s: %v", r.Question[0].Name, err)
			msg.SetRcode(r, dns.RcodeNameError)
		}

		// Currently forkdns doesn't support IPv6, but to ensure compatbility and expected behavior with
		// DNS clients, we return an empty AAAA response if the A record was found (meaning the domain
		// exists, but no AAAA records).
		if r.Question[0].Qtype == dns.TypeAAAA && msg.Rcode == dns.RcodeSuccess {
			msg.Answer = []dns.RR{} // Empty response for AAAA if equivalent A record exists.
		}
	} else {
		// Fallback to NXDOMAIN
		msg.SetRcode(r, dns.RcodeNameError)
	}

	err = w.WriteMsg(&msg)
	if err != nil {
		logger.Errorf("Failed sending response for %s: %v", r.Question[0].Name, err)
	}
}

// handlePTR answers requests for reverse DNS records.
// It is used with cluster networking to provide cluster wide DNS PTR resolution by consulting the
// local DHCP leases file and if not found, then relaying the question to the other cluster member's
// forkdns instance. Returns DNS message to be sent as response.
func (h *dnsHandler) handlePTR(r *dns.Msg) (dns.Msg, error) {
	msg := dns.Msg{}
	msg.SetReply(r)

	// If request is marked as non-recursive it means it is from another forkdns and we should
	// attempt to answer it using the local dnsmasq lease file and not relay it.
	if !r.RecursionDesired {
		// Check if the local DHCP leases file contains a lease for the requested reverse DNS name.
		// If this fails with an error or no record found, then check other cluster hosts.
		hostname, err := h.getLeaseHostByReverseIPName(r.Question[0].Name)
		if err != nil {
			logger.Errorf("PTR record lease lookup failed for %s: %v", r.Question[0].Name, err)
		}

		// Record found in local DHCP leases file, generate answer response and send.
		if hostname != "" {
			msg.Authoritative = true
			msg.Answer = append(msg.Answer, &dns.PTR{
				Hdr: dns.RR_Header{
					Name:   r.Question[0].Name,
					Rrtype: dns.TypePTR,
					Class:  dns.ClassINET,
					Ttl:    0,
				},

				// Suffix the hostname in the lease with the cluster DNS zone name (e.g. ".example.net.")
				// The final full stop is important as the response needs to be a FQDN.
				Ptr: fmt.Sprintf("%s.%s.", hostname, h.domain),
			})

			return msg, nil
		}

		// Record not found locally, return NXDOMAIN.
		msg.SetRcode(r, dns.RcodeNameError)
		return msg, nil
	}

	// If we get here, then the recursion desired flag was set, meaning we cannot answer the
	// query locally and need to relay it to the other forkdns instances.

	// Get current list of servers safely.
	dnsServersFileLock.Lock()
	servers := dnsServersList
	dnsServersFileLock.Unlock()

	// Query all the servers.
	for _, server := range servers {
		req := dns.Msg{}
		req.Question = r.Question
		// Tell the remote node we only want to query their local data (to stop loops).
		req.RecursionDesired = false
		req.Id = r.Id

		resp, err := dns.Exchange(&req, fmt.Sprintf("%s:1053", server))
		if err != nil || len(resp.Answer) == 0 {
			// Error or empty response, try the next one
			continue
		}

		return *resp, nil
	}

	// Record not found in any of the remove servers.
	msg.SetRcode(r, dns.RcodeNameError)
	return msg, nil
}

// getLeaseHostByReverseIPName finds the hostname used in the DHCP lease by supplying a reverse
// DNS hostname of the device's IP.
func (h *dnsHandler) getLeaseHostByReverseIPName(reverseName string) (string, error) {
	ip := dnsutil.ExtractAddressFromReverse(reverseName)
	if ip == "" {
		return "", errors.New("Failed to convert reverse name to IP")
	}

	file, err := os.Open(h.leaseFile)
	if err != nil {
		return "", err
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 5 {
			address := fields[2]
			if address == ip {
				return fields[3], nil
			}
		}
	}

	err = scanner.Err()
	if err != nil {
		return "", err
	}

	return "", nil
}

// handleA answers requests for A DNS records.
// It is used with cluster networking to provide cluster wide DNS A resolution by consulting the
// local DHCP leases file and if not found, then relaying the question to the other cluster member's
// forkdns instance. Returns DNS message to be sent as response.
func (h *dnsHandler) handleA(r *dns.Msg) (dns.Msg, error) {
	msg := dns.Msg{}
	msg.SetReply(r)

	// If request is marked as non-recursive it means it is from another forkdns and we should
	// attempt to answer it using the local dnsmasq lease file and not relay it.
	if !r.RecursionDesired {
		// Check if the local DHCP leases file contains a lease for the requested hostname name.
		// If this fails with an error or no record found, then check other cluster hosts.
		ip, err := h.getLeaseHostByDNSName(r.Question[0].Name)
		if err != nil {
			logger.Errorf("A record lease lookup failed for %s: %v", r.Question[0].Name, err)
		}

		// Record found in local DHCP leases file, generate answer response and send.
		if ip != "" {
			msg.Authoritative = true
			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{
					Name:   r.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    0,
				},
				A: net.ParseIP(ip),
			})

			return msg, nil
		}

		// Record not found locally, return NXDOMAIN.
		msg.SetRcode(r, dns.RcodeNameError)
		return msg, nil
	}

	// If we get here, then the recursion desired flag was set, meaning we cannot answer the
	// query locally and need to relay it to the other forkdns instances.

	// Get current list of servers safely.
	dnsServersFileLock.Lock()
	servers := dnsServersList
	dnsServersFileLock.Unlock()

	// Query all the servers.
	for _, server := range servers {
		req := dns.Msg{}
		req.Question = r.Question
		// Tell the remote node we only want to query their local data (to stop loops).
		req.RecursionDesired = false
		req.Id = r.Id

		resp, err := dns.Exchange(&req, fmt.Sprintf("%s:1053", server))
		if err != nil || resp.Rcode != dns.RcodeSuccess {
			// Error sending request or error response, try next server.
			continue
		}

		return *resp, nil
	}

	// Record not found in any of the remote servers.
	msg.SetRcode(r, dns.RcodeNameError)
	return msg, nil
}

// getLeaseHostByDNSName finds the hostname used in the DHCP lease by supplying a DNS A name.
func (h *dnsHandler) getLeaseHostByDNSName(dnsName string) (string, error) {
	host := strings.TrimSuffix(dnsName, fmt.Sprintf(".%s.", h.domain))

	file, err := os.Open(h.leaseFile)
	if err != nil {
		return "", err
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 5 {
			hostname := fields[3]
			if hostname == host {
				return fields[2], nil
			}
		}
	}

	err = scanner.Err()
	if err != nil {
		return "", err
	}

	return "", nil
}

func (c *cmdForkDNS) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkdns <listen address> <domain> <network name>"
	cmd.Short = "Internal DNS proxy for clustering"
	cmd.Long = `Description:
  Spawns a specialised DNS server designed for relaying A and PTR queries that cannot be answered by
  the local dnsmasq process to the other cluster member's forkdns process where it will inspect the
  local dnsmasq lease file looking for an answer to the query.
  It uses the "recursion desired" flag in incoming DNS requests to modify its behaviour.
  When "recursion desired" is set to yes, the query is immediately relayed to the other cluster nodes
  (with the "recursion desired" flag set to no) as it indicates that the local dnsmasq process was
  unable to answer it from the local lease file.
  When "recursion desired" flag is set to no, this indicates the request has been sent from another
  forkdns process, and the local dnsmasq lease file only is parsed to try and answer the query.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkDNS) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) < 3 {
		_ = cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	err := logger.InitLogger("", "incus-forkdns", c.global.flagLogVerbose, c.global.flagLogDebug, nil)
	if err != nil {
		return err
	}

	// Setup watcher on servers file.
	watcher, err := inotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("Unable to setup inotify: %s", err)
	}

	networkName := args[2]
	path := shared.VarPath("networks", networkName, network.ForkdnsServersListPath)

	// Only watch for files being renamed inside the watched directory as new server lists are created in a
	// temporary file first and then renamed to the file actually used by forkdns.
	err = watcher.AddWatch(path, inotify.InMovedTo)
	if err != nil {
		return fmt.Errorf("Unable to setup inotify watch on %s: %w", path, err)
	}

	// Run the server list monitor concurrently waiting for file changes.
	go serversFileMonitor(watcher, path, networkName)

	logger.Info("Started")

	srv := &dns.Server{
		Addr: args[0],
		Net:  "udp",
	}

	srv.Handler = &dnsHandler{
		domain:    args[1],
		leaseFile: shared.VarPath("networks", networkName, "dnsmasq.leases"),
	}

	err = srv.ListenAndServe()
	if err != nil {
		return fmt.Errorf("Failed to set udp listener: %w", err)
	}

	return nil
}
