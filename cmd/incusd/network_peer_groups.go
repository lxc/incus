package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"

	"github.com/lxc/incus/v7/internal/server/auth"
	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/lifecycle"
	"github.com/lxc/incus/v7/internal/server/network"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/internal/server/state"
	localUtil "github.com/lxc/incus/v7/internal/server/util"
	"github.com/lxc/incus/v7/internal/version"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/revert"
	"github.com/lxc/incus/v7/shared/validate"
)

var networkPeerGroupsCmd = APIEndpoint{
	Path: "network-peer-groups",

	Get:  APIEndpointAction{Handler: networkPeerGroupsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: networkPeerGroupsPost, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanCreateNetworkPeerGroups)},
}

var networkPeerGroupCmd = APIEndpoint{
	Path: "network-peer-groups/{name}",

	Get:    APIEndpointAction{Handler: networkPeerGroupGet, AccessHandler: allowPermission(auth.ObjectTypeNetworkPeerGroup, auth.EntitlementCanView, "name")},
	Put:    APIEndpointAction{Handler: networkPeerGroupPut, AccessHandler: allowPermission(auth.ObjectTypeNetworkPeerGroup, auth.EntitlementCanEdit, "name")},
	Delete: APIEndpointAction{Handler: networkPeerGroupDelete, AccessHandler: allowPermission(auth.ObjectTypeNetworkPeerGroup, auth.EntitlementCanEdit, "name")},
}

// networkPeerGroupOVNPrefix returns the OVN name prefix for a network peer group, keyed by its
// numeric DB ID. This mirrors the "incus-net<ID>" convention used for networks (see
// acl.OVNNetworkPrefix).
func networkPeerGroupOVNPrefix(networkPeerGroupID int64) string {
	return fmt.Sprintf("incus-pg%d", networkPeerGroupID)
}

// networkPeerGroupLogicalSwitchName returns the name of the OVN logical switch backing a network peer group.
func networkPeerGroupLogicalSwitchName(networkPeerGroupID int64) networkOVN.OVNSwitch {
	return networkOVN.OVNSwitch(fmt.Sprintf("%s-ls", networkPeerGroupOVNPrefix(networkPeerGroupID)))
}

// networkPeerGroupLogicalRouterName returns the name of the OVN logical router backing a network peer group.
func networkPeerGroupLogicalRouterName(networkPeerGroupID int64) networkOVN.OVNRouter {
	return networkOVN.OVNRouter(fmt.Sprintf("%s-lr", networkPeerGroupOVNPrefix(networkPeerGroupID)))
}

// networkPeerGroupInternalRouterPortName returns the OVN router port name connecting a network peer
// group's router to its switch.
func networkPeerGroupInternalRouterPortName(networkPeerGroupID int64) networkOVN.OVNRouterPort {
	return networkOVN.OVNRouterPort(fmt.Sprintf("%s-lrp-int", networkPeerGroupOVNPrefix(networkPeerGroupID)))
}

// networkPeerGroupInternalSwitchPortName returns the OVN switch port name connecting a network peer
// group's switch to its router.
func networkPeerGroupInternalSwitchPortName(networkPeerGroupID int64) networkOVN.OVNSwitchPort {
	return networkOVN.OVNSwitchPort(fmt.Sprintf("%s-lsp-int", networkPeerGroupOVNPrefix(networkPeerGroupID)))
}

// networkPeerGroupNetworkPortNames returns the OVN router port (on the member network's own router) and
// switch port (on the network peer group's switch) names used to connect a member network to a network peer group.
func networkPeerGroupNetworkPortNames(networkPeerGroupSwitchName networkOVN.OVNSwitch, networkID int64) (networkOVN.OVNRouterPort, networkOVN.OVNSwitchPort) {
	routerPortName := networkOVN.OVNRouterPort(fmt.Sprintf("%s-lrp-net%d", networkPeerGroupSwitchName, networkID))
	switchPortName := networkOVN.OVNSwitchPort(fmt.Sprintf("%s-lsp-net%d", networkPeerGroupSwitchName, networkID))
	return routerPortName, switchPortName
}

