package network

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/lxc/incus/v7/internal/server/db"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/shared/api"
)

// These targets describe the actual pre-Stop route inputs, not a prefix query
// reconstructed after the port or instance configuration has changed.
type ovnNICStopRouteTarget struct {
	router   networkOVN.OVNRouter
	port     networkOVN.OVNRouterPort
	nextHop4 net.IP
	nextHop6 net.IP
}

type ovnNICStopRouteCapture struct {
	router networkOVN.OVNRouter
	routes []networkOVN.OVNRouterRoute
}

type ovnNICRouteCleanupBackend interface {
	CaptureNICRouteCleanup(context.Context, networkOVN.OVNRouter, ...networkOVN.OVNRouterRoute) (networkOVN.NICRouteCleanup, error)
	ApplyNICRouteCleanup(context.Context, networkOVN.NICRouteCleanup) error
}

// nicStopRouteCaptures uses the same family/next-hop/output-port rules as
// InstanceDevicePortStart. An unavailable peer family was never installed.
// A missing local next-hop is an error, rather than permission to delete by prefix.
func nicStopRouteCaptures(prefixes []net.IPNet, local ovnNICStopRouteTarget, peers []ovnNICStopRouteTarget) ([]ovnNICStopRouteCapture, error) {
	if len(prefixes) == 0 {
		return nil, nil
	}

	targets := append([]ovnNICStopRouteTarget{local}, peers...)
	captures := make([]ovnNICStopRouteCapture, 0, len(targets))
	for i, target := range targets {
		if target.router == "" || target.port == "" {
			return nil, errors.New("Original NIC route router/output-port identity is missing")
		}

		capture := ovnNICStopRouteCapture{router: target.router}
		for _, prefix := range prefixes {
			hop := target.nextHop4
			if prefix.IP.To4() == nil {
				hop = target.nextHop6
			}

			if hop == nil {
				if i == 0 {
					return nil, fmt.Errorf("Original NIC route next-hop for %q is missing", prefix.String())
				}

				continue
			}

			capture.routes = append(capture.routes, networkOVN.OVNRouterRoute{Prefix: prefix, NextHop: slices.Clone(hop), Port: target.port})
		}

		if len(capture.routes) > 0 {
			captures = append(captures, capture)
		}
	}

	return captures, nil
}

// stopNICCapturedRoutes is the production Stop continuation boundary:
// capture every original target before the first mutating callback, apply
// only those row identities, and never enter later effects after an error.
// Its returned plans are not a complete durable NIC acknowledgment.
func stopNICCapturedRoutes(ctx context.Context, backend ovnNICRouteCleanupBackend, captures []ovnNICStopRouteCapture, publish func([]networkOVN.NICRouteCleanup) error, beforeRoutes func() error, afterRoutes func() error) error {
	plans, err := captureNICStopRoutes(ctx, backend, captures, publish)
	if err != nil {
		return err
	}

	return applyNICCapturedRoutes(ctx, backend, plans, beforeRoutes, afterRoutes)
}

// captureNICStopRoutes publishes the original route plans without entering any
// cleanup continuation. Source capture before host detach uses this same path.
func captureNICStopRoutes(ctx context.Context, backend ovnNICRouteCleanupBackend, captures []ovnNICStopRouteCapture, publish func([]networkOVN.NICRouteCleanup) error) ([]networkOVN.NICRouteCleanup, error) {
	{
		err := ctx.Err()
		if err != nil {
			return nil, err
		}
	}

	plans := make([]networkOVN.NICRouteCleanup, 0, len(captures))
	for _, capture := range captures {
		plan, err := backend.CaptureNICRouteCleanup(ctx, capture.router, capture.routes...)
		if err != nil {
			return nil, fmt.Errorf("Failed capturing original NIC routes on %q: %w", capture.router, err)
		}

		plans = append(plans, plan)
	}

	{
		err := ctx.Err()
		if err != nil {
			return nil, err
		}
	}

	if publish == nil {
		return nil, errors.New("Original NIC cleanup publication is required")
	}

	err := publish(plans)
	if err != nil {
		return nil, fmt.Errorf("Failed durably publishing original NIC cleanup: %w", err)
	}

	return plans, nil
}

func applyNICCapturedRoutes(ctx context.Context, backend ovnNICRouteCleanupBackend, plans []networkOVN.NICRouteCleanup, beforeRoutes func() error, afterRoutes func() error) error {
	{
		err := ctx.Err()
		if err != nil {
			return err
		}
	}

	err := beforeRoutes()
	if err != nil {
		return err
	}

	for _, plan := range plans {
		err = backend.ApplyNICRouteCleanup(ctx, plan)
		if err != nil {
			return fmt.Errorf("Failed applying original NIC routes on %q: %w", plan.RouterName, err)
		}
	}

	return afterRoutes()
}

