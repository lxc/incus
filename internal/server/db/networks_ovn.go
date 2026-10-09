//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/internal/server/db/query"
	"github.com/lxc/incus/v7/shared/api"
)

// OVNPeerOperationName cannot collide with an API network name.
const OVNPeerOperationName = "cluster/ovn-peering"

// OVNBackendID retains the private backend owner across daemon restart and cluster membership changes.
func (n *NodeTx) OVNBackendID(ctx context.Context) (string, error) {
	_, err := n.ExecContext(ctx, `INSERT INTO networks_ovn_backend (id, backend_id) VALUES (1, ?) ON CONFLICT (id) DO NOTHING`, uuid.NewString())
	if err != nil {
		return "", err
	}

	var id string
	err = n.QueryRowContext(ctx, `SELECT backend_id FROM networks_ovn_backend WHERE id=1`).Scan(&id)
	return id, err
}

// OVNBackendRoot returns the database root bound to a backend kind after OVNBackendID initialized the private owner.
func (n *NodeTx) OVNBackendRoot(ctx context.Context, kind string) (string, error) {
	column, err := ovnBackendRootColumn(kind)
	if err != nil {
		return "", err
	}

	var root string
	err = n.QueryRowContext(ctx, fmt.Sprintf("SELECT %s FROM networks_ovn_backend WHERE id=1", column)).Scan(&root)
	return root, err
}

// BindOVNBackendRoot binds a client before exposure, with configuration excluded throughout the fresh OVNBackendRebindBlocked check and binding.
func (n *NodeTx) BindOVNBackendRoot(ctx context.Context, kind string, expectedRoot string, rootUUID string, rebindBlocked bool) error {
	column, err := ovnBackendRootColumn(kind)
	if err != nil {
		return err
	}

	for i, value := range []string{rootUUID, expectedRoot} {
		if i == 1 && value == "" {
			continue
		}

		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return fmt.Errorf("Invalid OVN %s database root UUID %q", kind, value)
		}
	}

	root, err := n.OVNBackendRoot(ctx, kind)
	if err != nil {
		return err
	}

	if root != expectedRoot {
		return api.StatusErrorf(http.StatusConflict, "OVN %s database root binding changed during client setup", kind)
	}

	if root == rootUUID {
		return nil
	}

	if root != "" && rebindBlocked {
		return api.StatusErrorf(http.StatusConflict, "OVN %s database root changed while backend rebinding is blocked", kind)
	}

	result, err := n.ExecContext(ctx, fmt.Sprintf("UPDATE networks_ovn_backend SET %s=? WHERE id=1 AND %s=?", column, column), rootUUID, expectedRoot)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if count != 1 {
		return api.StatusErrorf(http.StatusConflict, "OVN %s database root binding changed during client setup", kind)
	}

	return nil
}

func ovnBackendRootColumn(kind string) (string, error) {
	switch kind {
	case "ovs":
		return "ovs_root", nil
	case "nb":
		return "nb_root", nil
	case "sb":
		return "sb_root", nil
	default:
		return "", fmt.Errorf("Invalid OVN backend kind %q", kind)
	}
}

// RegisterOVNBackendID binds the stable local owner to this membership without replacing outstanding provenance.
func (c *ClusterTx) RegisterOVNBackendID(ctx context.Context, backendID string) error {
	_, err := uuid.Parse(backendID)
	if err != nil {
		return fmt.Errorf("Invalid OVN backend identity: %w", err)
	}

	var id string
	err = c.tx.QueryRowContext(ctx, `SELECT backend_id FROM networks_ovn_members WHERE node_id=?`, c.nodeID).Scan(&id)
	if err == nil {
		if id != backendID {
			return fmt.Errorf("OVN backend identity changed for member %d", c.nodeID)
		}

		return nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var pending bool
	err = c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_operations WHERE node_id=? AND backend_fenced=1) OR EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE node_id=? AND backend_fenced=1)`, c.nodeID, c.nodeID).Scan(&pending)
	if err != nil {
		return err
	}

	if pending {
		return fmt.Errorf("OVN backend identity is missing for member %d with outstanding fenced work", c.nodeID)
	}

	_, err = c.tx.ExecContext(ctx, `INSERT INTO networks_ovn_members (node_id, backend_id) VALUES (?, ?)`, c.nodeID, backendID)
	return err
}

// AcquireOVNNetworkOperation serializes network origins without treating heartbeat loss as fencing.
func (c *ClusterTx) AcquireOVNNetworkOperation(ctx context.Context, projectName string, name string, token string, operation string) error {
	referenceErr := c.BeginOVNReferenceActivation(ctx)
	if referenceErr != nil {
		return referenceErr
	}

	result, err := c.tx.ExecContext(ctx, `