// networkPeerGroupInternalLinkMAC generates a random MAC for a router port on a network peer group's
// switch. The link carries no user traffic, so any valid, locally administered, unicast MAC will do.
func networkPeerGroupInternalLinkMAC() net.HardwareAddr {
	mac := make(net.HardwareAddr, 6)
	for i := range mac {
		mac[i] = byte(rand.Intn(256))
	}

	mac[0] = (mac[0] | 0x02) &^ 0x01

	return mac
}

// networkPeerGroupLinkAddresses returns the IPv4/IPv6 addresses for the router port at the given link
// index in the network peer group's internal (never user-facing) addressing scheme. Index 1 is
// reserved for the network peer group's own router port; members are allocated indexes from 2 (see
// (*db.ClusterTx).AddNetworkToNetworkPeerGroup).
func networkPeerGroupLinkAddresses(linkIndex int) (*net.IPNet, *net.IPNet) {
	ipv4 := &net.IPNet{
		IP:   net.IPv4(169, 254, 0, byte(linkIndex)),
		Mask: net.CIDRMask(24, 32),
	}

	ipv6 := &net.IPNet{
		IP:   net.ParseIP(fmt.Sprintf("fd42:1:1:1::%x", linkIndex)),
		Mask: net.CIDRMask(64, 128),
	}

	return ipv4, ipv6
}

// networkPeerGroupInternalLinkNetworks returns the IP network(s) assigned to the router port used to
// connect the network peer group's own router to its switch.
func networkPeerGroupInternalLinkNetworks() []*net.IPNet {
	ipv4, ipv6 := networkPeerGroupLinkAddresses(1)
	return []*net.IPNet{ipv4, ipv6}
}

// networkPeerGroupNormalizeNetwork defaults an unset project to the default project.
func networkPeerGroupNormalizeNetwork(networkPeerGroupNetwork api.NetworkPeerGroupNetwork) api.NetworkPeerGroupNetwork {
	if networkPeerGroupNetwork.Project == "" {
		networkPeerGroupNetwork.Project = api.ProjectDefaultName
	}

	return networkPeerGroupNetwork
}

// networkPeerGroupNetworksContains returns true if list contains a network matching networkPeerGroupNetwork.
func networkPeerGroupNetworksContains(list []api.NetworkPeerGroupNetwork, networkPeerGroupNetwork api.NetworkPeerGroupNetwork) bool {
	for _, item := range list {
		if item.Name == networkPeerGroupNetwork.Name && item.Project == networkPeerGroupNetwork.Project {
			return true
		}
	}

	return false
}

// networkPeerGroupMembershipsToAPI converts internal network peer group network memberships to their
// API representation (dropping internal-only fields such as the OVN link index).
func networkPeerGroupMembershipsToAPI(memberships []db.NetworkPeerGroupNetworkMembership) []api.NetworkPeerGroupNetwork {
	networks := make([]api.NetworkPeerGroupNetwork, 0, len(memberships))
	for _, membership := range memberships {
		networks = append(networks, api.NetworkPeerGroupNetwork{Name: membership.Name, Project: membership.Project})
	}

	return networks
}

// networkPeerGroupLoadNetworkSubnets loads the network of the given network peer group membership and
// returns its own configured IPv4/IPv6 subnet(s) (nil for a family that isn't configured).
func networkPeerGroupLoadNetworkSubnets(s *state.State, membership db.NetworkPeerGroupNetworkMembership) (network.Network, *net.IPNet, *net.IPNet, error) {
	netIface, err := network.LoadByName(s, membership.Project, membership.Name)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Failed loading network %q in project %q: %w", membership.Name, membership.Project, err)
	}

	ipv4Net, ipv6Net, err := netIface.PeerGroupSubnets()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Failed getting subnets for network %q in project %q: %w", membership.Name, membership.Project, err)
	}

	return netIface, ipv4Net, ipv6Net, nil
}

