package bgp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"strconv"
	"sync"

	"github.com/google/uuid"
	bgpAPI "github.com/osrg/gobgp/v4/api"
	bgpAPIutil "github.com/osrg/gobgp/v4/pkg/apiutil"
	bgpPacket "github.com/osrg/gobgp/v4/pkg/packet/bgp"
	bgpServer "github.com/osrg/gobgp/v4/pkg/server"

	"github.com/lxc/incus/v6/internal/ports"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
)

// Server represents a BGP server instance.
type Server struct {
	bgp *bgpServer.BgpServer

	// Internal state (to handle reconfiguration)
	address  string
	asn      uint32
	routerID net.IP
	paths    map[string]path
	peers    map[string]peer

	mu sync.Mutex
}

type path struct {
	owner   string
	prefix  net.IPNet
	nexthop net.IP
}

type peer struct {
	address  net.IP
	asn      uint32
	password string
	holdtime uint64
	count    int
}

// NewServer returns a new server instance.
func NewServer() *Server {
	// Setup new struct.
	s := &Server{
		paths: map[string]path{},
		peers: map[string]peer{},
	}

	return s
}

func (s *Server) setup() {
}

// Start sets up the BGP listener.
func (s *Server) start(address string, asn uint32, routerID net.IP) error {
	// If routerID is nil, fill with our best guess.
	if routerID == nil || routerID.To4() == nil {
		return ErrBadRouterID
	}

	// Check if already running
	if s.bgp != nil {
		return errors.New("BGP listener is already running")
	}

	// Setup the logger.
	logHandler := logger.NewSlogHandler("bgp:", logger.Warn)
	logLevel := &slog.LevelVar{}
	logLevel.Set(slog.LevelWarn)

	// Spawn the BGP goroutines.
	s.bgp = bgpServer.NewBgpServer(bgpServer.LoggerOption(slog.New(logHandler), logLevel))
	go s.bgp.Serve()

	// Get the address and port.
	addrHost, addrPort, err := net.SplitHostPort(address)
	if err != nil {
		addrHost = address
		addrPort = fmt.Sprintf("%d", ports.BGPDefaultPort)
	}

	if addrHost == "" {
		addrHost = "::"
	}

	addrPortInt, err := strconv.ParseInt(addrPort, 10, 32)
	if err != nil {
		return err
	}

	// Setup the listener configuration.
	conf := &bgpAPI.Global{
		RouterId: routerID.String(),
		Asn:      asn,

		// Always setup for IPv4 and IPv6.
		Families: []uint32{0, 1},

		// Listen address.
		ListenAddresses: []string{addrHost},
		ListenPort:      int32(addrPortInt),
	}

	// Start the listener.
	err = s.bgp.StartBgp(context.Background(), &bgpAPI.StartBgpRequest{Global: conf})
	if err != nil {
		return err
	}

	// Copy the path list
	oldPaths := map[string]path{}
	maps.Copy(oldPaths, s.paths)

	// Add existing paths.
	s.paths = map[string]path{}
	for _, path := range oldPaths {
		err := s.addPrefix(path.prefix, path.nexthop, path.owner)
		if err != nil {
			return err
		}
	}

	// Copy the peer list.
	oldPeers := map[string]peer{}
	maps.Copy(oldPeers, s.peers)

	// Add existing peers.
	s.peers = map[string]peer{}
	for _, peer := range oldPeers {
		err := s.addPeer(peer.address, peer.asn, peer.password, peer.holdtime)
		if err != nil {
			return err
		}
	}

	// Record the address.
	s.address = address
	s.asn = asn
	s.routerID = routerID

	return nil
}

