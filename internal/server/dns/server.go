package dns

import (
	"context"
	"sync"

	"github.com/miekg/dns"

	"github.com/lxc/incus/internal/ports"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/db"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/logger"
)

// ZoneRetriever is a function which fetches a DNS zone.
type ZoneRetriever func(name string, full bool) (*Zone, error)

// Server represents a DNS server instance.
type Server struct {
	tcpDNS *dns.Server
	udpDNS *dns.Server

	// External dependencies.
	db            *db.Cluster
	zoneRetriever ZoneRetriever

	// Internal state (to handle reconfiguration).
	address string

	mu sync.Mutex
}

// NewServer returns a new server instance.
func NewServer(db *db.Cluster, retriever ZoneRetriever) *Server {
	// Setup new struct.
	s := &Server{db: db, zoneRetriever: retriever}
	return s
}

// Start sets up the DNS listener.
func (s *Server) Start(address string) error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.start(address)
}

func (s *Server) start(address string) error {
	// Set default port if needed.
	address = internalUtil.CanonicalNetworkAddress(address, ports.DNSDefaultPort)

	// Setup the handler.
	handler := dnsHandler{}
	handler.server = s

	// Spawn the DNS server.
	s.tcpDNS = &dns.Server{Addr: address, Net: "tcp", Handler: handler}
	go func() {
		err := s.tcpDNS.ListenAndServe()
		if err != nil {
			logger.Errorf("Failed to bind TCP DNS address %q: %v", address, err)
		}
	}()

	s.udpDNS = &dns.Server{Addr: address, Net: "udp", Handler: handler}
	go func() {
		err := s.udpDNS.ListenAndServe()
		if err != nil {
			logger.Errorf("Failed to bind TCP DNS address %q: %v", address, err)
		}
	}()

	// TSIG handling.
	err := s.updateTSIG()
	if err != nil {
		return err
	}

	// Record the address.
	s.address = address

	return nil
}

// Stop tears down the DNS listener.
func (s *Server) Stop() error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.stop()
}

func (s *Server) stop() error {
	// Skip if no instance.
	if s.tcpDNS == nil || s.udpDNS == nil {
		return nil
	}

	// Stop the listener.
	_ = s.tcpDNS.Shutdown()
	_ = s.udpDNS.Shutdown()

	// Unset the address.
	s.address = ""
	return nil
}

// Reconfigure updates the listener with a new configuration.
func (s *Server) Reconfigure(address string) error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.reconfigure(address)
}

func (s *Server) reconfigure(address string) error {
	// Get the old address.
	oldAddress := s.address

	// Setup reverter.
	revert := revert.New()
	defer revert.Fail()

	// Stop the listener.
	err := s.stop()
	if err != nil {
		return err
	}

	// Check if we should start.
	if address != "" {
		// Restore old address on failure.
		revert.Add(func() { _ = s.start(oldAddress) })

		// Start the listener with the new address.
		err = s.start(address)
		if err != nil {
			return err
		}
	}

	// All done.
	revert.Success()
	return nil
}

// UpdateTSIG fetches all TSIG keys and loads them into the DNS server.
func (s *Server) UpdateTSIG() error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.updateTSIG()
}

func (s *Server) updateTSIG() error {
	// Skip if no instance.
	if s.tcpDNS == nil || s.udpDNS == nil || s.db == nil {
		return nil
	}

	var secrets map[string]string

	err := s.db.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get all the secrets.
		secrets, err = tx.GetNetworkZoneKeys(ctx)

		return err
	})
	if err != nil {
		return err
	}

	// Apply to the DNS servers.
	s.tcpDNS.TsigSecret = secrets
	s.udpDNS.TsigSecret = secrets

	return nil
}
