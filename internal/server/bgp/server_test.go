package bgp

import (
	"net"
	"testing"
)

func TestPeerOwnersSurviveRepeatedCleanup(t *testing.T) {
	s := NewServer()
	address := net.ParseIP("192.0.2.1")
	for _, owner := range []string{"network_a", "network_a", "network_b"} {
		err := s.AddPeer(address, "", 64512, "", 30, owner)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(s.peers[address.String()].owners) != 2 {
		t.Fatal("Duplicate owner added a peer reference")
	}

	for range 2 {
		err := s.RemovePeer(address, "", "network_a")
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(s.peers[address.String()].owners) != 1 {
		t.Fatal("Repeated cleanup removed the sibling peer")
	}

	err := s.RemovePeer(address, "", "network_b")
	if err != nil {
		t.Fatal(err)
	}

	if len(s.peers) != 0 {
		t.Fatal("Last owner did not remove the peer")
	}
}