INSERT INTO networks_ovn_operations (project_id, name, node_id, token, operation, backend_fenced)
SELECT (SELECT id FROM projects WHERE name=?), ?, ?, ?, ?, 1
WHERE NOT EXISTS (SELECT 1 FROM networks_ovn_operations WHERE project_id=(SELECT id FROM projects WHERE name=?) AND name=?)
ON CONFLICT (project_id, name) DO NOTHING
`, projectName, name, c.nodeID, token, operation, api.ProjectDefaultName, OVNPeerOperationName)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if count != 1 {
		var owner string
		var kind string
		var recipients string
		err = c.tx.QueryRowContext(ctx, `SELECT COALESCE(member.name, 'removed member #' || op.node_id), op.operation, COALESCE((SELECT group_concat(COALESCE(recipient.name, 'member #' || notification.node_id)) FROM networks_ovn_notifications notification LEFT JOIN nodes recipient ON recipient.id=notification.node_id WHERE notification.token=op.token), '') FROM networks_ovn_operations op LEFT JOIN nodes member ON member.id=op.node_id WHERE (op.project_id=(SELECT id FROM projects WHERE name=?) AND op.name=?) OR (op.project_id=(SELECT id FROM projects WHERE name=?) AND op.name=?) LIMIT 1`, projectName, name, api.ProjectDefaultName, OVNPeerOperationName).Scan(&owner, &kind, &recipients)
		if err != nil {
			return err
		}

		return api.StatusErrorf(http.StatusConflict, "OVN network %q has another cluster operation in progress: %s owns %s (unacknowledged recipients: %s)", name, owner, kind, recipients)
	}

	return nil
}

// AcquireOVNPeerOperation excludes lifecycle origins while peers change shared routers or interconnect resources.
func (c *ClusterTx) AcquireOVNPeerOperation(ctx context.Context, token string, operation string, backendFenced bool) error {
	if backendFenced {
		referenceErr := c.BeginOVNReferenceActivation(ctx)
		if referenceErr != nil {
			return referenceErr
		}
	}

	result, err := c.tx.ExecContext(ctx, `
INSERT INTO networks_ovn_operations (project_id, name, node_id, token, operation, backend_fenced)
SELECT (SELECT id FROM projects WHERE name=?), ?, ?, ?, ?, ?
WHERE NOT EXISTS (SELECT 1 FROM networks_ovn_operations)
AND NOT EXISTS (SELECT 1 FROM instances_profile_reference_apply WHERE active_token != '')
`, api.ProjectDefaultName, OVNPeerOperationName, c.nodeID, token, operation, backendFenced)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if count != 1 {
		return api.StatusErrorf(http.StatusConflict, "Shared OVN resource change conflicts with an outstanding network or instance operation")
	}

	return nil
}

// WithOVNPeerIntegrationOperation reserves integration database mutations within their existing transaction.
func (c *ClusterTx) WithOVNPeerIntegrationOperation(ctx context.Context, update func() error) error {
	token := uuid.NewString()
	err := c.AcquireOVNPeerOperation(ctx, token, "peer-integration", true)
	if err != nil {
		return err
	}

	err = update()
	if err != nil {
		return err
	}

	return c.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, OVNPeerOperationName, token)
}

// ValidateOVNPeerReady rejects stale identities and maintenance states without claiming local daemon readiness.
func (c *ClusterTx) ValidateOVNPeerReady(ctx context.Context, projectName string, name string, networkID int64) error {
	id, info, nodes, err := c.GetNetworkInAnyState(ctx, projectName, name)
	if err != nil {
		return err
	}

	local, exists := nodes[c.nodeID]
	if id != networkID || info.Status != api.NetworkStatusCreated || !exists || NetworkStateToAPIStatus(local.State) != api.NetworkStatusCreated {
		return api.StatusErrorf(http.StatusConflict, "OVN network %q is not created on this member", name)
	}

	var memberState int
	err = c.tx.QueryRowContext(ctx, "SELECT state FROM nodes WHERE id=?", c.nodeID).Scan(&memberState)
	if err != nil {
		return err
	}

	if memberState != ClusterMemberStateCreated {
		return api.StatusErrorf(http.StatusConflict, "OVN member is not active; complete maintenance first")
	}

	return nil
}

// ReleaseOVNNetworkOperation releases only the operation identified by its token.
func (c *ClusterTx) ReleaseOVNNetworkOperation(ctx context.Context, projectName string, name string, token string) error {
	_, err := c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_operations WHERE NOT EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE token=networks_ovn_operations.token) AND project_id=(SELECT id FROM projects WHERE name=?) AND name=? AND token=?`, projectName, name, token)
	if err != nil {
		return err
	}

	current, err := c.OVNNetworkOperationToken(ctx, projectName, name)
	if err != nil {
		return err
	}

	if current == token {
		return api.StatusErrorf(http.StatusConflict, "OVN operation still has unacknowledged notifications")
	}

	return nil
}