// Stop tears down the BGP listener.
func (s *Server) stop() error {
	// Skip if no instance.
	if s.bgp == nil {
		return nil
	}

	// Save the peer list.
	oldPeers := map[string]peer{}
	maps.Copy(oldPeers, s.peers)

	// Remove all the peers.
	for _, peer := range s.peers {
		err := s.removePeer(peer.address)
		if err != nil {
			return err
		}
	}

	// Restore peer list.
	s.peers = oldPeers

	// Stop the listener.
	err := s.bgp.StopBgp(context.Background(), &bgpAPI.StopBgpRequest{})
	if err != nil {
		return err
	}

	// Mark the daemon as down.
	s.address = ""
	s.asn = 0
	s.routerID = nil
	s.bgp = nil

	return nil
}

// Configure updates the listener with a new configuration..
func (s *Server) Configure(address string, asn uint32, routerID net.IP) error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.configure(address, asn, routerID)
}

func (s *Server) configure(address string, asn uint32, routerID net.IP) error {
	// Store current configuration for reverting.
	oldAddress := s.address
	oldASN := s.asn
	oldRouterID := s.routerID

	// Setup reverter.
	reverter := revert.New()
	defer reverter.Fail()

	// Stop the listener.
	err := s.stop()
	if err != nil {
		return fmt.Errorf("Failed to stop current listener: %w", err)
	}

	// Check if we should start.
	if address != "" && asn > 0 && routerID != nil {
		// Restore old address on failure.
		reverter.Add(func() { _ = s.start(oldAddress, oldASN, oldRouterID) })

		// Start the listener with the new address.
		err = s.start(address, asn, routerID)
		if err != nil {
			return fmt.Errorf("Failed to start new listener: %w", err)
		}
	}

	// All done.
	reverter.Success()
	return nil
}

// AddPrefix adds a new prefix to the BGP server.
func (s *Server) AddPrefix(subnet net.IPNet, nexthop net.IP, owner string) error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.addPrefix(subnet, nexthop, owner)
}

