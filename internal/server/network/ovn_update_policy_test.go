//go:build linux && cgo && !agent

package network

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/cluster"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

func newOVNUpdatePolicyTestNetwork(t *testing.T) *ovn {
	t.Helper()
	n := &ovn{common: common{
		state:   &state.State{DB: &db.DB{Cluster: &db.Cluster{}}},
		id:      -1660001,
		project: "update-policy-test",
		name:    t.Name(),
		status:  api.NetworkStatusCreated,
		nodes:   map[int64]db.NetworkNode{0: {ID: 0, State: 1}},
	}}
	require.Equal(t, api.NetworkStatusCreated, db.NetworkStateToAPIStatus(n.nodes[0].State))
	ovnLocalStates.Lock()
	previous, hadPrevious := ovnLocalStates.members[n.id]
	ovnLocalStates.Unlock()
	pn := ProjectNetwork{ProjectName: n.project, NetworkName: n.name}
	unavailableNetworksMu.Lock()
	_, wasUnavailable := unavailableNetworks[pn]
	unavailableNetworksMu.Unlock()
	n.setLocalState(ovnLocalState{started: true})
	t.Cleanup(func() {
		ovnLocalStates.Lock()
		if hadPrevious {
			ovnLocalStates.members[n.id] = previous
		} else {
			delete(ovnLocalStates.members, n.id)
		}

		ovnLocalStates.Unlock()
		unavailableNetworksMu.Lock()
		if wasUnavailable {
			unavailableNetworks[pn] = struct{}{}
		} else {
			delete(unavailableNetworks, pn)
		}

		unavailableNetworksMu.Unlock()
	})

	return n
}

func TestOVNUpdateNotifierPolicyConfigChanges(t *testing.T) {
	for _, tc := range []struct {
		name           string
		oldConfig      map[string]string
		newConfig      map[string]string
		description    string
		changedKeys    []string
		globalPending  bool
		localPending   bool
		localUnstarted bool
		unavailable    bool
		want           cluster.NotifierPolicy
	}{
		{name: "metadata-added", newConfig: map[string]string{"user.cleanup": "probe"}, want: cluster.NotifyAlive},
		{name: "metadata-removed", oldConfig: map[string]string{"user.cleanup": "probe"}, want: cluster.NotifyAlive},
		{name: "metadata-changed", oldConfig: map[string]string{"user.cleanup": "old"}, newConfig: map[string]string{"user.cleanup": "probe"}, want: cluster.NotifyAlive},
		{name: "description-only", description: "updated description", want: cluster.NotifyAlive},
		{name: "shared-key", newConfig: map[string]string{"dns.domain": "example.test"}, changedKeys: []string{"dns.domain"}, want: cluster.NotifyAlive},
		{name: "shared-key-and-metadata", newConfig: map[string]string{"dns.domain": "example.test", "user.cleanup": "probe"}, changedKeys: []string{"dns.domain"}, want: cluster.NotifyAlive},
		{name: "shared-key-and-member-key", newConfig: map[string]string{"dns.domain": "example.test", "bridge.mtu": "1400"}, changedKeys: []string{"dns.domain", "bridge.mtu"}, want: cluster.NotifyAll},
		{name: "custom-tunnel", oldConfig: map[string]string{"tunnel.test.protocol": "geneve"}, newConfig: map[string]string{"tunnel.test.protocol": "geneve", "dns.domain": "example.test"}, changedKeys: []string{"dns.domain"}, want: cluster.NotifyAll},
		{name: "globally-pending", newConfig: map[string]string{"dns.domain": "example.test"}, changedKeys: []string{"dns.domain"}, globalPending: true, want: cluster.NotifyAll},
		{name: "locally-pending", newConfig: map[string]string{"dns.domain": "example.test"}, changedKeys: []string{"dns.domain"}, localPending: true, want: cluster.NotifyAll},
		{name: "locally-unstarted", newConfig: map[string]string{"dns.domain": "example.test"}, changedKeys: []string{"dns.domain"}, localUnstarted: true, want: cluster.NotifyAll},
		{name: "locally-unavailable", newConfig: map[string]string{"dns.domain": "example.test"}, changedKeys: []string{"dns.domain"}, unavailable: true, want: cluster.NotifyAll},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newOVNUpdatePolicyTestNetwork(t)
			n.config = tc.oldConfig
			if tc.globalPending {
				n.status = api.NetworkStatusPending
			}

			if tc.localPending {
				n.nodes[0] = db.NetworkNode{ID: 0, State: 0}
			}

			if tc.localUnstarted {
				n.setLocalState(ovnLocalState{})
			}

			if tc.unavailable {
				n.setUnavailable()
			}

			dbUpdateNeeded, changedKeys, _, err := n.configChanged(api.NetworkPut{Description: tc.description, Config: tc.newConfig})
			require.NoError(t, err)
			require.True(t, dbUpdateNeeded)
			require.ElementsMatch(t, tc.changedKeys, changedKeys)
			require.Equal(t, tc.want, n.updateNotifierPolicy(changedKeys))
		})
	}
}