// networkPeerGroupSetNetworks reconciles the desired member network list for a network peer group
// against its current membership, attaching/detaching networks as needed (see
// networkPeerGroupAttachNetwork and networkPeerGroupDetachNetwork).
func networkPeerGroupSetNetworks(ctx context.Context, s *state.State, networkPeerGroupID int64, networkPeerGroupName string, desired []api.NetworkPeerGroupNetwork) error {
	normalizedDesired := make([]api.NetworkPeerGroupNetwork, len(desired))
	for i, networkPeerGroupNetwork := range desired {
		normalizedDesired[i] = networkPeerGroupNormalizeNetwork(networkPeerGroupNetwork)
	}

	var current []db.NetworkPeerGroupNetworkMembership

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		current, err = tx.GetNetworkPeerGroupNetworkMemberships(ctx, networkPeerGroupName)
		return err
	})
	if err != nil {
		return err
	}

	var toRemove []db.NetworkPeerGroupNetworkMembership

	remaining := make([]db.NetworkPeerGroupNetworkMembership, 0, len(current))
	for _, membership := range current {
		if networkPeerGroupNetworksContains(normalizedDesired, api.NetworkPeerGroupNetwork{Name: membership.Name, Project: membership.Project}) {
			remaining = append(remaining, membership)
		} else {
			toRemove = append(toRemove, membership)
		}
	}

	var toAdd []api.NetworkPeerGroupNetwork

	for _, networkPeerGroupNetwork := range normalizedDesired {
		found := false
		for _, membership := range current {
			if membership.Name == networkPeerGroupNetwork.Name && membership.Project == networkPeerGroupNetwork.Project {
				found = true
				break
			}
		}

		if !found {
			toAdd = append(toAdd, networkPeerGroupNetwork)
		}
	}

	for _, membership := range toRemove {
		// Clean up against every other network it has routes with, not just survivors, so
		// nothing is left dangling if several members are removed at once.
		othersExcludingSelf := make([]db.NetworkPeerGroupNetworkMembership, 0, len(current)-1)
		for _, other := range current {
			if other.NetworkID != membership.NetworkID {
				othersExcludingSelf = append(othersExcludingSelf, other)
			}
		}

		err := networkPeerGroupDetachNetwork(ctx, s, networkPeerGroupID, networkPeerGroupName, membership, othersExcludingSelf)
		if err != nil {
			return err
		}
	}

	for _, networkPeerGroupNetwork := range toAdd {
		membership, err := networkPeerGroupAttachNetwork(ctx, s, networkPeerGroupID, networkPeerGroupName, networkPeerGroupNetwork, remaining)
		if err != nil {
			return err
		}

		remaining = append(remaining, *membership)
	}

	return nil
}