// HasLocalOVNNetworkOperations detects previous local work without releasing or abandoning it.
func (c *ClusterTx) HasLocalOVNNetworkOperations(ctx context.Context) (bool, error) {
	var pending bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_operations WHERE node_id=?) OR EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE node_id=?)`, c.nodeID, c.nodeID).Scan(&pending)
	return pending, err
}

// HasLocalFencedOVNWork detects only local work eligible for automatic release after backend fencing.
func (c *ClusterTx) HasLocalFencedOVNWork(ctx context.Context) (bool, error) {
	var pending bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_operations WHERE node_id=? AND backend_fenced=1) OR EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE node_id=? AND backend_fenced=1)`, c.nodeID, c.nodeID).Scan(&pending)
	return pending, err
}

// OVNPreviousReceipt identifies accepted work captured before the current daemon admits requests.
type OVNPreviousReceipt struct {
	ID    string
	Token string
}

// OVNPreviousWork limits startup recovery to exact prior identities from one membership.
type OVNPreviousWork struct {
	NodeID       int64
	OriginTokens []string
	Receipts     []OVNPreviousReceipt
}

// SnapshotLocalOVNFencedWork captures prior local work while the process-lifetime lock excludes the previous daemon.
func (c *ClusterTx) SnapshotLocalOVNFencedWork(ctx context.Context) (OVNPreviousWork, error) {
	previous := OVNPreviousWork{NodeID: c.nodeID}
	var err error
	previous.OriginTokens, err = query.SelectStrings(ctx, c.tx, `SELECT token FROM networks_ovn_operations WHERE node_id=? AND backend_fenced=1 ORDER BY token`, c.nodeID)
	if err != nil {
		return OVNPreviousWork{}, err
	}

	rows, err := c.tx.QueryContext(ctx, `SELECT id, token FROM networks_ovn_notifications WHERE node_id=? AND backend_fenced=1 ORDER BY id`, c.nodeID)
	if err != nil {
		return OVNPreviousWork{}, err
	}

	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var receipt OVNPreviousReceipt
		err = rows.Scan(&receipt.ID, &receipt.Token)
		if err != nil {
			return OVNPreviousWork{}, err
		}

		previous.Receipts = append(previous.Receipts, receipt)
	}

	return previous, rows.Err()
}

