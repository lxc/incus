package network

import (
	"net"

	"github.com/cyphar/incus/lxd/cluster"
	"github.com/cyphar/incus/lxd/cluster/request"
	"github.com/cyphar/incus/lxd/db"
	"github.com/cyphar/incus/lxd/state"
	"github.com/cyphar/incus/shared"
	"github.com/cyphar/incus/shared/api"
)

// Type represents a LXD network driver type.
type Type interface {
	FillConfig(config map[string]string) error
	Info() Info
	ValidateName(name string) error
	Type() string
	DBType() db.NetworkType
}

// Network represents an instantiated LXD network.
type Network interface {
	Type

	// Load.
	init(state *state.State, id int64, projectName string, netInfo *api.Network, netNodes map[int64]db.NetworkNode)

	// Config.
	Validate(config map[string]string) error
	ID() int64
	Name() string
	Project() string
	Description() string
	Status() string
	LocalStatus() string
	Config() map[string]string
	Locations() []string
	IsUsed() (bool, error)
	IsManaged() bool
	DHCPv4Subnet() *net.IPNet
	DHCPv6Subnet() *net.IPNet
	DHCPv4Ranges() []shared.IPRange
	DHCPv6Ranges() []shared.IPRange

	// Actions.
	Create(clientType request.ClientType) error
	Start() error
	Stop() error
	Rename(name string) error
	Update(newNetwork api.NetworkPut, targetNode string, clientType request.ClientType) error
	HandleHeartbeat(heartbeatData *cluster.APIHeartbeat) error
	Delete(clientType request.ClientType) error
	handleDependencyChange(netName string, netConfig map[string]string, changedKeys []string) error

	// Status.
	State() (*api.NetworkState, error)
	Leases(projectName string, clientType request.ClientType) ([]api.NetworkLease, error)

	// Address Forwards.
	ForwardCreate(forward api.NetworkForwardsPost, clientType request.ClientType) error
	ForwardUpdate(listenAddress string, newForward api.NetworkForwardPut, clientType request.ClientType) error
	ForwardDelete(listenAddress string, clientType request.ClientType) error

	// Load Balancers.
	LoadBalancerCreate(loadBalancer api.NetworkLoadBalancersPost, clientType request.ClientType) error
	LoadBalancerUpdate(listenAddress string, newLoadBalancer api.NetworkLoadBalancerPut, clientType request.ClientType) error
	LoadBalancerDelete(listenAddress string, clientType request.ClientType) error

	// Peerings.
	PeerCreate(forward api.NetworkPeersPost) error
	PeerUpdate(peerName string, newPeer api.NetworkPeerPut) error
	PeerDelete(peerName string) error
	PeerUsedBy(peerName string) ([]string, error)
}
