package dns

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

type notifyState struct {
	pending  bool
	lastSent time.Time
}

type zonePeer struct {
	address string
	key     string
}

// zonePeers extracts the peer definitions from a zone's configuration.
func zonePeers(zone api.NetworkZone) map[string]*zonePeer {
	peers := map[string]*zonePeer{}

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
			peers[peerName] = &zonePeer{}
		}

		switch fields[2] {
		case "address":
			peers[peerName].address = v
		case "key":
			peers[peerName].key = v
		}
	}

	return peers
}

// NotifyZone schedules a DNS NOTIFY to the zone's peers, merging rapid changes.
func (s *Server) NotifyZone(name string) {
	if s.zoneRetriever == nil {
		return
	}

	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()

	zoneState := s.notifyZones[name]
	if zoneState == nil {
		zoneState = &notifyState{}
		s.notifyZones[name] = zoneState
	}

	// A notification is already scheduled.
	if zoneState.pending {
		return
	}

	// Merge rapid changes and rate limit to one NOTIFY per minute per zone.
	delay := 5 * time.Second
	elapsed := time.Since(zoneState.lastSent)
	if elapsed < time.Minute {
		delay = time.Minute - elapsed
	}

	zoneState.pending = true
	time.AfterFunc(delay, func() {
		s.sendNotify(name)
	})
}

// sendNotify sends a DNS NOTIFY for the zone to all of its configured peers.
func (s *Server) sendNotify(name string) {
	s.notifyMu.Lock()
	zoneState := s.notifyZones[name]
	if zoneState != nil {
		zoneState.pending = false
		zoneState.lastSent = time.Now()
	}

	s.notifyMu.Unlock()

	// Load the zone configuration.
	zone, err := s.zoneRetriever(name, false)
	if err != nil {
		logger.Debug("Skipping DNS NOTIFY for unknown zone", logger.Ctx{"zone": name, "err": err})
		return
	}

	fqdn := dns.Fqdn(name)
	for peerName, peer := range zonePeers(zone.Info) {
		if peer.address == "" {
			continue
		}

		m := &dns.Msg{}
		m.SetNotify(fqdn)

		client := &dns.Client{Timeout: 5 * time.Second}
		if peer.key != "" {
			keyName := fmt.Sprintf("%s_%s.", name, peerName)
			m.SetTsig(keyName, dns.HmacSHA256, 300, time.Now().Unix())
			client.TsigSecret = map[string]string{keyName: peer.key}
		}

		_, _, err := client.Exchange(m, net.JoinHostPort(peer.address, "53"))
		if err != nil {
			logger.Warn("Failed to send DNS NOTIFY", logger.Ctx{"zone": name, "peer": peerName, "err": err})
			continue
		}

		logger.Debug("Sent DNS NOTIFY", logger.Ctx{"zone": name, "peer": peerName})
	}
}
