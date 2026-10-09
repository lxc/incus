package network

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
)

type nicStopRoutesFixture struct {
	t           *testing.T
	events      []string
	captureFail networkOVN.OVNRouter
	applyFail   networkOVN.OVNRouter
}

func (f *nicStopRoutesFixture) CaptureNICRouteCleanup(ctx context.Context, router networkOVN.OVNRouter, routes ...networkOVN.OVNRouterRoute) (networkOVN.NICRouteCleanup, error) {
	{
		err := ctx.Err()
		if err != nil {
			return networkOVN.NICRouteCleanup{}, err
		}
	}

	f.events = append(f.events, "capture:"+string(router))
	require.NotEmpty(f.t, routes)
	if f.captureFail == router {
		return networkOVN.NICRouteCleanup{}, errors.New("capture failed")
	}

	return networkOVN.NICRouteCleanup{RouterName: router}, nil
}

func (f *nicStopRoutesFixture) ApplyNICRouteCleanup(ctx context.Context, plan networkOVN.NICRouteCleanup) error {
	{
		err := ctx.Err()
		if err != nil {
			return err
		}
	}

	f.events = append(f.events, "apply:"+string(plan.RouterName))
	if f.applyFail == plan.RouterName {
		return errors.New("apply failed")
	}

	return nil
}

func nicStopRouteTestCaptures(t *testing.T) []ovnNICStopRouteCapture {
	t.Helper()
	_, prefix, err := net.ParseCIDR("192.0.2.0/24")
	require.NoError(t, err)
	captures, err := nicStopRouteCaptures([]net.IPNet{*prefix}, ovnNICStopRouteTarget{router: "local", port: "local-port", nextHop4: net.ParseIP("192.0.2.2")}, []ovnNICStopRouteTarget{{router: "peer", port: "peer-port", nextHop4: net.ParseIP("198.51.100.1")}})
	require.NoError(t, err)
	return captures
}

func TestNICStopCapturedRouteBoundary(t *testing.T) {
	cases := []struct {
		name        string
		captureFail networkOVN.OVNRouter
		applyFail   networkOVN.OVNRouter
		publishFail bool
		earlyFail   bool
		lateFail    bool
		canceled    bool
		wantErr     bool
		want        []string
	}{
		{name: "success", want: []string{"capture:local", "capture:peer", "publish", "before", "apply:local", "apply:peer", "after"}},
		{name: "local-capture-failure", captureFail: "local", wantErr: true, want: []string{"capture:local"}},
		{name: "peer-capture-failure", captureFail: "peer", wantErr: true, want: []string{"capture:local", "capture:peer"}},
		{name: "publication-failure", publishFail: true, wantErr: true, want: []string{"capture:local", "capture:peer", "publish"}},
		{name: "early-cleanup-failure", earlyFail: true, wantErr: true, want: []string{"capture:local", "capture:peer", "publish", "before"}},
		{name: "local-route-failure", applyFail: "local", wantErr: true, want: []string{"capture:local", "capture:peer", "publish", "before", "apply:local"}},
		{name: "peer-route-failure", applyFail: "peer", wantErr: true, want: []string{"capture:local", "capture:peer", "publish", "before", "apply:local", "apply:peer"}},
		{name: "late-cleanup-failure", lateFail: true, wantErr: true, want: []string{"capture:local", "capture:peer", "publish", "before", "apply:local", "apply:peer", "after"}},
		{name: "canceled-before-capture", canceled: true, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &nicStopRoutesFixture{t: t, captureFail: tc.captureFail, applyFail: tc.applyFail}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}

			err := stopNICCapturedRoutes(ctx, f, nicStopRouteTestCaptures(t), func(plans []networkOVN.NICRouteCleanup) error {
				f.events = append(f.events, "publish")
				require.Len(t, plans, 2)
				if tc.publishFail {
					return errors.New("publication failed")
				}

				return nil
			}, func() error {
				f.events = append(f.events, "before")
				if tc.earlyFail {
					return errors.New("early cleanup failed")
				}

				return nil
			}, func() error {
				f.events = append(f.events, "after")
				if tc.lateFail {
					return errors.New("late cleanup failed")
				}

				return nil
			})
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tc.want, f.events)
		})
	}
}