func (s *Server) addPrefix(subnet net.IPNet, nexthop net.IP, owner string) error {
	// Check for an existing entry.
	for _, path := range s.paths {
		if path.owner != owner || path.prefix.String() != subnet.String() || path.nexthop.String() != nexthop.String() {
			continue
		}

		return nil
	}

	// Prepare the prefix.
	prefixLen, _ := subnet.Mask.Size()
	prefix := subnet.IP.String()

	nlri := &bgpAPI.NLRI{Nlri: &bgpAPI.NLRI_Prefix{Prefix: &bgpAPI.IPAddressPrefix{
		Prefix:    prefix,
		PrefixLen: uint32(prefixLen),
	}}}

	aOrigin := &bgpAPI.Attribute_Origin{Origin: &bgpAPI.OriginAttribute{
		Origin: 0,
	}}

	// Add the prefix to the server.
	var pathUUID string
	if s.bgp != nil {
		if subnet.IP.To4() != nil {
			// IPv4 prefix.
			family := &bgpAPI.Family{
				Afi:  bgpAPI.Family_AFI_IP,
				Safi: bgpAPI.Family_SAFI_UNICAST,
			}

			aNextHop := &bgpAPI.Attribute_NextHop{NextHop: &bgpAPI.NextHopAttribute{
				NextHop: nexthop.String(),
			}}

			path := &bgpAPI.Path{
				Family: family,
				Nlri:   nlri,
				Pattrs: []*bgpAPI.Attribute{
					{
						Attr: aOrigin,
					},
					{
						Attr: aNextHop,
					},
				},
			}

			utilNlri, err := bgpAPIutil.GetNativeNlri(path)
			if err != nil {
				return err
			}

			utilAttrs, err := bgpAPIutil.GetNativePathAttributes(path)
			if err != nil {
				return err
			}

			utilPath := &bgpAPIutil.Path{
				Family: bgpPacket.NewFamily(bgpPacket.AFI_IP, bgpPacket.SAFI_UNICAST),
				Nlri:   utilNlri,
				Attrs:  utilAttrs,
			}

			resp, err := s.bgp.AddPath(bgpAPIutil.AddPathRequest{
				Paths: []*bgpAPIutil.Path{utilPath},
			})
			if err != nil {
				return err
			}

			if len(resp) != 1 {
				return errors.New("Expected single response from AddPath")
			}

			pathUUID = string(resp[0].UUID.String())
		} else {
			// IPv6 prefix.
			family := &bgpAPI.Family{
				Afi:  bgpAPI.Family_AFI_IP6,
				Safi: bgpAPI.Family_SAFI_UNICAST,
			}

			v6Attrs := &bgpAPI.Attribute_MpReach{MpReach: &bgpAPI.MpReachNLRIAttribute{
				Family:   family,
				NextHops: []string{nexthop.String()},
				Nlris:    []*bgpAPI.NLRI{nlri},
			}}

			path := &bgpAPI.Path{
				Family: family,
				Nlri:   nlri,
				Pattrs: []*bgpAPI.Attribute{
					{
						Attr: aOrigin,
					},
					{
						Attr: v6Attrs,
					},
				},
			}

			utilNlri, err := bgpAPIutil.GetNativeNlri(path)
			if err != nil {
				return err
			}

			utilAttrs, err := bgpAPIutil.GetNativePathAttributes(path)
			if err != nil {
				return err
			}

			utilPath := &bgpAPIutil.Path{
				Family: bgpPacket.NewFamily(bgpPacket.AFI_IP6, bgpPacket.SAFI_UNICAST),
				Nlri:   utilNlri,
				Attrs:  utilAttrs,
			}

			resp, err := s.bgp.AddPath(bgpAPIutil.AddPathRequest{
				Paths: []*bgpAPIutil.Path{utilPath},
			})
			if err != nil {
				return err
			}

			if len(resp) != 1 {
				return errors.New("Expected single response from AddPath")
			}

			pathUUID = string(resp[0].UUID.String())
		}
	} else {
		// Generate a dummy UUID.
		pathUUID = uuid.New().String()
	}

	// Add path to the map.
	s.paths[pathUUID] = path{
		prefix:  subnet,
		nexthop: nexthop,
		owner:   owner,
	}

	return nil
}

// RemovePrefixByOwner removes all prefixes for the provided owner.
func (s *Server) RemovePrefixByOwner(owner string) error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Make a copy of the paths dict to safely iterate (path removal mutates it).
	paths := map[string]path{}
	maps.Copy(paths, s.paths)

	// Iterate through the paths and remove them from the server.
	for pathUUID, path := range paths {
		if path.owner == owner {
			err := s.removePrefixByUUID(pathUUID)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// RemovePrefix removes a prefix from the BGP server.
func (s *Server) RemovePrefix(subnet net.IPNet, nexthop net.IP) error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.removePrefix(subnet, nexthop)
}

func (s *Server) removePrefix(subnet net.IPNet, nexthop net.IP) error {
	found := false
	for pathUUID, path := range s.paths {
		if path.prefix.String() != subnet.String() || path.nexthop.String() != nexthop.String() {
			continue
		}

		found = true

		// Remove the prefix.
		err := s.removePrefixByUUID(pathUUID)
		if err != nil {
			return err
		}
	}

	if !found {
		return ErrPrefixNotFound
	}

	return nil
}

func (s *Server) removePrefixByUUID(pathUUID string) error {
	// Remove it from the BGP server.
	if s.bgp != nil {
		nativeUUID, err := uuid.Parse(pathUUID)
		if err != nil {
			return err
		}

		err = s.bgp.DeletePath(bgpAPIutil.DeletePathRequest{UUIDs: []uuid.UUID{nativeUUID}})
		if err != nil && err.Error() != "can't find a specified path" {
			return err
		}
	}

	// Remove the path from the map.
	delete(s.paths, pathUUID)

	return nil
}

// AddPeer adds a new BGP peer.
func (s *Server) AddPeer(address net.IP, asn uint32, password string, holdTime uint64) error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.addPeer(address, asn, password, holdTime)
}