// AuthorizeOVNNICCleanupReservations carries only the enclosing caller's exact
// capabilities into a freshly loaded device. Every use is still verified in SQL,
// and nested release never releases these tokens.
func AuthorizeOVNNICCleanupReservations(n Network, tokens map[int64]string) error {
	ovnNet, ok := n.(*ovn)
	if !ok || ovnNet.ovnOperationToken == "" || tokens[ovnNet.ID()] != ovnNet.ovnOperationToken {
		return errors.New("Original NIC cleanup caller capability is missing or stale")
	}

	for id, token := range tokens {
		if id <= 0 || token == "" {
			return errors.New("Invalid enclosing NIC cleanup capability")
		}
	}

	ovnNet.ovnNICCleanupTokens = maps.Clone(tokens)
	return nil
}

func (n *ovn) nicStopInheritedRouteTokens(ids []int64) (map[int64]string, error) {
	if n.ovnOperationToken == "" || !slices.Contains(ids, n.ID()) {
		return nil, errors.New("Original NIC cleanup source capability is missing")
	}

	if n.ovnNICCleanupTokens != nil && n.ovnNICCleanupTokens[n.ID()] != n.ovnOperationToken {
		return nil, errors.New("Original NIC cleanup enclosing capability has changed")
	}

	inherited := map[int64]string{n.ID(): n.ovnOperationToken}
	for _, id := range ids {
		{
			token, exists := n.ovnNICCleanupTokens[id]
			if exists {
				inherited[id] = token
			}
		}
	}

	return inherited, nil
}

// reserveNICStopRouteTargets extends the existing source NIC reservation to
// the router owner and peers. The second graph read is protected by these
// durable reservations; a graph change during acquisition refuses Stop.
func (n *ovn) reserveNICStopRouteTargets(ctx context.Context) (*ovn, []*ovn, []db.OVNNICCleanupReservation, func() error, error) {
	owner, err := n.routerOwner()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	var peers []*ovn
	err = owner.forPeers(func(peer *ovn) error {
		peers = append(peers, peer)
		return nil
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	ids := []int64{n.ID(), owner.ID()}
	for _, peer := range peers {
		ids = append(ids, peer.ID())
	}

	slices.Sort(ids)
	ids = slices.Compact(ids)
	inherited, err := n.nicStopInheritedRouteTokens(ids)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// Parallel stops on networks sharing a router owner or peers contend briefly; wait for them. The
	// wait is bounded because two stops on mutually peered networks each hold their own reservation.
	var reservations []db.OVNNICCleanupReservation
	deadline := time.Now().Add(10 * time.Second)
	for {
		err = n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			reservations, err = tx.AcquireOVNNICCleanupOperations(ctx, ids, inherited)
			return err
		})
		if err == nil || !api.StatusErrorCheck(err, http.StatusConflict) || time.Now().After(deadline) {
			break
		}

		select {
		case <-ctx.Done():
			return nil, nil, nil, nil, errors.Join(err, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}

	if err != nil {
		return nil, nil, nil, nil, err
	}

	release := func() error {
		return n.state.DB.Cluster.Transaction(context.WithoutCancel(ctx), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.ReleaseOVNNICCleanupOperations(ctx, reservations)
		})
	}

	fail := func(err error) (*ovn, []*ovn, []db.OVNNICCleanupReservation, func() error, error) {
		return nil, nil, nil, nil, errors.Join(err, release())
	}

	oldParentID := n.parentID
	err = n.reload()
	if err != nil {
		return fail(err)
	}

	if n.parentID != oldParentID {
		return fail(errors.New("Original NIC router owner changed during cleanup reservation"))
	}

	if owner.ID() != n.ID() {
		err = owner.reload()
		if err != nil {
			return fail(err)
		}
	} else {
		owner = n
	}

	var currentPeers []*ovn
	err = owner.forPeers(func(peer *ovn) error {
		currentPeers = append(currentPeers, peer)
		return nil
	})
	if err != nil {
		return fail(err)
	}

	currentIDs := []int64{n.ID(), owner.ID()}
	for _, peer := range currentPeers {
		currentIDs = append(currentIDs, peer.ID())
	}

	slices.Sort(currentIDs)
	currentIDs = slices.Compact(currentIDs)
	if !slices.Equal(ids, currentIDs) {
		return fail(errors.New("Original NIC route peer set changed during cleanup reservation"))
	}

	return owner, currentPeers, reservations, release, nil
}