func TestNICStopOriginalRouteInputs(t *testing.T) {
	_, v4, err := net.ParseCIDR("192.0.2.0/24")
	require.NoError(t, err)
	_, v6, err := net.ParseCIDR("2001:db8::/64")
	require.NoError(t, err)
	local := ovnNICStopRouteTarget{router: "local", port: "local-port", nextHop4: net.ParseIP("192.0.2.2"), nextHop6: net.ParseIP("2001:db8::2")}
	t.Run("both-families-and-peer-output-port", func(t *testing.T) {
		peer := ovnNICStopRouteTarget{router: "peer", port: "peer-port", nextHop4: net.ParseIP("198.51.100.1"), nextHop6: net.ParseIP("2001:db8:1::1")}
		captures, err := nicStopRouteCaptures([]net.IPNet{*v4, *v6}, local, []ovnNICStopRouteTarget{peer})
		require.NoError(t, err)
		require.Len(t, captures, 2)
		require.Equal(t, local.nextHop4, captures[0].routes[0].NextHop)
		require.Equal(t, local.nextHop6, captures[0].routes[1].NextHop)
		require.Equal(t, local.port, captures[0].routes[0].Port)
		require.Equal(t, peer.nextHop4, captures[1].routes[0].NextHop)
		require.Equal(t, peer.nextHop6, captures[1].routes[1].NextHop)
		require.Equal(t, peer.port, captures[1].routes[1].Port)
		original := captures[0].routes[0].NextHop.String()
		local.nextHop4[0] ^= 1
		require.Equal(t, original, captures[0].routes[0].NextHop.String())
		local.nextHop4[0] ^= 1
	})
	t.Run("peer-missing-family-was-not-installed", func(t *testing.T) {
		peer := ovnNICStopRouteTarget{router: "peer", port: "peer-port", nextHop4: net.ParseIP("198.51.100.1")}
		captures, err := nicStopRouteCaptures([]net.IPNet{*v4, *v6}, local, []ovnNICStopRouteTarget{peer})
		require.NoError(t, err)
		require.Len(t, captures[1].routes, 1)
		require.Equal(t, *v4, captures[1].routes[0].Prefix)
	})
	t.Run("local-missing-hop-refuses-prefix-only-delete", func(t *testing.T) {
		absent := local
		absent.nextHop6 = nil
		_, err := nicStopRouteCaptures([]net.IPNet{*v4, *v6}, absent, nil)
		require.Error(t, err)
	})
	t.Run("missing-output-port-refuses", func(t *testing.T) {
		absent := local
		absent.port = ""
		_, err := nicStopRouteCaptures([]net.IPNet{*v4}, absent, nil)
		require.Error(t, err)
	})
	t.Run("no-routes-needs-no-route-target", func(t *testing.T) {
		captures, err := nicStopRouteCaptures(nil, ovnNICStopRouteTarget{}, nil)
		require.NoError(t, err)
		require.Empty(t, captures)
	})
}

func TestNICStopCapturedRoutesNoRouteEffects(t *testing.T) {
	var events []string
	require.NoError(t, stopNICCapturedRoutes(context.Background(), nil, nil, func(plans []networkOVN.NICRouteCleanup) error {
		require.Empty(t, plans)
		events = append(events, "publish")
		return nil
	}, func() error { events = append(events, "before"); return nil }, func() error { events = append(events, "after"); return nil }))
	require.Equal(t, []string{"publish", "before", "after"}, events)
}

func TestNICStopCapturedRoutesCanceledWithoutRoutes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := stopNICCapturedRoutes(ctx, nil, nil, func([]networkOVN.NICRouteCleanup) error { calls++; return nil }, func() error {
		calls++
		return nil
	}, func() error {
		calls++
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls)
}

func TestNICStopInheritedRouteReservations(t *testing.T) {
	newNetwork := func() *ovn {
		n := &ovn{}
		n.id = 11
		AuthorizeOVNInitialization(n, "source-token")
		return n
	}

	t.Run("enclosing-peer-token-is-inherited-and-unrelated-excluded", func(t *testing.T) {
		n := newNetwork()
		parent := map[int64]string{11: "source-token", 12: "peer-token", 90: "unrelated-token"}
		require.NoError(t, AuthorizeOVNNICCleanupReservations(n, parent))
		parent[12] = "changed-by-caller"
		inherited, err := n.nicStopInheritedRouteTokens([]int64{11, 12, 13})
		require.NoError(t, err)
		require.Equal(t, map[int64]string{11: "source-token", 12: "peer-token"}, inherited)
	})
	t.Run("foreign-source-token-refused", func(t *testing.T) {
		n := newNetwork()
		require.Error(t, AuthorizeOVNNICCleanupReservations(n, map[int64]string{11: "foreign", 12: "peer-token"}))
		require.Nil(t, n.ovnNICCleanupTokens)
	})
	t.Run("ended-source-capability-refused", func(t *testing.T) {
		n := newNetwork()
		require.NoError(t, AuthorizeOVNNICCleanupReservations(n, map[int64]string{11: "source-token", 12: "peer-token"}))
		n.ovnOperationToken = "new-source-token"
		_, err := n.nicStopInheritedRouteTokens([]int64{11, 12})
		require.Error(t, err)
	})
	t.Run("ordinary-single-network-source", func(t *testing.T) {
		n := newNetwork()
		inherited, err := n.nicStopInheritedRouteTokens([]int64{11, 12})
		require.NoError(t, err)
		require.Equal(t, map[int64]string{11: "source-token"}, inherited)
	})
}