// networkPeerGroupAttachNetwork validates and attaches a single network to a network peer group.
// otherMembers is the network peer group's current membership, excluding the network being attached.
func networkPeerGroupAttachNetwork(ctx context.Context, s *state.State, networkPeerGroupID int64, networkPeerGroupName string, networkPeerGroupNetwork api.NetworkPeerGroupNetwork, otherMembers []db.NetworkPeerGroupNetworkMembership) (*db.NetworkPeerGroupNetworkMembership, error) {
	netIface, err := network.LoadByName(s, networkPeerGroupNetwork.Project, networkPeerGroupNetwork.Name)
	if err != nil {
		return nil, fmt.Errorf("Failed loading network %q in project %q: %w", networkPeerGroupNetwork.Name, networkPeerGroupNetwork.Project, err)
	}

	if !netIface.Info().PeerGroups {
		return nil, fmt.Errorf("Network %q in project %q does not support network peer groups", networkPeerGroupNetwork.Name, networkPeerGroupNetwork.Project)
	}

	ipv4Net, ipv6Net, err := netIface.PeerGroupSubnets()
	if err != nil {
		return nil, fmt.Errorf("Failed getting subnets for network %q in project %q: %w", networkPeerGroupNetwork.Name, networkPeerGroupNetwork.Project, err)
	}

	if ipv4Net == nil && ipv6Net == nil {
		return nil, fmt.Errorf("Network %q in project %q has no configured subnet", networkPeerGroupNetwork.Name, networkPeerGroupNetwork.Project)
	}

	// Check for duplicate/overlapping subnets against the other current members.
	for _, other := range otherMembers {
		_, otherIPv4Net, otherIPv6Net, err := networkPeerGroupLoadNetworkSubnets(s, other)
		if err != nil {
			return nil, err
		}

		if ipv4Net != nil && otherIPv4Net != nil && (network.SubnetContains(ipv4Net, otherIPv4Net) || network.SubnetContains(otherIPv4Net, ipv4Net)) {
			return nil, fmt.Errorf("Network %q in project %q has a subnet that overlaps with network %q in project %q", networkPeerGroupNetwork.Name, networkPeerGroupNetwork.Project, other.Name, other.Project)
		}

		if ipv6Net != nil && otherIPv6Net != nil && (network.SubnetContains(ipv6Net, otherIPv6Net) || network.SubnetContains(otherIPv6Net, ipv6Net)) {
			return nil, fmt.Errorf("Network %q in project %q has a subnet that overlaps with network %q in project %q", networkPeerGroupNetwork.Name, networkPeerGroupNetwork.Project, other.Name, other.Project)
		}
	}

	ovnnb, _, err := s.OVN()
	if err != nil {
		return nil, fmt.Errorf("Failed to get OVN client: %w", err)
	}

	// Allocate a link index and record the DB membership row.
	var linkIndex int
	var networkID int64

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		linkIndex, networkID, err = tx.AddNetworkToNetworkPeerGroup(ctx, networkPeerGroupName, networkPeerGroupNetwork.Project, networkPeerGroupNetwork.Name)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("Failed recording network %q in project %q as a network peer group member: %w", networkPeerGroupNetwork.Name, networkPeerGroupNetwork.Project, err)
	}

	reverter := revert.New()
	defer reverter.Fail()

	reverter.Add(func() {
		_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.RemoveNetworkFromNetworkPeerGroup(ctx, networkPeerGroupName, networkPeerGroupNetwork.Project, networkPeerGroupNetwork.Name)
		})
	})

	networkPeerGroupSwitchName := networkPeerGroupLogicalSwitchName(networkPeerGroupID)
	routerPortName, switchPortName := networkPeerGroupNetworkPortNames(networkPeerGroupSwitchName, networkID)
	linkIPv4, linkIPv6 := networkPeerGroupLinkAddresses(linkIndex)

	err = netIface.PeerGroupJoin(string(routerPortName), string(switchPortName), string(networkPeerGroupSwitchName), linkIPv4, linkIPv6)
	if err != nil {
		return nil, fmt.Errorf("Failed attaching network %q in project %q to network peer group: %w", networkPeerGroupNetwork.Name, networkPeerGroupNetwork.Project, err)
	}

	reverter.Add(func() {
		_ = netIface.PeerGroupLeave(string(routerPortName), string(switchPortName), string(networkPeerGroupSwitchName))
	})

	// Route on the network peer group's own router to the new member's subnet(s), via the new member's link address.
	networkPeerGroupRouterName := networkPeerGroupLogicalRouterName(networkPeerGroupID)
	networkPeerGroupRouterPortName := networkPeerGroupInternalRouterPortName(networkPeerGroupID)

	if ipv4Net != nil {
		err = ovnnb.CreateLogicalRouterRoute(ctx, networkPeerGroupRouterName, true, networkOVN.OVNRouterRoute{Prefix: *ipv4Net, NextHop: linkIPv4.IP, Port: networkPeerGroupRouterPortName})
		if err != nil {
			return nil, fmt.Errorf("Failed adding network peer group router route: %w", err)
		}
	}

	if ipv6Net != nil {
		err = ovnnb.CreateLogicalRouterRoute(ctx, networkPeerGroupRouterName, true, networkOVN.OVNRouterRoute{Prefix: *ipv6Net, NextHop: linkIPv6.IP, Port: networkPeerGroupRouterPortName})
		if err != nil {
			return nil, fmt.Errorf("Failed adding network peer group router route: %w", err)
		}
	}

	// Cross routes with every other current member, via the network peer group's own router.
	networkPeerGroupLinkIPv4, networkPeerGroupLinkIPv6 := networkPeerGroupLinkAddresses(1)

	for _, other := range otherMembers {
		otherNet, otherIPv4Net, otherIPv6Net, err := networkPeerGroupLoadNetworkSubnets(s, other)
		if err != nil {
			return nil, err
		}

		otherRouterPortName, _ := networkPeerGroupNetworkPortNames(networkPeerGroupSwitchName, other.NetworkID)

		if ipv4Net != nil && otherIPv4Net != nil {
			err = otherNet.PeerGroupAddRoute(string(otherRouterPortName), *ipv4Net, networkPeerGroupLinkIPv4.IP)
			if err != nil {
				return nil, fmt.Errorf("Failed adding route on network %q in project %q: %w", other.Name, other.Project, err)
			}

			err = netIface.PeerGroupAddRoute(string(routerPortName), *otherIPv4Net, networkPeerGroupLinkIPv4.IP)
			if err != nil {
				return nil, fmt.Errorf("Failed adding route on network %q in project %q: %w", networkPeerGroupNetwork.Name, networkPeerGroupNetwork.Project, err)
			}
		}

		if ipv6Net != nil && otherIPv6Net != nil {
			err = otherNet.PeerGroupAddRoute(string(otherRouterPortName), *ipv6Net, networkPeerGroupLinkIPv6.IP)
			if err != nil {
				return nil, fmt.Errorf("Failed adding route on network %q in project %q: %w", other.Name, other.Project, err)
			}

			err = netIface.PeerGroupAddRoute(string(routerPortName), *otherIPv6Net, networkPeerGroupLinkIPv6.IP)
			if err != nil {
				return nil, fmt.Errorf("Failed adding route on network %q in project %q: %w", networkPeerGroupNetwork.Name, networkPeerGroupNetwork.Project, err)
			}
		}
	}

	reverter.Success()

	return &db.NetworkPeerGroupNetworkMembership{NetworkID: networkID, Name: networkPeerGroupNetwork.Name, Project: networkPeerGroupNetwork.Project, LinkIndex: linkIndex}, nil
}