func TestOVNUpdateNotifierMetadataSelection(t *testing.T) {
	for _, tc := range []struct {
		name          string
		state         db.NetworkState
		status        string
		nodeID        int64
		missing       bool
		nilNodes      bool
		emptyNodes    bool
		noMembers     bool
		address       string
		memberState   int
		enabled       bool
		stalePending  bool
		newConfig     map[string]string
		description   string
		customTunnel  bool
		globalPending bool
		want          cluster.NotifierPolicy
		selected      []int64
	}{
		{name: "created", state: 1, status: api.NetworkStatusCreated, want: cluster.NotifyAlive, selected: []int64{0, 1, 2}},
		{name: "pending", state: 0, status: api.NetworkStatusPending, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "errored", state: 2, status: api.NetworkStatusErrored, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "starting", state: 3, status: api.NetworkStatusStarting, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "deleting", state: 4, status: api.NetworkStatusDeleting, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "preparing", state: 5, status: api.NetworkStatusPreparing, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "prepared", state: 6, status: api.NetworkStatusPrepared, want: cluster.NotifyAlive, selected: []int64{0, 2}},
		{name: "stopped", state: 7, status: api.NetworkStatusStopped, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "unknown", state: -1, status: api.NetworkStatusUnknown, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "missing-row", missing: true, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "nil-rows", nilNodes: true, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "empty-rows", emptyNodes: true, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "inconsistent-id", state: 1, status: api.NetworkStatusCreated, nodeID: 99, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "untouched-maintenance", state: 0, status: api.NetworkStatusPending, memberState: db.ClusterMemberStateEvacuated, enabled: true, want: cluster.NotifyAlive, selected: []int64{0, 2}},
		{name: "disabled-maintenance-exclusion", state: 0, status: api.NetworkStatusPending, memberState: db.ClusterMemberStateEvacuated, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "active-pending-not-excluded", state: 0, status: api.NetworkStatusPending, enabled: true, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "maintenance-missing-not-excluded", missing: true, memberState: db.ClusterMemberStateEvacuated, enabled: true, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "origin-address", missing: true, address: "origin", want: cluster.NotifyAlive, selected: []int64{0, 1, 2}},
		{name: "placeholder-address", missing: true, address: "0.0.0.0", want: cluster.NotifyAlive, selected: []int64{0, 1, 2}},
		{name: "no-selected-members", nilNodes: true, noMembers: true, want: cluster.NotifyAlive},
		{name: "stale-pending-fresh-created", stalePending: true, state: 1, status: api.NetworkStatusCreated, want: cluster.NotifyAlive, selected: []int64{0, 1, 2}},
		{name: "description-created", description: "updated description", state: 1, status: api.NetworkStatusCreated, want: cluster.NotifyAlive, selected: []int64{0, 1, 2}},
		{name: "description-pending", description: "updated description", state: 0, status: api.NetworkStatusPending, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "shared-and-metadata-pending", state: 0, status: api.NetworkStatusPending, newConfig: map[string]string{"dns.domain": "example.test", "user.cleanup": "probe"}, want: cluster.NotifyAlive, selected: []int64{0, 1, 2}},
		{name: "member-key-and-metadata-created", state: 1, status: api.NetworkStatusCreated, newConfig: map[string]string{"bridge.mtu": "1400", "user.cleanup": "probe"}, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "custom-tunnel-metadata-created", state: 1, status: api.NetworkStatusCreated, customTunnel: true, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
		{name: "global-pending-metadata-created", state: 1, status: api.NetworkStatusCreated, globalPending: true, want: cluster.NotifyAll, selected: []int64{0, 1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newOVNUpdatePolicyTestNetwork(t)
			n.nodes[1] = db.NetworkNode{ID: 1, State: 1}
			if tc.stalePending {
				n.nodes[1] = db.NetworkNode{ID: 1, State: 0}
			}

			newConfig := tc.newConfig
			if newConfig == nil {
				newConfig = map[string]string{"user.cleanup": "probe"}
			}

			if tc.description != "" {
				newConfig = map[string]string{}
			}

			if tc.customTunnel {
				n.config = map[string]string{"tunnel.test.protocol": "geneve"}
				newConfig["tunnel.test.protocol"] = "geneve"
			}

			if tc.globalPending {
				n.status = api.NetworkStatusPending
			}

			needed, changedKeys, _, err := n.configChanged(api.NetworkPut{Config: newConfig, Description: tc.description})
			require.NoError(t, err)
			require.True(t, needed)
			policy := n.updateNotifierPolicy(changedKeys)
			nodeID := tc.nodeID
			if nodeID == 0 {
				nodeID = 1
			}

			nodes := map[int64]db.NetworkNode{
				1: {ID: nodeID, State: tc.state},
				2: {ID: 2, State: 1},
			}

			if tc.missing {
				delete(nodes, 1)
			} else if tc.status != "" {
				require.Equal(t, tc.status, db.NetworkStateToAPIStatus(nodes[1].State))
			}

			if tc.nilNodes {
				nodes = nil
			} else if tc.emptyNodes {
				nodes = map[int64]db.NetworkNode{}
			}

			address := tc.address
			if address == "" {
				address = "peer"
			}

			members := []db.NodeInfo{
				{ID: 0, Address: "origin"},
				{ID: 1, Address: address, State: tc.memberState},
				{ID: 2, Address: "other-peer"},
			}

			if tc.noMembers {
				members = nil
			}

			selected, defaultPolicy := ovnNotifierSelection(policy, false, nodes, members, tc.enabled, "origin")
			require.Equal(t, tc.selected, selected)
			require.Equal(t, policy, defaultPolicy)
			selected, actual := ovnNotifierSelection(policy, len(changedKeys) == 0, nodes, members, tc.enabled, "origin")
			require.Equal(t, tc.selected, selected)
			require.Equal(t, tc.want, actual)
		})
	}
}
