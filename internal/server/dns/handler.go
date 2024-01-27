package dns

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
)

type dnsHandler struct {
	server *Server
	mu     sync.Mutex
}

func (d dnsHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	// Don't allow concurent queries.
	d.mu.Lock()
	defer d.mu.Unlock()

	// Check if we're ready to serve queries.
	if d.server.zoneRetriever == nil {
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeServerFailure)
		err := w.WriteMsg(m)
		if err != nil {
			logger.Error("Unable to write message", logger.Ctx{"err": err})
		}

		return
	}

	// Only allow a single request.
	if len(r.Question) != 1 {
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeServerFailure)
		err := w.WriteMsg(m)
		if err != nil {
			logger.Error("Unable to write message", logger.Ctx{"err": err})
		}

		return
	}

	// Check that it's a supported request type.
	if r.Question[0].Qtype != dns.TypeAXFR && r.Question[0].Qtype != dns.TypeIXFR && r.Question[0].Qtype != dns.TypeSOA {
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeNotImplemented)
		err := w.WriteMsg(m)
		if err != nil {
			logger.Error("Unable to write message", logger.Ctx{"err": err})
		}

		return
	}

	// Extract the request information.
	name := strings.TrimSuffix(r.Question[0].Name, ".")
	ip, _, err := net.SplitHostPort(w.RemoteAddr().String())
	if err != nil {
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeServerFailure)
		err := w.WriteMsg(m)
		if err != nil {
			logger.Error("Unable to write message", logger.Ctx{"err": err})
		}

		return
	}

	// Prepare the response.
	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true

	// Load the zone.
	zone, err := d.server.zoneRetriever(name, r.Question[0].Qtype != dns.TypeSOA)
	if err != nil {
		// On failure, return NXDOMAIN.
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeNameError)
		err := w.WriteMsg(m)
		if err != nil {
			logger.Error("Unable to write message", logger.Ctx{"err": err})
		}

		return
	}

	// Check access.
	if !d.isAllowed(zone.Info, ip, r.IsTsig(), w.TsigStatus() == nil) {
		// On auth failure, return NXDOMAIN to avoid information leaks.
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeNameError)
		err := w.WriteMsg(m)
		if err != nil {
			logger.Error("Unable to write message", logger.Ctx{"err": err})
		}

		return
	}

	zoneRR := dns.NewZoneParser(strings.NewReader(zone.Content), "", "")
	for {
		rr, ok := zoneRR.Next()
		if !ok {
			err := zoneRR.Err()
			if err != nil {
				logger.Errorf("Bad DNS record in zone %q: %v", name, err)

				m := new(dns.Msg)
				m.SetRcode(r, dns.RcodeFormatError)
				err := w.WriteMsg(m)
				if err != nil {
					logger.Error("Unable to write message", logger.Ctx{"err": err})
				}

				return
			}

			break
		}

		m.Answer = append(m.Answer, rr)
	}

	tsig := r.IsTsig()
	if tsig != nil && w.TsigStatus() == nil {
		m.SetTsig(tsig.Hdr.Name, tsig.Algorithm, 300, time.Now().Unix())
	}

	err = w.WriteMsg(m)
	if err != nil {
		logger.Error("Unable to write message", logger.Ctx{"err": err})
	}
}

func (d *dnsHandler) isAllowed(zone api.NetworkZone, ip string, tsig *dns.TSIG, tsigStatus bool) bool {
	type peer struct {
		address string
		key     string
	}

	// Build a list of peers.
	peers := map[string]*peer{}
	for k, v := range zone.Config {
		if !strings.HasPrefix(k, "peers.") {
			continue
		}

		// Extract the fields.
		fields := strings.SplitN(k, ".", 3)
		if len(fields) != 3 {
			continue
		}

		peerName := fields[1]

		if peers[peerName] == nil {
			peers[peerName] = &peer{}
		}

		// Add the correct validation rule for the dynamic field based on last part of key.
		switch fields[2] {
		case "address":
			peers[peerName].address = v
		case "key":
			peers[peerName].key = v
		}
	}

	// Validate access.
	for peerName, peer := range peers {
		peerKeyName := fmt.Sprintf("%s_%s.", zone.Name, peerName)

		if peer.address != "" && ip != peer.address {
			// Bad IP address.
			continue
		}

		if peer.key != "" && (tsig == nil || !tsigStatus) {
			// Missing or invalid TSIG.
			continue
		}

		if peer.key != "" && tsig.Hdr.Name != peerKeyName {
			// Bad key name (valid TSIG but potentially for another domain).
			continue
		}

		// We have a trusted peer.
		return true
	}

	return false
}