// networkPeerGroupDetachNetwork detaches a single network from a network peer group. otherMembers is
// every other network that currently has routes against this one (whether or not it's also being
// removed in this same call - see networkPeerGroupSetNetworks). If the network can no longer be
// loaded (e.g. deleted out-of-band), OVN cleanup is skipped and only the DB membership record is
// removed.
func networkPeerGroupDetachNetwork(ctx context.Context, s *state.State, networkPeerGroupID int64, networkPeerGroupName string, membership db.NetworkPeerGroupNetworkMembership, otherMembers []db.NetworkPeerGroupNetworkMembership) error {
	networkPeerGroupSwitchName := networkPeerGroupLogicalSwitchName(networkPeerGroupID)
	routerPortName, switchPortName := networkPeerGroupNetworkPortNames(networkPeerGroupSwitchName, membership.NetworkID)

	netIface, ipv4Net, ipv6Net, err := networkPeerGroupLoadNetworkSubnets(s, membership)
	if err != nil {
		logger.Warn("Failed loading network for network peer group removal, skipping OVN cleanup", logger.Ctx{"network": membership.Name, "project": membership.Project, "error": err})
		netIface = nil
	}

	if netIface != nil {
		// Remove the cross routes with every other member.
		for _, other := range otherMembers {
			otherNet, otherIPv4Net, otherIPv6Net, err := networkPeerGroupLoadNetworkSubnets(s, other)
			if err != nil {
				logger.Warn("Failed loading network for network peer group route cleanup", logger.Ctx{"network": other.Name, "project": other.Project, "error": err})
				continue
			}

			if ipv4Net != nil {
				_ = otherNet.PeerGroupRemoveRoute(*ipv4Net)
			}

			if ipv6Net != nil {
				_ = otherNet.PeerGroupRemoveRoute(*ipv6Net)
			}

			if otherIPv4Net != nil {
				_ = netIface.PeerGroupRemoveRoute(*otherIPv4Net)
			}

			if otherIPv6Net != nil {
				_ = netIface.PeerGroupRemoveRoute(*otherIPv6Net)
			}
		}

		// Remove the network peer group's own route to this network's subnet(s).
		ovnnb, _, err := s.OVN()
		if err != nil {
			return fmt.Errorf("Failed to get OVN client: %w", err)
		}

		networkPeerGroupRouterName := networkPeerGroupLogicalRouterName(networkPeerGroupID)

		if ipv4Net != nil {
			err = ovnnb.DeleteLogicalRouterRoute(ctx, networkPeerGroupRouterName, *ipv4Net)
			if err != nil {
				logger.Warn("Failed removing network peer group router route", logger.Ctx{"network": membership.Name, "project": membership.Project, "error": err})
			}
		}

		if ipv6Net != nil {
			err = ovnnb.DeleteLogicalRouterRoute(ctx, networkPeerGroupRouterName, *ipv6Net)
			if err != nil {
				logger.Warn("Failed removing network peer group router route", logger.Ctx{"network": membership.Name, "project": membership.Project, "error": err})
			}
		}

		// Remove the router port and switch port connecting this network to the network peer group.
		err = netIface.PeerGroupLeave(string(routerPortName), string(switchPortName), string(networkPeerGroupSwitchName))
		if err != nil {
			return fmt.Errorf("Failed detaching network %q in project %q from network peer group: %w", membership.Name, membership.Project, err)
		}
	}

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RemoveNetworkFromNetworkPeerGroup(ctx, networkPeerGroupName, membership.Project, membership.Name)
	})
	if err != nil {
		return fmt.Errorf("Failed removing network %q in project %q from network peer group: %w", membership.Name, membership.Project, err)
	}

	return nil
}

