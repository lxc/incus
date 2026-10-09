package network

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/locking"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/util"
)

// LockOVNLifecycle rejects overlapping operations, including notifications, to avoid cross-member deadlocks.
func LockOVNLifecycle(projectName string, networkName string) (locking.UnlockFunc, error) {
	unlock, _ := locking.TryLock(fmt.Sprintf("network.ovn.lifecycle.%q.%q", projectName, networkName))
	if unlock == nil {
		return nil, api.StatusErrorf(http.StatusConflict, "OVN network %q has another lifecycle operation in progress", networkName)
	}

	return unlock, nil
}

type ovnLocalState struct {
	restoring bool
	started   bool
	deleting  bool
	config    map[string]string
}

var ovnLocalStates = struct {
	sync.Mutex
	members map[int64]ovnLocalState
}{members: make(map[int64]ovnLocalState)}

func (n *ovn) localState() ovnLocalState {
	ovnLocalStates.Lock()
	defer ovnLocalStates.Unlock()
	return ovnLocalStates.members[n.id]
}

func (n *ovn) setLocalState(value ovnLocalState) {
	ovnLocalStates.Lock()
	defer ovnLocalStates.Unlock()
	ovnLocalStates.members[n.id] = value
}

// OVNStartedLocally reports whether this member already started the network since the daemon
// started, for example through returning-member initialization.
func OVNStartedLocally(n Network) bool {
	driver, ok := n.(*ovn)
	return ok && driver.localState().started
}

// OVNClearCreateRollback forgets the local deletion state a rolled back creation left behind: the
// network itself was not deleted, so later initialization must not be refused as a deletion retry.
func OVNClearCreateRollback(n Network) {
	driver, ok := n.(*ovn)
	if !ok {
		return
	}

	ovnLocalStates.Lock()
	defer ovnLocalStates.Unlock()
	if ovnLocalStates.members[driver.id].deleting {
		delete(ovnLocalStates.members, driver.id)
	}
}

func (n *ovn) reload() error {
	fresh, err := LoadByName(n.state, n.project, n.name)
	if err != nil {
		return err
	}

	if fresh.ID() != n.id || fresh.Type() != "ovn" {
		return api.StatusErrorf(http.StatusConflict, "OVN network %q has been replaced", n.name)
	}

	ovnNet, ok := fresh.(*ovn)
	if !ok {
		return api.StatusErrorf(http.StatusConflict, "OVN network %q has been replaced", n.name)
	}

	guardedNB := n.ovnnb
	if guardedNB != nil && guardedNB.ReferenceMutationGuarded() && guardedNB.BackendID() != ovnNet.ovnnb.BackendID() {
		return errors.New("Shared reload original NB root changed")
	}

	token := n.ovnOperationToken
	n.common = ovnNet.common
	n.ovnOperationToken = token
	n.ovnnb = ovnNet.ovnnb
	if guardedNB != nil && guardedNB.ReferenceMutationGuarded() {
		n.ovnnb = guardedNB
	}

	n.ovnsb = ovnNet.ovnsb
	n.parentID = ovnNet.parentID
	n.parentUplink = ovnNet.parentUplink
	return nil
}

func (n *ovn) rawLocalStatus() string {
	node, exists := n.nodes[n.state.DB.Cluster.GetNodeID()]
	if !exists {
		return api.NetworkStatusUnknown
	}

	return db.NetworkStateToAPIStatus(node.State)
}

// ensureLocalStarted requires the lifecycle lock and initializes only globally created networks.
func (n *ovn) ensureLocalStarted() error {
	err := n.reload()
	if err != nil {
		return err
	}

	if n.Status() != api.NetworkStatusCreated {
		return api.StatusErrorf(http.StatusConflict, "OVN network %q is not created", n.name)
	}

	err = n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		name, err := tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		member, err := tx.GetNodeByName(ctx, name)
		if err != nil {
			return err
		}

		operation, err := tx.OVNNetworkOperation(ctx, n.project, n.name)
		if err != nil {
			return err
		}

		token, err := tx.OVNNetworkOperationToken(ctx, n.project, n.name)
		if err != nil {
			return err
		}

		local := n.localState()
		readyForRestore := local.restoring && local.started && !local.deleting && n.rawLocalStatus() == api.NetworkStatusCreated && IsAvailable(n.project, n.name)
		// Instance pre-start checks only observe readiness; they cannot initialize during restore.
		readOnlyRestore := n.ovnOperationToken == "" && readyForRestore
		restoring := member.State == db.ClusterMemberStateRestoring && (readOnlyRestore || (token != "" && token == n.ovnOperationToken && (operation == "restore" || (operation == "nic" && readyForRestore))))
		if member.State != db.ClusterMemberStateCreated && !restoring {
			return api.StatusErrorf(http.StatusConflict, "OVN member is not active; complete maintenance first")
		}

		return nil
	})
	if err != nil {
		return err
	}

	local := n.localState()
	if local.deleting {
		return api.StatusErrorf(http.StatusConflict, "OVN network %q is awaiting deletion retry", n.name)
	}

	if local.started && n.rawLocalStatus() == api.NetworkStatusCreated && IsAvailable(n.project, n.name) {
		return nil
	}

	err = n.Validate(n.config, request.ClientTypeNormal)
	if err != nil {
		return err
	}

	return n.Start()
}

