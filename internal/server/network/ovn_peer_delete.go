package network

import networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"

// deletePeerPolicies identifies the parent's peers whose anti-spoof rules include this child's routes.
func (n *ovn) deletePeerPolicies() ([]networkOVN.NetworkPeerPolicy, error) {
	if n.parentID == 0 {
		return nil, nil
	}

	owner, err := n.routerOwner()
	if err != nil {
		return nil, err
	}

	peers := []networkOVN.NetworkPeerPolicy{}
	err = owner.forPeers(func(target *ovn) error {
		peers = append(peers, networkOVN.NetworkPeerPolicy{RouterID: target.ID(), PeerID: owner.ID()})
		return nil
	})
	return peers, err
}