// API endpoints.

// swagger:operation GET /1.0/network-peer-groups network-peer-groups network_peer_groups_get
//
//	Get the network peer groups
//
//	Returns a list of network peer groups (URLs).
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example:
//	            - /1.0/network-peer-groups/region1
//	            - /1.0/network-peer-groups/region2
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/network-peer-groups?recursion=1 network-peer-groups network_peer_groups_get_recursion1
//
//	Get the network peer groups
//
//	Returns a list of network peer groups (structs).
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of network peer groups
//	          items:
//	            $ref: "#/definitions/NetworkPeerGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkPeerGroupsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	recursion := localUtil.IsRecursionRequest(r)

	linkResults := make([]string, 0)
	fullResults := make([]api.NetworkPeerGroup, 0)

	err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		groups, err := dbCluster.GetNetworkPeerGroups(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, group := range groups {
			if recursion {
				info, err := group.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				memberships, err := tx.GetNetworkPeerGroupNetworkMemberships(ctx, group.Name)
				if err != nil {
					return err
				}

				info.Networks = networkPeerGroupMembershipsToAPI(memberships)

				fullResults = append(fullResults, *info)
			}

			linkResults = append(linkResults, api.NewURL().Path(version.APIVersion, "network-peer-groups", group.Name).String())
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if !recursion {
		return response.SyncResponse(true, linkResults)
	}

	return response.SyncResponse(true, fullResults)
}

// swagger:operation POST /1.0/network-peer-groups network-peer-groups network_peer_groups_post
//
//	Add a network peer group
//
//	Creates a new network peer group. This provisions the underlying OVN
//	Logical_Switch and Logical_Router used to peer networks together, and
//	attaches any networks listed in the request to it (see
//	PUT /1.0/network-peer-groups/{name} for details on network attachment).
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: network_peer_group
//	    description: Network peer group
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkPeerGroupsPost"
//	responses:
//	  "201":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "409":
//	    $ref: "#/responses/Conflict"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkPeerGroupsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.NetworkPeerGroupsPost{}

	// Parse the request into a record.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	err = validate.IsAPIName(req.Name, false)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid network peer group name: %w", err))
	}

	ovnnb, _, err := s.OVN()
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get OVN client: %w", err))
	}

	reverter := revert.New()
	defer reverter.Fail()

	// Create the DB record first so the OVN resources below can be named after its ID (mirroring
	// the "incus-net<ID>" convention used for networks).
	var networkPeerGroupID int64

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbRecord := dbCluster.NetworkPeerGroup{
			Name:        req.Name,
			Description: req.Description,
		}

		var err error
		networkPeerGroupID, err = dbCluster.CreateNetworkPeerGroup(ctx, tx.Tx(), dbRecord)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	reverter.Add(func() {
		_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return dbCluster.DeleteNetworkPeerGroup(ctx, tx.Tx(), req.Name)
		})
	})

	// Create the OVN logical switch.
	switchName := networkPeerGroupLogicalSwitchName(networkPeerGroupID)

	err = ovnnb.CreateLogicalSwitch(r.Context(), switchName, false)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to create OVN logical switch: %w", err))
	}

	reverter.Add(func() { _ = ovnnb.DeleteLogicalSwitch(context.Background(), switchName) })

	// Create the OVN logical router.
	routerName := networkPeerGroupLogicalRouterName(networkPeerGroupID)

	err = ovnnb.CreateLogicalRouter(r.Context(), routerName, false)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to create OVN logical router: %w", err))
	}

	reverter.Add(func() { _ = ovnnb.DeleteLogicalRouter(context.Background(), routerName) })

	// Connect the OVN logical router to the OVN logical switch via an internal link.
	lrpName := networkPeerGroupInternalRouterPortName(networkPeerGroupID)

	err = ovnnb.CreateLogicalRouterPort(r.Context(), routerName, lrpName, networkPeerGroupInternalLinkMAC(), 0, networkPeerGroupInternalLinkNetworks(), "", false)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to create OVN logical router port: %w", err))
	}

	reverter.Add(func() { _ = ovnnb.DeleteLogicalRouterPort(context.Background(), routerName, lrpName) })

	lspName := networkPeerGroupInternalSwitchPortName(networkPeerGroupID)

	err = ovnnb.CreateLogicalSwitchPort(r.Context(), switchName, lspName, &networkOVN.OVNSwitchPortOpts{RouterPort: lrpName}, false)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to create OVN logical switch port: %w", err))
	}

	reverter.Add(func() { _ = ovnnb.DeleteLogicalSwitchPort(context.Background(), switchName, lspName) })

	// Attach any requested member networks.
	err = networkPeerGroupSetNetworks(r.Context(), s, networkPeerGroupID, req.Name, req.Networks)
	if err != nil {
		return response.SmartError(err)
	}

	// Add the network peer group to the auth backend.
	err = s.Authorizer.AddNetworkPeerGroup(r.Context(), req.Name)
	if err != nil {
		logger.Error("Failed to add network peer group to authorizer", logger.Ctx{"name": req.Name, "error": err})
	}

	// Emit the lifecycle event.
	lc := lifecycle.NetworkPeerGroupCreated.Event(req.Name, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(api.ProjectDefaultName, lc)

	reverter.Success()

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation GET /1.0/network-peer-groups/{name} network-peer-groups network_peer_group_get
//
//	Get the network peer group
//
//	Gets a specific network peer group.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: path
//	    name: name
//	    description: Network peer group name
//	    type: string
//	    required: true
//	responses:
//	  "200":
//	    description: Network peer group
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/NetworkPeerGroup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkPeerGroupGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the network peer group name.
	name, err := pathVar(r, "name")
	if err != nil {
		return response.SmartError(err)
	}

	var info *api.NetworkPeerGroup

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbRecord, err := dbCluster.GetNetworkPeerGroup(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		info, err = dbRecord.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		memberships, err := tx.GetNetworkPeerGroupNetworkMemberships(ctx, name)
		if err != nil {
			return err
		}

		info.Networks = networkPeerGroupMembershipsToAPI(memberships)

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, info, info.Writable())
}