// EnsureOVNLocal initializes a globally created OVN network before starting a workload.
func EnsureOVNLocal(n Network) (err error) {
	ovnNet, ok := n.(*ovn)
	if !ok {
		return nil
	}

	if ovnNet.ovnOperationToken != "" {
		release, err := ovnNet.waitOperation("nic")
		if err != nil {
			return err
		}

		defer func() { err = errors.Join(err, release()) }()
		return ovnNet.ensureLocalStarted()
	}

	unlock, err := WaitOVNLifecycle(ovnNet.state.ShutdownCtx, n.Project(), n.Name())
	if err != nil {
		return err
	}

	defer unlock()
	return ovnNet.ensureLocalStarted()
}

func (n *ovn) recordLocalStarted() error {
	err := n.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, info, _, err := tx.GetNetworkInAnyState(ctx, n.project, n.name)
		if err != nil {
			return err
		}

		operation, err := tx.OVNNetworkOperation(ctx, n.project, n.name)
		if err != nil {
			return err
		}

		token, err := tx.OVNNetworkOperationToken(ctx, n.project, n.name)
		if err != nil {
			return err
		}

		creating := n.ovnOperationToken != "" && token == n.ovnOperationToken && operation == "create" && info.Status == api.NetworkStatusErrored
		if id != n.id || (info.Status != api.NetworkStatusCreated && !creating) || operation == "delete" || (n.ovnOperationToken != "" && token != n.ovnOperationToken) {
			return api.StatusErrorf(http.StatusConflict, "OVN network changed during initialization")
		}

		nodes, err := tx.NetworkNodes(ctx, n.id)
		if err != nil {
			return err
		}

		var memberState int
		name, err := tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		member, err := tx.GetNodeByName(ctx, name)
		if err != nil {
			return err
		}

		memberState = member.State
		restoring := token == n.ovnOperationToken && operation == "restore" && memberState == db.ClusterMemberStateRestoring
		node, exists := nodes[n.state.DB.Cluster.GetNodeID()]
		if !exists || db.NetworkStateToAPIStatus(node.State) != api.NetworkStatusStarting || (memberState != db.ClusterMemberStateCreated && !restoring) {
			return api.StatusErrorf(http.StatusConflict, "OVN member state changed during initialization")
		}

		return tx.NetworkNodeCreated(n.id)
	})
	if err != nil {
		return err
	}

	return n.reload()
}

// AuthorizeOVNInitialization permits local initialization within an authenticated network operation.
func AuthorizeOVNInitialization(n Network, token ...string) {
	ovnNet, ok := n.(*ovn)
	if ok {
		ovnNet.operationAuthorized = true
		if len(token) > 0 {
			ovnNet.ovnOperationToken = token[0]
		}
	}
}

// WaitOVNLifecycle allows local initialization and parallel workload starts to wait for each other.
func WaitOVNLifecycle(ctx context.Context, projectName string, networkName string) (locking.UnlockFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return locking.Lock(ctx, fmt.Sprintf("network.ovn.lifecycle.%q.%q", projectName, networkName))
}

// OVNNeedsInitialization identifies additive returning-member work without changing driver-wide lifecycle rules.
func OVNNeedsInitialization(n Network) bool {
	ovnNet, ok := n.(*ovn)
	if !ok || n.Status() != api.NetworkStatusCreated {
		return false
	}

	switch ovnNet.rawLocalStatus() {
	case api.NetworkStatusPending, api.NetworkStatusStarting, api.NetworkStatusStopped, api.NetworkStatusUnknown:
		return true
	case api.NetworkStatusCreated:
		return !ovnNet.localState().started || !IsAvailable(n.Project(), n.Name())
	default:
		return false
	}
}

// AcquireOVNOperation reserves a network origin without allowing heartbeat-based takeover.
func AcquireOVNOperation(s *state.State, projectName string, networkName string, operation string) (func() error, string, error) {
	token := uuid.NewString()
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcquireOVNNetworkOperation(ctx, projectName, networkName, token, operation)
	})
	if err != nil {
		return nil, "", err
	}

	return func() error {
		return s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.ReleaseOVNNetworkOperation(ctx, projectName, networkName, token)
		})
	}, token, nil
}