// ClearPreviousOVNFencedWork requires all backend fences and releases only identities captured before this daemon admitted requests.
func (c *ClusterTx) ClearPreviousOVNFencedWork(ctx context.Context, previous OVNPreviousWork) error {
	if previous.NodeID != c.nodeID {
		return api.StatusErrorf(http.StatusConflict, "OVN previous work belongs to another member")
	}

	for _, receipt := range previous.Receipts {
		_, err := c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_notifications WHERE id=? AND token=? AND node_id=? AND backend_fenced=1`, receipt.ID, receipt.Token, c.nodeID)
		if err != nil {
			return err
		}
	}

	for _, token := range previous.OriginTokens {
		_, err := c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_notifications WHERE token=? AND node_id=0 AND EXISTS (SELECT 1 FROM networks_ovn_operations WHERE token=? AND node_id=? AND backend_fenced=1)`, token, token, c.nodeID)
		if err != nil {
			return err
		}

		_, err = c.tx.ExecContext(ctx, `UPDATE networks_ovn_operations SET abandoned=1 WHERE token=? AND node_id=? AND backend_fenced=1 AND ic_integration_id IS NULL`, token, c.nodeID)
		if err != nil {
			return err
		}
	}

	_, err := c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_operations WHERE abandoned=1 AND backend_fenced=1 AND ic_integration_id IS NULL AND NOT EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE token=networks_ovn_operations.token)`)
	return err
}

// ClearLocalOVNBackendConfig requires the process-lifetime lock and releases only a prior local configuration-only reservation without receipts.
func (c *ClusterTx) ClearLocalOVNBackendConfig(ctx context.Context) error {
	_, err := c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_operations WHERE node_id=? AND project_id=(SELECT id FROM projects WHERE name=?) AND name=? AND ((operation='backend-config' AND backend_fenced=1) OR (operation IN ('acl-config', 'address-set-config') AND backend_fenced=0)) AND NOT EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE token=networks_ovn_operations.token)`, c.nodeID, api.ProjectDefaultName, OVNPeerOperationName)
	return err
}

// HasLocalOVNBackendWork excludes configuration-only origins while retaining every locally accepted receipt.
func (c *ClusterTx) HasLocalOVNBackendWork(ctx context.Context) (bool, error) {
	var pending bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_operations WHERE node_id=? AND operation!='backend-config') OR EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE node_id=?)`, c.nodeID, c.nodeID).Scan(&pending)
	return pending, err
}

// OVNBackendRebindBlocked retains backend roots while any OVN definition, resource operation or notification remains in the cluster.
func (c *ClusterTx) OVNBackendRebindBlocked(ctx context.Context) (bool, error) {
	var blocked bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks WHERE type=?) OR EXISTS (SELECT 1 FROM networks_ovn_operations WHERE operation!='backend-config') OR EXISTS (SELECT 1 FROM networks_ovn_notifications)`, NetworkTypeOVN).Scan(&blocked)
	return blocked, err
}

// OVNLocalBackendRebindBlocked reports local unresolved work whose plans or receipts are bound to this
// member's current backend roots: operations, receipts, incomplete NIC cleanup or open migrations.
func (c *ClusterTx) OVNLocalBackendRebindBlocked(ctx context.Context) (bool, error) {
	var blocked bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_operations WHERE node_id=? AND operation!='backend-config') OR EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE node_id=?) OR EXISTS (SELECT 1 FROM networks_ovn_nic_cleanup WHERE source_node_id=? AND completed=0) OR EXISTS (SELECT 1 FROM networks_ovn_nic_migrations WHERE (source_node_id=? OR target_node_id=?) AND phase IN ('authorized','handover'))`, c.nodeID, c.nodeID, c.nodeID, c.nodeID, c.nodeID).Scan(&blocked)
	return blocked, err
}

// ClearLocalOVNNetworkOperations requires completed backend fencing and retains work from older unfenced daemons.
func (c *ClusterTx) ClearLocalOVNNetworkOperations(ctx context.Context) error {
	_, err := c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_notifications WHERE (node_id=? AND backend_fenced=1) OR (node_id=0 AND token IN (SELECT token FROM networks_ovn_operations WHERE node_id=?))`, c.nodeID, c.nodeID)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE networks_ovn_operations SET abandoned=1 WHERE node_id=? AND backend_fenced=1 AND ic_integration_id IS NULL`, c.nodeID)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_operations WHERE abandoned=1 AND backend_fenced=1 AND ic_integration_id IS NULL AND NOT EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE token=networks_ovn_operations.token)`)
	return err
}

// OVNNetworkOperation returns the active operation, if any, for this network name.
func (c *ClusterTx) OVNNetworkOperation(ctx context.Context, projectName string, name string) (string, error) {
	var operation string
	err := c.tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT operation FROM networks_ovn_operations WHERE project_id=(SELECT id FROM projects WHERE name=?) AND name=?), '')`, projectName, name).Scan(&operation)
	return operation, err
}

// EnableOVNLocalInitialization records the local-state contract only for newly defined networks.
func (c *ClusterTx) EnableOVNLocalInitialization(ctx context.Context, networkID int64) error {
	_, err := c.tx.ExecContext(ctx, `INSERT INTO networks_ovn_local_initialization (network_id) VALUES (?) ON CONFLICT DO NOTHING`, networkID)
	return err
}

// OVNLocalInitializationEnabled distinguishes the new contract from historical Pending rows.
func (c *ClusterTx) OVNLocalInitializationEnabled(ctx context.Context, networkID int64) (bool, error) {
	var enabled bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_local_initialization WHERE network_id=?)`, networkID).Scan(&enabled)
	return enabled, err
}