func (s *Server) addPeer(address net.IP, asn uint32, password string, holdTime uint64) error {
	// Look for an existing peer.
	bgpPeer, bgpPeerExists := s.peers[address.String()]
	if bgpPeerExists {
		if bgpPeer.asn != asn {
			return fmt.Errorf("Peer %q already used but with differing ASN (%d vs %d)", address, asn, bgpPeer.asn)
		}

		if bgpPeer.password != password {
			return fmt.Errorf("Peer %q already used but with a different password", address)
		}

		// Reuse the existing entry.
		bgpPeer.count++
		s.peers[address.String()] = bgpPeer
		return nil
	}

	// Setup the configuration.
	n := &bgpAPI.Peer{
		// Peer information.
		Conf: &bgpAPI.PeerConf{
			NeighborAddress: address.String(),
			PeerAsn:         uint32(asn),
			AuthPassword:    password,
		},

		// Allow for 120s offline before route removal.
		GracefulRestart: &bgpAPI.GracefulRestart{
			Enabled:     true,
			RestartTime: 3600,
		},

		// Always allow for the maximum multihop.
		EbgpMultihop: &bgpAPI.EbgpMultihop{
			Enabled:     true,
			MultihopTtl: 255,
		},
	}

	// Add hold time if configured.
	if holdTime > 0 {
		n.Timers = &bgpAPI.Timers{
			Config: &bgpAPI.TimersConfig{
				HoldTime: holdTime,
			},
		}
	}

	// Setup peer for dual-stack.
	n.AfiSafis = make([]*bgpAPI.AfiSafi, 0)
	for _, f := range []string{"ipv4-unicast", "ipv6-unicast"} {
		rf, err := bgpPacket.GetFamily(f)
		if err != nil {
			return err
		}

		afi := rf.Afi()
		safi := rf.Safi()
		family := &bgpAPI.Family{
			Afi:  bgpAPI.Family_Afi(afi),
			Safi: bgpAPI.Family_Safi(safi),
		}

		n.AfiSafis = append(n.AfiSafis, &bgpAPI.AfiSafi{
			MpGracefulRestart: &bgpAPI.MpGracefulRestart{
				Config: &bgpAPI.MpGracefulRestartConfig{
					Enabled: true,
				},
			},
			Config: &bgpAPI.AfiSafiConfig{Family: family},
		})
	}

	// Add the peer.
	if s.bgp != nil {
		err := s.bgp.AddPeer(context.Background(), &bgpAPI.AddPeerRequest{Peer: n})
		if err != nil {
			return err
		}
	}

	// Add the peer to the list.
	if bgpPeerExists {
		bgpPeer.count++
		s.peers[address.String()] = bgpPeer
	} else {
		s.peers[address.String()] = peer{
			address:  address,
			asn:      asn,
			password: password,
			holdtime: holdTime,
			count:    1,
		}
	}

	return nil
}

// RemovePeer removes a prefix from the BGP server.
func (s *Server) RemovePeer(address net.IP) error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.removePeer(address)
}

func (s *Server) removePeer(address net.IP) error {
	// Find the peer.
	bgpPeer, bgpPeerExists := s.peers[address.String()]
	if !bgpPeerExists {
		return ErrPeerNotFound
	}

	// Remove the peer from the BGP server.
	if s.bgp != nil && bgpPeer.count == 1 {
		err := s.bgp.DeletePeer(context.Background(), &bgpAPI.DeletePeerRequest{Address: address.String()})
		if err != nil {
			return err
		}
	}

	// Update peer list.
	if bgpPeer.count == 1 {
		// Delete the peer.
		delete(s.peers, address.String())
	} else {
		// Decrease refcount.
		bgpPeer.count--
		s.peers[address.String()] = bgpPeer
	}

	return nil
}