// swagger:operation PUT /1.0/network-peer-groups/{name} network-peer-groups network_peer_group_put
//
//	Update the network peer group
//
//	Updates the network peer group description and its list of member OVN networks.
//
//	Adding a network attaches its router to the network peer group's switch and
//	sets up routes so it can reach the group's other members. It must
//	already exist, have a configured subnet, and that subnet must not
//	overlap with any other member's. Removing a network reverses this.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: path
//	    name: name
//	    description: Network peer group name
//	    type: string
//	    required: true
//	  - in: body
//	    name: network_peer_group
//	    description: Network peer group configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkPeerGroupPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkPeerGroupPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the network peer group name.
	name, err := pathVar(r, "name")
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing network peer group.
	var info *api.NetworkPeerGroup
	var networkPeerGroupID int64

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbRecord, err := dbCluster.GetNetworkPeerGroup(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		networkPeerGroupID = int64(dbRecord.ID)

		info, err = dbRecord.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		memberships, err := tx.GetNetworkPeerGroupNetworkMemberships(ctx, name)
		if err != nil {
			return err
		}

		info.Networks = networkPeerGroupMembershipsToAPI(memberships)

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	err = localUtil.EtagCheck(r, info.Writable())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Decode the request.
	req := api.NetworkPeerGroupPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Update the description.
	if info.Description != req.Description {
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateNetworkPeerGroupDescription(ctx, name, req.Description)
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Reconcile the member network list.
	err = networkPeerGroupSetNetworks(r.Context(), s, networkPeerGroupID, name, req.Networks)
	if err != nil {
		return response.SmartError(err)
	}

	// Emit the lifecycle event.
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.NetworkPeerGroupUpdated.Event(name, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/network-peer-groups/{name} network-peer-groups network_peer_group_delete
//
//	Delete the network peer group
//
//	Detaches any remaining member networks (reversing what
//	PUT /1.0/network-peer-groups/{name} set up for them), then removes the
//	network peer group along with its underlying OVN Logical_Switch and Logical_Router.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: path
//	    name: name
//	    description: Network peer group name
//	    type: string
//	    required: true
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkPeerGroupDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the network peer group name.
	name, err := pathVar(r, "name")
	if err != nil {
		return response.SmartError(err)
	}

	ovnnb, _, err := s.OVN()
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get OVN client: %w", err))
	}

	// Get the network peer group ID (needed to name its OVN resources).
	var networkPeerGroupID int64

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		networkPeerGroupID, err = dbCluster.GetNetworkPeerGroupID(ctx, tx.Tx(), name)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Detach all remaining member networks.
	err = networkPeerGroupSetNetworks(r.Context(), s, networkPeerGroupID, name, nil)
	if err != nil {
		return response.SmartError(err)
	}

	// Delete the DB record.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return dbCluster.DeleteNetworkPeerGroup(ctx, tx.Tx(), name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Delete the OVN logical router.
	err = ovnnb.DeleteLogicalRouter(r.Context(), networkPeerGroupLogicalRouterName(networkPeerGroupID))
	if err != nil {
		logger.Error("Failed to delete OVN logical router for network peer group", logger.Ctx{"name": name, "error": err})
	}

	// Delete the OVN logical switch.
	err = ovnnb.DeleteLogicalSwitch(r.Context(), networkPeerGroupLogicalSwitchName(networkPeerGroupID))
	if err != nil && !errors.Is(err, networkOVN.ErrNotFound) {
		logger.Error("Failed to delete OVN logical switch for network peer group", logger.Ctx{"name": name, "error": err})
	}

	// Delete the network peer group from the auth backend.
	err = s.Authorizer.DeleteNetworkPeerGroup(r.Context(), name)
	if err != nil {
		logger.Error("Failed to remove network peer group from authorizer", logger.Ctx{"name": name, "error": err})
	}

	// Emit the lifecycle event.
	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.NetworkPeerGroupDeleted.Event(name, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}