// ClaimOVNLocalInitialization changes the raw row before any member-local effects begin.
func (c *ClusterTx) ClaimOVNLocalInitialization(ctx context.Context, projectName string, name string, networkID int64, authorizedToken string) error {
	id, info, nodes, err := c.GetNetworkInAnyState(ctx, projectName, name)
	if err != nil {
		return err
	}

	operation, err := c.OVNNetworkOperation(ctx, projectName, name)
	if err != nil {
		return err
	}

	currentToken, err := c.OVNNetworkOperationToken(ctx, projectName, name)
	if err != nil {
		return err
	}

	authorizedOperation := authorizedToken != "" && authorizedToken == currentToken
	if authorizedToken != "" && !authorizedOperation {
		return api.StatusErrorf(http.StatusConflict, "OVN initialization operation has ended")
	}

	var memberState int
	err = c.tx.QueryRowContext(ctx, `SELECT state FROM nodes WHERE id=?`, c.nodeID).Scan(&memberState)
	if err != nil {
		return err
	}

	node, exists := nodes[c.nodeID]
	restoring := authorizedOperation && operation == "restore" && memberState == ClusterMemberStateRestoring
	if memberState != ClusterMemberStateCreated && !restoring {
		return api.StatusErrorf(http.StatusConflict, "OVN member is not active")
	}

	if (node.State == networkPreparing || node.State == networkPrepared) && !restoring {
		return api.StatusErrorf(http.StatusConflict, "OVN network is prepared for maintenance; restore the member first")
	}

	creating := authorizedOperation && operation == "create" && info.Status == api.NetworkStatusErrored
	if id != networkID || (info.Status != api.NetworkStatusCreated && !creating) {
		return api.StatusErrorf(http.StatusConflict, "OVN network changed before initialization")
	}

	if operation == "delete" || (operation != "" && !authorizedOperation) {
		return api.StatusErrorf(http.StatusConflict, "OVN network %q has another cluster operation in progress", name)
	}

	if !exists {
		// OVN networks have no pre-join member config. Claim ownership for a newly joined
		// member before any effects, without manufacturing a never-initialized deletion proof.
		_, err := c.tx.ExecContext(ctx, "INSERT INTO networks_nodes (network_id, node_id, state) VALUES (?, ?, ?)", networkID, c.nodeID, networkStarting)
		return err
	}

	return c.networkNodeState(networkID, networkStarting)
}

// OVNNetworkOperationToken returns the token of the currently authorized network operation.
func (c *ClusterTx) OVNNetworkOperationToken(ctx context.Context, projectName string, name string) (string, error) {
	var token string
	err := c.tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT token FROM networks_ovn_operations WHERE project_id=(SELECT id FROM projects WHERE name=?) AND name=?), '')`, projectName, name).Scan(&token)
	return token, err
}

// ValidateOVNDelete rechecks skipped members' offline deletion eligibility before deleting the authoritative row.
func (c *ClusterTx) ValidateOVNDelete(ctx context.Context, networkID int64, skipped []int64) error {
	nodes, err := c.NetworkNodes(ctx, networkID)
	if err != nil {
		return err
	}

	enabled, err := c.OVNLocalInitializationEnabled(ctx, networkID)
	if err != nil {
		return err
	}

	for _, id := range skipped {
		node, exists := nodes[id]
		var memberState int
		err := c.tx.QueryRowContext(ctx, `SELECT state FROM nodes WHERE id=?`, id).Scan(&memberState)
		if err != nil {
			return err
		}

		prepared := node.State == networkPrepared && memberState == ClusterMemberStateEvacuated
		neverInitialized := enabled && node.State == networkPending
		if !exists || (!neverInitialized && !prepared) {
			return api.StatusErrorf(http.StatusConflict, "Skipped OVN member is not eligible for offline deletion")
		}
	}

	return nil
}

// OVNDeletionRelyingOnMember returns a network whose active deletion may have skipped this
// member as prepared, or "" when changing the member state cannot invalidate a deletion.
func (c *ClusterTx) OVNDeletionRelyingOnMember(ctx context.Context, memberID int64) (string, error) {
	var name string
	err := c.tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT n.name FROM networks n JOIN networks_nodes local ON local.network_id=n.id AND local.node_id=? JOIN networks_ovn_operations op ON op.project_id=n.project_id AND op.name=n.name WHERE op.operation='delete' AND local.state=? ORDER BY n.id LIMIT 1), '')`, memberID, networkPrepared).Scan(&name)
	return name, err
}

