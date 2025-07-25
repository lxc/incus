package ip

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Route represents arguments for route manipulation.
type Route struct {
	DevName string
	Route   *net.IPNet
	Table   string
	Src     net.IP
	Proto   string
	Family  Family
	Via     net.IP
	VRF     string
	Scope   string
}

func (r *Route) netlinkRoute() (*netlink.Route, error) {
	link, err := linkByName(r.DevName)
	if err != nil {
		return nil, err
	}

	route := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Family:    int(r.Family),
		Dst:       r.Route,
		Src:       r.Src,
		Gw:        r.Via,
	}

	if r.Table != "" {
		tableID, err := r.tableID()
		if err != nil {
			return nil, fmt.Errorf("Invalid table %q: %w", r.Table, err)
		}

		route.Table = tableID
	} else if r.VRF != "" {
		vrfDev, err := linkByName(r.VRF)
		if err != nil {
			return nil, err
		}

		vrf, ok := vrfDev.(*netlink.Vrf)
		if !ok {
			return nil, fmt.Errorf("%q is not a vrf", r.VRF)
		}

		route.Table = int(vrf.Table)
	}

	route.Scope, err = r.netlinkScope()
	if err != nil {
		return nil, err
	}

	if r.Via == nil {
		route.Scope = netlink.SCOPE_LINK
	}

	if r.Proto != "" {
		proto, err := r.netlinkProto()
		if err != nil {
			return nil, err
		}

		route.Protocol = proto
	}

	return route, nil
}

func (r *Route) tableID() (int, error) {
	switch r.Table {
	case "default":
		return unix.RT_TABLE_DEFAULT, nil
	case "main":
		return unix.RT_TABLE_MAIN, nil
	case "local":
		return unix.RT_TABLE_LOCAL, nil
	default:
		return strconv.Atoi(r.Table)
	}
}

func (r *Route) netlinkScope() (netlink.Scope, error) {
	switch r.Scope {
	case "nowhere":
		return netlink.SCOPE_NOWHERE, nil
	case "host":
		return netlink.SCOPE_HOST, nil
	case "link":
		return netlink.SCOPE_LINK, nil
	case "universe":
		return netlink.SCOPE_UNIVERSE, nil
	case "":
		if r.Via == nil {
			return netlink.SCOPE_LINK, nil
		}

		return netlink.SCOPE_UNIVERSE, nil
	default:
		return 0, fmt.Errorf("Invalid scope %q", r.Scope)
	}
}

func (r *Route) netlinkProto() (netlink.RouteProtocol, error) {
	switch r.Proto {
	case "babel":
		return unix.RTPROT_BABEL, nil
	case "bgp":
		return unix.RTPROT_BGP, nil
	case "bird":
		return unix.RTPROT_BIRD, nil
	case "boot":
		return unix.RTPROT_BOOT, nil
	case "dhcp":
		return unix.RTPROT_DHCP, nil
	case "dnrouted":
		return unix.RTPROT_DNROUTED, nil
	case "eigrp":
		return unix.RTPROT_EIGRP, nil
	case "gated":
		return unix.RTPROT_GATED, nil
	case "isis":
		return unix.RTPROT_ISIS, nil
	case "keepalived":
		return unix.RTPROT_KEEPALIVED, nil
	case "kernel":
		return unix.RTPROT_KERNEL, nil
	case "mrouted":
		return unix.RTPROT_MROUTED, nil
	case "mrt":
		return unix.RTPROT_MRT, nil
	case "ntk":
		return unix.RTPROT_NTK, nil
	case "ospf":
		return unix.RTPROT_OSPF, nil
	case "ra":
		return unix.RTPROT_RA, nil
	case "redirect":
		return unix.RTPROT_REDIRECT, nil
	case "rip":
		return unix.RTPROT_RIP, nil
	case "static":
		return unix.RTPROT_STATIC, nil
	case "unspec":
		return unix.RTPROT_UNSPEC, nil
	case "xorp":
		return unix.RTPROT_XORP, nil
	case "zebra":
		return unix.RTPROT_ZEBRA, nil
	default:
		proto, err := strconv.Atoi(r.Proto)
		if err != nil {
			return 0, err
		}

		return netlink.RouteProtocol(proto), nil
	}
}