// AcquireOVNOperationWait waits, as network operations do, for a transient conflicting reservation
// to finish before acquiring it.
func AcquireOVNOperationWait(s *state.State, projectName string, networkName string, operation string) (func() error, string, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		release, token, err := AcquireOVNOperation(s, projectName, networkName, operation)
		if err == nil || !api.StatusErrorCheck(err, http.StatusConflict) || time.Now().After(deadline) {
			return release, token, err
		}

		select {
		case <-s.ShutdownCtx.Done():
			return nil, "", errors.Join(err, s.ShutdownCtx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// EnsureOVNReturning initializes untouched returning-member networks only on active members.
func EnsureOVNReturning(n Network) error {
	ovnNet, ok := n.(*ovn)
	if !ok {
		return nil
	}

	unlock, err := WaitOVNLifecycle(ovnNet.state.ShutdownCtx, n.Project(), n.Name())
	if err != nil {
		return err
	}

	defer unlock()
	if ovnNet.state.ShutdownCtx.Err() != nil || ovnNet.state.DB.Cluster.LocalNodeIsEvacuated() {
		return nil
	}

	return ovnNet.ensureLocalStarted()
}

// waitOperation reserves the origin before waiting for local work, so a recipient is never blocked by a waiter.
func (n *ovn) waitOperation(operation string, cleanup ...bool) (func() error, error) {
	if n.ovnOperationToken != "" {
		err := n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			token, err := tx.OVNNetworkOperationToken(ctx, n.project, n.name)
			if err != nil {
				return err
			}

			if token != n.ovnOperationToken {
				return api.StatusErrorf(http.StatusConflict, "OVN parent operation has ended")
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		return func() error { return nil }, nil
	}

	parent := n.state.ShutdownCtx
	if len(cleanup) > 0 && cleanup[0] {
		parent = context.WithoutCancel(parent)
	}

	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	var release func() error
	var token string
	var err error
	for {
		release, token, err = AcquireOVNOperation(n.state, n.project, n.name, operation)
		if err == nil {
			break
		}

		if !api.StatusErrorCheck(err, http.StatusConflict) {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	unlock, err := WaitOVNLifecycle(ctx, n.project, n.name)
	if err != nil {
		return nil, errors.Join(err, release())
	}

	AuthorizeOVNInitialization(n, token)
	err = n.reload()
	if err != nil {
		unlock()
		return nil, errors.Join(err, release())
	}

	return func() error {
		defer unlock()
		defer func() { n.ovnOperationToken = "" }()
		return release()
	}, nil
}

// AcquireOVNNICOperation covers device-level writes and nested driver operations with one durable reservation.
func AcquireOVNNICOperation(n Network, cleanup bool) (func() error, error) {
	ovnNet, ok := n.(*ovn)
	if !ok {
		return nil, fmt.Errorf("Network %q is not an OVN network", n.Name())
	}

	release, err := ovnNet.waitOperation("nic", cleanup)
	if err != nil {
		return nil, err
	}

	if !cleanup {
		err = ovnNet.ensureLocalStarted()
		if err != nil {
			return nil, errors.Join(err, release())
		}
	}

	return release, nil
}

// PrepareOVNLocal is called only after acknowledged instance evacuation on this member.
func PrepareOVNLocal(n Network) (err error) {
	ovnNet, ok := n.(*ovn)
	if !ok {
		return nil
	}

	release, err := ovnNet.waitOperation("prepare")
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, release()) }()
	err = ovnNet.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.EnsureOVNNICCleanupComplete(ctx, n.ID())
	})
	if err != nil {
		return err
	}

	if ovnNet.rawLocalStatus() == api.NetworkStatusPrepared {
		return nil
	}

	if ovnNet.rawLocalStatus() == api.NetworkStatusPending {
		var enabled bool
		err = ovnNet.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			enabled, err = tx.OVNLocalInitializationEnabled(ctx, n.ID())
			return err
		})
		if err != nil {
			return err
		}

		if enabled {
			return nil
		}
	}

	err = ovnNet.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.OVNLocalPreparing(ctx, n.ID(), ovnNet.ovnOperationToken)
	})
	if err != nil {
		return err
	}

	err = ovnNet.Stop()
	if err != nil {
		return err
	}

	return ovnNet.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.OVNLocalPrepared(ctx, n.ID(), ovnNet.ovnOperationToken)
	})
}