func TestNICStopMissingPublicationRefusesEffects(t *testing.T) {
	calls := 0
	err := stopNICCapturedRoutes(context.Background(), nil, nil, nil, func() error { calls++; return nil }, func() error { calls++; return nil })
	require.ErrorContains(t, err, "publication is required")
	require.Zero(t, calls)
}

func TestNICStopStoredRoutesDoNotRecapture(t *testing.T) {
	f := &nicStopRoutesFixture{t: t, captureFail: "replacement"}
	plans := []networkOVN.NICRouteCleanup{{RouterName: "original"}}
	err := applyNICCapturedRoutes(context.Background(), f, plans, func() error { f.events = append(f.events, "before"); return nil }, func() error { f.events = append(f.events, "after"); return nil })
	require.NoError(t, err)
	require.Equal(t, []string{"before", "apply:original", "after"}, f.events)
}

func TestNICStopCaptureOnlyBoundary(t *testing.T) {
	cases := []struct {
		name           string
		captureFail    networkOVN.OVNRouter
		publishFail    bool
		canceled       bool
		missingPublish bool
		wantErr        bool
		want           []string
	}{
		{name: "success", want: []string{"capture:local", "capture:peer", "publish"}},
		{name: "local-refusal", captureFail: "local", wantErr: true, want: []string{"capture:local"}},
		{name: "peer-refusal", captureFail: "peer", wantErr: true, want: []string{"capture:local", "capture:peer"}},
		{name: "publication-refusal", publishFail: true, wantErr: true, want: []string{"capture:local", "capture:peer", "publish"}},
		{name: "required-publication", missingPublish: true, wantErr: true, want: []string{"capture:local", "capture:peer"}},
		{name: "canceled", canceled: true, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &nicStopRoutesFixture{t: t, captureFail: tc.captureFail, applyFail: "local"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}

			var publish func([]networkOVN.NICRouteCleanup) error
			if !tc.missingPublish {
				publish = func(plans []networkOVN.NICRouteCleanup) error {
					f.events = append(f.events, "publish")
					require.Len(t, plans, 2)
					if tc.publishFail {
						return errors.New("publication failure")
					}

					return nil
				}
			}

			plans, err := captureNICStopRoutes(ctx, f, nicStopRouteTestCaptures(t), publish)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, plans)
			} else {
				require.NoError(t, err)
				require.Len(t, plans, 2)
			}

			require.Equal(t, tc.want, f.events)
		})
	}

	t.Run("retry-captures-and-publishes-without-apply", func(t *testing.T) {
		f := &nicStopRoutesFixture{t: t, captureFail: "peer"}
		publish := func(plans []networkOVN.NICRouteCleanup) error { f.events = append(f.events, "publish"); return nil }
		_, err := captureNICStopRoutes(context.Background(), f, nicStopRouteTestCaptures(t), publish)
		require.Error(t, err)
		f.captureFail = ""
		plans, err := captureNICStopRoutes(context.Background(), f, nicStopRouteTestCaptures(t), publish)
		require.NoError(t, err)
		require.Len(t, plans, 2)
		require.Equal(t, []string{"capture:local", "capture:peer", "capture:local", "capture:peer", "publish"}, f.events)
	})
	t.Run("no-routes-still-requires-publication", func(t *testing.T) {
		published := 0
		plans, err := captureNICStopRoutes(context.Background(), nil, nil, func(plans []networkOVN.NICRouteCleanup) error {
			published++
			require.Empty(t, plans)
			return nil
		})
		require.NoError(t, err)
		require.Empty(t, plans)
		require.Equal(t, 1, published)
	})
}