// AddOVNNotification reserves an unaccepted request before it is dispatched.
func (c *ClusterTx) AddOVNNotification(ctx context.Context, id string, token string) error {
	_, err := c.tx.ExecContext(ctx, `INSERT INTO networks_ovn_notifications (id, token) VALUES (?, ?)`, id, token)
	return err
}

// AcceptOVNNotification atomically distinguishes entered work from a cancelled request.
func (c *ClusterTx) AcceptOVNNotification(ctx context.Context, id string, token string) error {
	result, err := c.tx.ExecContext(ctx, `UPDATE networks_ovn_notifications SET node_id=?, backend_fenced=1 WHERE id=? AND token=? AND node_id=0 AND EXISTS (SELECT 1 FROM networks_ovn_operations WHERE token=?)`, c.nodeID, id, token, token)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if count != 1 {
		return api.StatusErrorf(http.StatusConflict, "OVN notification was cancelled or already accepted")
	}

	return nil
}

// FinishOVNNotification acknowledges completed work only from the accepting member.
func (c *ClusterTx) FinishOVNNotification(ctx context.Context, id string) error {
	_, err := c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_notifications WHERE id=? AND node_id=?`, id, c.nodeID)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_operations WHERE abandoned=1 AND backend_fenced=1 AND ic_integration_id IS NULL AND NOT EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE token=networks_ovn_operations.token)`)
	return err
}

// CancelOVNNotification cancels only unaccepted work and reports whether entered work remains.
func (c *ClusterTx) CancelOVNNotification(ctx context.Context, id string) (bool, error) {
	_, err := c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_notifications WHERE id=? AND node_id=0`, id)
	if err != nil {
		return false, err
	}

	var active bool
	err = c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE id=?)`, id).Scan(&active)
	return active, err
}

// OVNLocalPreparing gates initialization before acknowledged maintenance cleanup begins.
func (c *ClusterTx) OVNLocalPreparing(ctx context.Context, networkID int64, token string) error {
	err := c.EnsureOVNNICCleanupComplete(ctx, networkID)
	if err != nil {
		return err
	}

	var allowed bool
	err = c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks n JOIN networks_nodes local ON local.network_id=n.id AND local.node_id=? JOIN nodes member ON member.id=local.node_id JOIN networks_ovn_operations op ON op.project_id=n.project_id AND op.name=n.name WHERE n.id=? AND op.token=? AND op.operation='prepare' AND member.state=? AND local.state IN (?,?,?,?,?,?))`, c.nodeID, networkID, token, ClusterMemberStateEvacuating, networkPending, networkCreated, networkStarting, networkPreparing, networkPrepared, networkStopped).Scan(&allowed)
	if err != nil {
		return err
	}

	if !allowed {
		return api.StatusErrorf(http.StatusConflict, "OVN preparation no longer owns an evacuating member")
	}

	return c.networkNodeState(networkID, networkPreparing)
}

// OVNLocalPrepared acknowledges successful cleanup under the same preparation token.
func (c *ClusterTx) OVNLocalPrepared(ctx context.Context, networkID int64, token string) error {
	err := c.EnsureOVNNICCleanupComplete(ctx, networkID)
	if err != nil {
		return err
	}

	var allowed bool
	err = c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks n JOIN networks_nodes local ON local.network_id=n.id AND local.node_id=? JOIN nodes member ON member.id=local.node_id JOIN networks_ovn_operations op ON op.project_id=n.project_id AND op.name=n.name WHERE n.id=? AND op.token=? AND op.operation='prepare' AND local.state=? AND member.state=?)`, c.nodeID, networkID, token, networkStopped, ClusterMemberStateEvacuating).Scan(&allowed)
	if err != nil {
		return err
	}

	if !allowed {
		return api.StatusErrorf(http.StatusConflict, "OVN preparation changed before acknowledgement")
	}

	return c.networkNodeState(networkID, networkPrepared)
}