// Add adds new route.
func (r *Route) Add() error {
	route, err := r.netlinkRoute()
	if err != nil {
		return err
	}

	err = netlink.RouteAdd(route)
	if err != nil {
		return fmt.Errorf("Failed to add route %+v: %w", route, err)
	}

	return nil
}

// Delete deletes routing table.
func (r *Route) Delete() error {
	route, err := r.netlinkRoute()
	if err != nil {
		return err
	}

	err = netlink.RouteDel(route)
	if err != nil {
		return fmt.Errorf("Failed to delete route %+v: %w", route, err)
	}

	return nil
}

func routeFilterMask(route *netlink.Route) uint64 {
	var filterMask uint64

	// we always filter by interface because that is required to be set on our route type
	filterMask |= netlink.RT_FILTER_OIF

	if route.Dst != nil {
		filterMask |= netlink.RT_FILTER_DST
	}

	if route.Gw != nil {
		filterMask |= netlink.RT_FILTER_GW
	}

	if route.Protocol != 0 {
		filterMask |= netlink.RT_FILTER_PROTOCOL
	}

	if route.Table != 0 {
		filterMask |= netlink.RT_FILTER_TABLE
	}

	return filterMask
}

// Flush flushes routing tables.
func (r *Route) Flush() error {
	route, err := r.netlinkRoute()
	if err != nil {
		return err
	}

	var iterErr error

	err = netlink.RouteListFilteredIter(route.Family, route, routeFilterMask(route), func(route netlink.Route) (cont bool) {
		iterErr = netlink.RouteDel(&route)
		// Ignore missing routes.
		if errors.Is(iterErr, syscall.ESRCH) {
			iterErr = nil

			return true
		}

		return iterErr == nil
	})
	if err != nil {
		return fmt.Errorf("Failed to flush routes matching %+v: %w", route, err)
	}

	if iterErr != nil {
		return fmt.Errorf("Failed to flush routes matching %+v: %w", route, iterErr)
	}

	return nil
}

// Replace changes or adds new route.
// If there is already a route with the same destination, metric, tos and table then that route is updated,
// otherwise a new route is added.
func (r *Route) Replace() error {
	route, err := r.netlinkRoute()
	if err != nil {
		return err
	}

	err = netlink.RouteReplace(route)
	if err != nil {
		return fmt.Errorf("Failed to replace route %s: %w", route, err)
	}

	return nil
}

// List lists matching routes.
func (r *Route) List() ([]Route, error) {
	route, err := r.netlinkRoute()
	if err != nil {
		return nil, err
	}

	netlinkRoutes, err := netlink.RouteListFiltered(route.Family, route, routeFilterMask(route))
	if err != nil {
		return nil, fmt.Errorf("Failed to list routes matching %+v: %w", route, err)
	}

	routes := make([]Route, 0, len(netlinkRoutes))

	for _, netlinkRoute := range netlinkRoutes {
		var table string

		switch netlinkRoute.Table {
		case unix.RT_TABLE_MAIN:
			table = "main"
		case unix.RT_TABLE_LOCAL:
			table = "local"
		case unix.RT_TABLE_DEFAULT:
			table = "default"
		default:
			table = strconv.Itoa(netlinkRoute.Table)
		}

		routes = append(routes, Route{
			DevName: r.DevName, // routes are always filtered by device so we can use the device name that was passed in
			Route:   netlinkRoute.Dst,
			Src:     netlinkRoute.Src,
			Via:     netlinkRoute.Gw,
			Scope:   netlinkRoute.Scope.String(),
			Table:   table,
			VRF:     "", // adding a route to a VRF just adds it to the table associated with the VRF, so when retrieving routes that information is not available anymore and we just set the table
			Proto:   netlinkRoute.Protocol.String(),
			Family:  Family(netlinkRoute.Family),
		})
	}

	return routes, nil
}