// RestoreOVNLocal cancels preparation synchronously using the latest shared configuration.
func RestoreOVNLocal(n Network) (err error) {
	ovnNet, ok := n.(*ovn)
	if !ok {
		return nil
	}

	release, err := ovnNet.waitOperation("restore")
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, release()) }()
	local := ovnNet.localState()
	local.restoring = false
	ovnNet.setLocalState(local)
	if ovnNet.rawLocalStatus() == api.NetworkStatusCreated && local.started && !local.deleting && IsAvailable(n.Project(), n.Name()) {
		if !maps.Equal(local.config, ovnNet.config) {
			return api.StatusErrorf(http.StatusConflict, "OVN network %q changed during partial restore; evacuate again before restoring", n.Name())
		}
		// A restore retry must preserve networks used by already restored workloads.
		local.restoring = true
		ovnNet.setLocalState(local)
		return nil
	}

	err = ovnNet.Validate(ovnNet.config, request.ClientTypeNormal)
	if err != nil {
		return err
	}

	err = ovnNet.Start()
	if err == nil {
		local := ovnNet.localState()
		local.restoring = true
		ovnNet.setLocalState(local)
	}

	return err
}

// RestoreOVNLocalUncreated returns this member's maintenance row of a network that is not globally
// created to Stopped, as after a failed creation, so its create retry repairs it. Unacknowledged
// preparation is completed first, so no local resources remain.
func RestoreOVNLocalUncreated(n Network) (err error) {
	ovnNet, ok := n.(*ovn)
	if !ok {
		return nil
	}

	// A row that preparation never changed needs no reservation.
	switch ovnNet.rawLocalStatus() {
	case api.NetworkStatusPreparing, api.NetworkStatusPrepared:
	default:
		return nil
	}

	release, err := ovnNet.waitOperation("restore")
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, release()) }()
	switch ovnNet.rawLocalStatus() {
	case api.NetworkStatusPrepared:
	case api.NetworkStatusPreparing, api.NetworkStatusStopped:
		err = ovnNet.Stop()
		if err != nil {
			return err
		}

	default:
		return nil
	}

	return ovnNet.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.OVNLocalReturnStopped(ctx, n.ID(), ovnNet.ovnOperationToken)
	})
}

// LocalStatus exposes raw lifecycle states before the availability overlay.
func (n *ovn) LocalStatus() string {
	if n.status == api.NetworkStatusDeleting {
		return api.NetworkStatusDeleting
	}

	status := n.rawLocalStatus()
	switch status {
	case api.NetworkStatusPending, api.NetworkStatusStarting, api.NetworkStatusPreparing, api.NetworkStatusPrepared, api.NetworkStatusStopped:
		return status
	case api.NetworkStatusCreated:
		if !n.localState().started {
			return api.NetworkStatusUnavailable
		}
	}

	return n.common.LocalStatus()
}

// ClearOVNRestoreReadiness gates pre-start checks until this restore cycle confirms each network.
func ClearOVNRestoreReadiness(n Network) error {
	ovnNet, ok := n.(*ovn)
	if !ok {
		return nil
	}

	unlock, err := WaitOVNLifecycle(ovnNet.state.ShutdownCtx, n.Project(), n.Name())
	if err != nil {
		return err
	}

	defer unlock()
	local := ovnNet.localState()
	local.restoring = false
	ovnNet.setLocalState(local)
	return nil
}

// waitLocalLifecycle reserves NIC cleanup against peer changes even after daemon shutdown has begun.
func (n *ovn) waitLocalLifecycle() (func() error, error) {
	return n.waitOperation("nic", true)
}

// RestartOVNLocal waits for an existing origin before changing local chassis membership.
func RestartOVNLocal(n Network) (err error) {
	ovnNet, ok := n.(*ovn)
	if !ok {
		return nil
	}

	release, err := ovnNet.waitOperation("initialize")
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, release()) }()
	return ovnNet.Start()
}

// OVNRawLocalStatus exposes durable ownership to creation without the readiness overlay.
func OVNRawLocalStatus(n Network) string {
	ovnNet, ok := n.(*ovn)
	if !ok {
		return n.LocalStatus()
	}

	return ovnNet.rawLocalStatus()
}

// RetireOVNACLNetworkGroups retires the network's per-network groups of the named ACLs once no NIC
// or network uses them. A group keeps the network's router port after its last NIC user, and only
// the network's normal update reservation may retire it.
func RetireOVNACLNetworkGroups(n Network, aclNames []string) (err error) {
	ovnNet, ok := n.(*ovn)
	if !ok {
		return nil
	}

	release, err := ovnNet.waitOperation("update")
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, release()) }()
	keep := util.SplitNTrimSpace(ovnNet.config["security.acls"], ",", -1, true)
	_, err = acl.OVNRetireNetworkACLGroups(ovnNet.state, ovnNet.ovnnb, ovnNet.project, ovnNet.name, ovnNet.ID(), ovnNet.parentID, ovnNet.ovnOperationToken, aclNames, keep)
	return err
}