// OVNChildNetworks counts the OVN networks in any state that name this network as their parent.
// It runs in the transaction marking the parent deleting, which serializes with CheckOVNParentCreated.
func (c *ClusterTx) OVNChildNetworks(ctx context.Context, projectName string, name string) (int, error) {
	var children int
	err := c.tx.QueryRowContext(ctx, `SELECT count(*) FROM networks n JOIN projects p ON p.id=n.project_id JOIN networks_config cfg ON cfg.network_id=n.id AND cfg.node_id IS NULL AND cfg.key='parent' WHERE p.name=? AND n.type=? AND cfg.value=?`, projectName, NetworkTypeOVN, name).Scan(&children)
	return children, err
}

// CheckOVNParentCreated refuses a child network definition unless its parent is globally created,
// in the transaction that stores the child's configuration.
func (c *ClusterTx) CheckOVNParentCreated(ctx context.Context, projectName string, parent string) error {
	var state NetworkState
	err := c.tx.QueryRowContext(ctx, `SELECT n.state FROM networks n JOIN projects p ON p.id=n.project_id WHERE p.name=? AND n.name=?`, projectName, parent).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return api.StatusErrorf(http.StatusNotFound, "Parent network %q not found", parent)
	}

	if err != nil {
		return err
	}

	if state != networkCreated {
		return api.StatusErrorf(http.StatusConflict, "Parent network %q is not created", parent)
	}

	return nil
}

// OVNLocalReturnStopped returns this member's maintenance row of a network that is not globally
// created to Stopped under the restore token. Its local resources were removed by preparation, as
// after a failed creation, so the network's create retry repairs it and its deletion removes it.
func (c *ClusterTx) OVNLocalReturnStopped(ctx context.Context, networkID int64, token string) error {
	var allowed bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks n JOIN networks_nodes local ON local.network_id=n.id AND local.node_id=? JOIN nodes member ON member.id=local.node_id JOIN networks_ovn_operations op ON op.project_id=n.project_id AND op.name=n.name WHERE n.id=? AND n.state<>? AND op.token=? AND op.operation='restore' AND local.state IN (?,?,?) AND member.state=?)`, c.nodeID, networkID, networkCreated, token, networkPreparing, networkPrepared, networkStopped, ClusterMemberStateRestoring).Scan(&allowed)
	if err != nil {
		return err
	}

	if !allowed {
		return api.StatusErrorf(http.StatusConflict, "OVN restore no longer owns a prepared row of an uncreated network")
	}

	return c.networkNodeState(networkID, networkStopped)
}

// OVNNotificationsPending reports accepted or uncertain work protected by an operation token.
func (c *ClusterTx) OVNNotificationsPending(ctx context.Context, token string) (bool, error) {
	var pending bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE token=?)`, token).Scan(&pending)
	return pending, err
}

// OVNLocalStopped publishes drained local ownership under the uplink serialization lock.
func (c *ClusterTx) OVNLocalStopped(ctx context.Context, projectName string, name string, networkID int64, token string) error {
	current, err := c.OVNNetworkOperationToken(ctx, projectName, name)
	if err != nil {
		return err
	}

	if token == "" || current != token {
		return api.StatusErrorf(http.StatusConflict, "OVN cleanup operation has ended")
	}

	id, _, nodes, err := c.GetNetworkInAnyState(ctx, projectName, name)
	if err != nil {
		return err
	}

	if id != networkID {
		return api.StatusErrorf(http.StatusConflict, "OVN network changed during cleanup")
	}

	local, exists := nodes[c.nodeID]
	if exists && local.State == networkPending {
		enabled, err := c.OVNLocalInitializationEnabled(ctx, networkID)
		if err != nil {
			return err
		}

		if enabled {
			return nil
		}
	}

	if exists && local.State == networkPrepared {
		return api.StatusErrorf(http.StatusConflict, "Prepared OVN network requires explicit restore")
	}

	return c.networkNodeState(networkID, networkStopped)
}
