package dns

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/lxc/incus/v6/internal/ports"
	"github.com/lxc/incus/v6/internal/server/db"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/logger"
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

	cmd chan serverCmdInfo

	mu sync.Mutex
}

type serverCmd int

const (
	serverCmdStart serverCmd = iota
	serverCmdRestart
	serverCmdStop
	serverCmdReconfigure
	serverCmdHandleError
)

type serverCmdInfo struct {
	cmd     serverCmd
	address string
	err     error
}

// NewServer returns a new server instance.
func NewServer(db *db.Cluster, retriever ZoneRetriever) *Server {
	// Setup new struct.
	s := &Server{db: db, zoneRetriever: retriever}
	return s
}

func (s *Server) handleErr(err error) {
	s.cmd <- serverCmdInfo{
		cmd: serverCmdHandleError,
		err: err,
	}
}

func (s *Server) runDNSServer() {
	shouldRun := false
	address := ""

	for cmd := range s.cmd {
		switch cmd.cmd {
		case serverCmdStart:
			if shouldRun {
				continue
			}

			shouldRun = true
			address = cmd.address
			s.mu.Lock()
			err := s.start(cmd.address)
			if err != nil {
				// Run in new goroutine to avoid deadlock.
				go s.handleErr(err)
			}

			s.mu.Unlock()
		case serverCmdRestart:
			s.mu.Lock()
			// don't start if the server shouldn't run or is already running (s.address is set when the server starts)
			if !shouldRun || s.address != "" {
				s.mu.Unlock()
				continue
			}

			err := s.start(address)
			if err != nil {
				// Run in new goroutine to avoid deadlock.
				go s.handleErr(err)
			}

			s.mu.Unlock()
		case serverCmdStop:
			shouldRun = false
			s.mu.Lock()
			s.stop()
			s.mu.Unlock()
		case serverCmdReconfigure:
			s.mu.Lock()
			s.stop()

			if cmd.address == "" {
				shouldRun = false
			} else {
				shouldRun = true
				address = cmd.address
				err := s.start(cmd.address)
				if err != nil {
					// Run in new goroutine to avoid deadlock.
					go s.handleErr(err)
				}
			}

			s.mu.Unlock()
		case serverCmdHandleError:
			if cmd.err == nil {
				continue
			}

			logger.Errorf("DNS server encountered an error, restarting in 10s: %v", cmd.err)
			s.mu.Lock()
			s.stop()
			s.mu.Unlock()
			go func() {
				<-time.NewTimer(time.Second * 10).C
				s.cmd <- serverCmdInfo{cmd: serverCmdRestart}
			}()
		}
	}
}

// Start sets up the DNS listener.
func (s *Server) Start(address string) error {
	s.mu.Lock()

	start := s.cmd == nil

	if start {
		s.cmd = make(chan serverCmdInfo)
		go s.runDNSServer()
	}

	s.mu.Unlock()

	if start {
		s.cmd <- serverCmdInfo{
			cmd:     serverCmdStart,
			address: address,
		}
	} else {
		s.cmd <- serverCmdInfo{
			cmd:     serverCmdReconfigure,
			address: address,
		}
	}

	return nil
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
			s.handleErr(fmt.Errorf("Failed to listen on TCP DNS address %q: %v", address, err))
		}
	}()

	s.udpDNS = &dns.Server{Addr: address, Net: "udp", Handler: handler}
	go func() {
		err := s.udpDNS.ListenAndServe()
		if err != nil {
			s.handleErr(fmt.Errorf("Failed to listen on UDP DNS address %q: %v", address, err))
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
	s.cmd <- serverCmdInfo{
		cmd: serverCmdStop,
	}

	return nil
}

func (s *Server) stop() {
	// Skip if no instance.
	if s.tcpDNS == nil || s.udpDNS == nil {
		return
	}

	// Stop the listener.
	_ = s.tcpDNS.Shutdown()
	_ = s.udpDNS.Shutdown()

	// Unset the address.
	s.address = ""
}

// Reconfigure updates the listener with a new configuration.
func (s *Server) Reconfigure(address string) error {
	return s.Start(address)
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
