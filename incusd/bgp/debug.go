package bgp

// DebugInfo represents the internal debug state of the BGP server.
type DebugInfo struct {
	Server   DebugInfoServer   `json:"server" yaml:"server"`
	Prefixes []DebugInfoPrefix `json:"prefixes" yaml:"prefixes"`
	Peers    []DebugInfoPeer   `json:"peers" yaml:"peers"`
}

// DebugInfoServer exposes the shared listener configuration.
type DebugInfoServer struct {
	Address  string `json:"address" yaml:"address"`
	ASN      uint32 `json:"asn" yaml:"asn"`
	RouterID string `json:"router_id" yaml:"router_id"`
	Running  bool   `json:"running" yaml:"running"`
}

// DebugInfoPrefix exposes details on a single BGP prefix.
type DebugInfoPrefix struct {
	Owner   string `json:"owner" yaml:"owner"`
	Prefix  string `json:"prefix" yaml:"prefix"`
	Nexthop string `json:"nexthop" yaml:"nexthop"`
}

// DebugInfoPeer exposes details on a single BGP peer.
type DebugInfoPeer struct {
	Address  string `json:"address" yaml:"address"`
	ASN      uint32 `json:"asn" yaml:"asn"`
	Password string `json:"password" yaml:"password"`
	Count    int    `json:"count" yaml:"count"`
	HoldTime uint64 `json:"holdtime" yaml:"holdtime"`
}

// Debug returns a dump of the current configuration.
func (s *Server) Debug() DebugInfo {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.debug()
}

func (s *Server) debug() DebugInfo {
	debug := DebugInfo{}

	// Fill in server state.
	debug.Server.Running = s.bgp != nil
	debug.Server.ASN = s.asn
	debug.Server.Address = s.address
	debug.Server.RouterID = s.routerID.String()

	// Fill in the peers.
	debug.Peers = []DebugInfoPeer{}
	for _, peer := range s.peers {
		entry := DebugInfoPeer{}
		entry.Address = peer.address.String()
		entry.ASN = peer.asn
		entry.Password = peer.password
		entry.Count = peer.count
		entry.HoldTime = peer.holdtime

		debug.Peers = append(debug.Peers, entry)
	}

	// Fill in the prefixes.
	debug.Prefixes = []DebugInfoPrefix{}
	for _, path := range s.paths {
		entry := DebugInfoPrefix{}
		entry.Prefix = path.prefix.String()
		entry.Owner = path.owner
		entry.Nexthop = path.nexthop.String()

		debug.Prefixes = append(debug.Prefixes, entry)
	}

	return debug
}
