package bgp

import (
	"context"
	"net"
	"testing"

	bgpAPI "github.com/osrg/gobgp/v4/api"
	"github.com/stretchr/testify/require"
)

func TestConfigurePreservesSharedPeer(t *testing.T) {
	for _, running := range []bool{false, true} {
		name := "queued"
		if running {
			name = "running"
		}

		t.Run(name, func(t *testing.T) {
			s := NewServer()
			routerID := net.ParseIP("192.0.2.1")
			address := net.ParseIP("127.0.0.2")
			t.Cleanup(func() {
				native := s.bgp
				require.NoError(t, s.Configure("", 0, nil))
				require.Nil(t, s.bgp)
				if native != nil {
					err := native.ListPeer(context.Background(), &bgpAPI.ListPeerRequest{}, func(*bgpAPI.Peer) {})
					require.ErrorContains(t, err, "server stopped")
				}
			})

			// A negative port disables GoBGP's TCP listener.
			if running {
				require.NoError(t, s.Configure("127.0.0.1:-1", 64512, routerID))
			}

			require.NoError(t, s.AddPeer(address, "", 64513, "shared-peer", 90))
			require.NoError(t, s.AddPeer(address, "", 64513, "shared-peer", 90))
			oldNative := s.bgp
			require.NoError(t, s.Configure("127.0.0.1:-1", 64514, routerID))
			if oldNative != nil {
				err := oldNative.ListPeer(context.Background(), &bgpAPI.ListPeerRequest{}, func(*bgpAPI.Peer) {})
				require.ErrorContains(t, err, "server stopped")
			}

			checkNativePeer := func(want int) {
				t.Helper()
				peers := []*bgpAPI.Peer{}
				err := s.bgp.ListPeer(context.Background(), &bgpAPI.ListPeerRequest{}, func(p *bgpAPI.Peer) {
					peers = append(peers, p)
				})
				require.NoError(t, err)
				require.Len(t, peers, want)
				if want > 0 {
					require.Equal(t, address.String(), peers[0].Conf.NeighborAddress)
					require.Equal(t, uint32(64513), peers[0].Conf.PeerAsn)
					require.Equal(t, "shared-peer", peers[0].Conf.AuthPassword)
					require.Equal(t, uint64(90), peers[0].Timers.Config.HoldTime)
				}
			}

			checkNativePeer(1)
			require.NoError(t, s.RemovePeer(address, ""))
			checkNativePeer(1)
			require.Equal(t, 1, s.peers[peerKey(address, "")].count)
			require.NoError(t, s.RemovePeer(address, ""))
			checkNativePeer(0)
			require.Empty(t, s.peers)
			require.ErrorIs(t, s.RemovePeer(address, ""), ErrPeerNotFound)
		})
	}
}
