//go:build linux && cgo && !agent

package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/shared/api"
)

// OVNNICCleanup holds the exact original inputs for one source member's cleanup attempt.
// It deliberately has no instance foreign key: moving or deleting an instance must not erase its source's debt.
// Completed attempts retain their immutable identity for reconciliation of an uncertain completion write.
type OVNNICCleanup struct {
	Generation   string
	SourceNodeID int64
	InstanceUUID string
	DeviceName   string
	Version      int
	NetworkIDs   []int64
	Payload      string
	Completed    bool
}

func (a OVNNICCleanup) validate() error {
	for _, value := range []string{a.Generation, a.InstanceUUID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return fmt.Errorf("Invalid OVN NIC cleanup UUID %q", value)
		}
	}

	if a.Version != 1 || strings.TrimSpace(a.DeviceName) == "" || a.SourceNodeID <= 0 {
		return errors.New("Invalid OVN NIC cleanup identity or version")
	}

	if len(a.NetworkIDs) == 0 || !slices.IsSorted(a.NetworkIDs) {
		return errors.New("OVN NIC cleanup networks must be a nonempty ordered set")
	}

	for i, id := range a.NetworkIDs {
		if id <= 0 || (i > 0 && id == a.NetworkIDs[i-1]) {
			return errors.New("Invalid OVN NIC cleanup network identity")
		}
	}

	if len(a.Payload) > 1024*1024 || !json.Valid([]byte(a.Payload)) || !strings.HasPrefix(strings.TrimSpace(a.Payload), "{") {
		return errors.New("Invalid OVN NIC cleanup payload")
	}

	return nil
}

func (a OVNNICCleanup) matches(other OVNNICCleanup) bool {
	return a.Generation == other.Generation && a.SourceNodeID == other.SourceNodeID &&
		a.InstanceUUID == other.InstanceUUID && a.DeviceName == other.DeviceName &&
		a.Version == other.Version && slices.Equal(a.NetworkIDs, other.NetworkIDs) && a.Payload == other.Payload
}

func scanOVNNICCleanup(row interface{ Scan(...any) error }) (OVNNICCleanup, error) {
	var a OVNNICCleanup
	var networks string
	err := row.Scan(&a.Generation, &a.SourceNodeID, &a.InstanceUUID, &a.DeviceName, &a.Version, &networks, &a.Payload, &a.Completed)
	if err != nil {
		return OVNNICCleanup{}, err
	}

	err = json.Unmarshal([]byte(networks), &a.NetworkIDs)
	if err != nil {
		return OVNNICCleanup{}, fmt.Errorf("Invalid stored OVN NIC cleanup networks: %w", err)
	}

	err = a.validate()
	return a, err
}

// OVNNICCleanupByGeneration reads even completed attempts; absence never acknowledges an uncertain write.
func (c *ClusterTx) OVNNICCleanupByGeneration(ctx context.Context, generation string) (OVNNICCleanup, error) {
	return scanOVNNICCleanup(c.tx.QueryRowContext(ctx, `SELECT generation, source_node_id, instance_uuid, device_name, version, network_ids, payload, completed FROM networks_ovn_nic_cleanup WHERE generation=?`, generation))
}

// OVNNICCleanupForDevice reads this original source member's pending attempt.
// Current placement/configuration is deliberately not a join condition.
func (c *ClusterTx) OVNNICCleanupForDevice(ctx context.Context, instanceUUID string, deviceName string) (OVNNICCleanup, error) {
	return scanOVNNICCleanup(c.tx.QueryRowContext(ctx, `SELECT generation, source_node_id, instance_uuid, device_name, version, network_ids, payload, completed FROM networks_ovn_nic_cleanup WHERE source_node_id=? AND instance_uuid=? AND device_name=? AND completed=0`, c.nodeID, instanceUUID, deviceName))
}

// OVNNICCleanups enumerates this original source member's debt, independently of current instance placement/configuration.
func (c *ClusterTx) OVNNICCleanups(ctx context.Context) ([]OVNNICCleanup, error) {
	rows, err := c.tx.QueryContext(ctx, `SELECT generation, source_node_id, instance_uuid, device_name, version, network_ids, payload, completed FROM networks_ovn_nic_cleanup WHERE source_node_id=? AND completed=0 ORDER BY generation`, c.nodeID)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()
	attempts := []OVNNICCleanup{}
	for rows.Next() {
		a, err := scanOVNNICCleanup(rows)
		if err != nil {
			return nil, err
		}

		attempts = append(attempts, a)
	}

	return attempts, rows.Err()
}

// validateOVNNICCleanupTokens requires the entire immutable network set to remain reserved in this transaction.
// Captures cannot enter a preparation reservation; completion may consume an inherited preparation token.
func (c *ClusterTx) validateOVNNICCleanupTokens(ctx context.Context, a OVNNICCleanup, tokens map[int64]string, capture bool) error {
	if a.SourceNodeID != c.nodeID || len(tokens) != len(a.NetworkIDs) {
		return api.StatusErrorf(http.StatusConflict, "OVN NIC cleanup source or reservation set changed")
	}

	for _, id := range a.NetworkIDs {
		token := tokens[id]
		if token == "" {
			return api.StatusErrorf(http.StatusConflict, "OVN NIC cleanup network reservation is missing")
		}

		var operation string
		err := c.tx.QueryRowContext(ctx, `SELECT op.operation FROM networks n JOIN networks_ovn_operations op ON op.project_id=n.project_id AND op.name=n.name WHERE n.id=? AND n.type=? AND op.token=?`, id, NetworkTypeOVN, token).Scan(&operation)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return api.StatusErrorf(http.StatusConflict, "OVN NIC cleanup network identity or reservation changed")
			}

			return err
		}

		if operation != "nic" && (capture || operation != "prepare") {
			return api.StatusErrorf(http.StatusConflict, "OVN NIC cleanup reservation does not authorize this action")
		}
	}

	return nil
}

// OVNNICCleanupReservation identifies a numeric network reserved for an original cleanup attempt.
// Inherited tokens belong to the enclosing caller and must not be released by a nested cleanup.
type OVNNICCleanupReservation struct {
	NetworkID int64
	Project   string
	Name      string
	Token     string
	Inherited bool
}

// AcquireOVNNICCleanupOperations reserves the finite original network/owner/peer set atomically.
// The caller must return an error from its enclosing Cluster.Transaction; partial acquisitions must never be committed.
// Resolve aliases by numeric identity so a renamed network cannot turn a retry into name-reuse cleanup.
func (c *ClusterTx) AcquireOVNNICCleanupOperations(ctx context.Context, networkIDs []int64, inherited map[int64]string) ([]OVNNICCleanupReservation, error) {
	if len(networkIDs) == 0 || !slices.IsSorted(networkIDs) {
		return nil, errors.New("OVN NIC cleanup requires an ordered original network set")
	}

	reservations := make([]OVNNICCleanupReservation, 0, len(networkIDs))
	for i, id := range networkIDs {
		if id <= 0 || (i > 0 && id == networkIDs[i-1]) {
			return nil, errors.New("Invalid original OVN NIC cleanup network set")
		}

		reservation := OVNNICCleanupReservation{NetworkID: id}
		err := c.tx.QueryRowContext(ctx, `SELECT project.name, network.name FROM networks network JOIN projects project ON project.id=network.project_id WHERE network.id=? AND network.type=?`, id, NetworkTypeOVN).Scan(&reservation.Project, &reservation.Name)
		if err != nil {
			return nil, err
		}

		{
			token, exists := inherited[id]
			if exists {
				if token == "" {
					return nil, api.StatusErrorf(http.StatusConflict, "Inherited OVN NIC cleanup reservation is empty")
				}

				var operation string
				err = c.tx.QueryRowContext(ctx, `SELECT operation FROM networks_ovn_operations WHERE project_id=(SELECT id FROM projects WHERE name=?) AND name=? AND token=?`, reservation.Project, reservation.Name, token).Scan(&operation)
				if err != nil {
					return nil, err
				}

				if operation != "nic" && operation != "prepare" {
					return nil, api.StatusErrorf(http.StatusConflict, "Inherited OVN NIC cleanup operation is not authorized")
				}

				reservation.Token = token
				reservation.Inherited = true
			} else {
				reservation.Token = uuid.NewString()
			}
		}

		reservations = append(reservations, reservation)
	}

	for id := range inherited {
		if !slices.Contains(networkIDs, id) {
			return nil, errors.New("Inherited OVN NIC cleanup reservation is outside the original network set")
		}
	}

	for _, reservation := range reservations {
		if reservation.Inherited {
			continue
		}

		err := c.AcquireOVNNetworkOperation(ctx, reservation.Project, reservation.Name, reservation.Token, "nic")
		if err != nil {
			return nil, err
		}
	}

	return reservations, nil
}

// ReleaseOVNNICCleanupOperations releases only newly acquired tokens, never the enclosing owner's tokens.
func (c *ClusterTx) ReleaseOVNNICCleanupOperations(ctx context.Context, reservations []OVNNICCleanupReservation) error {
	for _, reservation := range reservations {
		if reservation.Inherited {
			continue
		}

		err := c.ReleaseOVNNetworkOperation(ctx, reservation.Project, reservation.Name, reservation.Token)
		if err != nil {
			return err
		}
	}

	return nil
}

// CaptureOVNNICCleanup acknowledges an immutable attempt before source identity can be lost.
// Retry must use the same generation and exact inputs, including after an uncertain commit.
// This records inputs only; it does not certify their backend ownership or successful effects.
func (c *ClusterTx) CaptureOVNNICCleanup(ctx context.Context, a OVNNICCleanup, tokens map[int64]string) error {
	err := a.validate()
	if err != nil {
		return err
	}

	if a.Completed {
		return errors.New("A new OVN NIC cleanup capture cannot be completed")
	}

	err = c.validateOVNNICCleanupTokens(ctx, a, tokens, true)
	if err != nil {
		return err
	}

	existing, err := c.OVNNICCleanupByGeneration(ctx, a.Generation)
	if err == nil {
		if !a.matches(existing) {
			return api.StatusErrorf(http.StatusConflict, "OVN NIC cleanup capture identity changed")
		}

		return nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var memberState int
	err = c.tx.QueryRowContext(ctx, "SELECT state FROM nodes WHERE id=?", c.nodeID).Scan(&memberState)
	if err != nil {
		return err
	}

	// A restoring member starts workloads, so a failed start must be able to capture its own NIC cleanup.
	if memberState != ClusterMemberStateCreated && memberState != ClusterMemberStateEvacuating && memberState != ClusterMemberStateRestoring {
		return api.StatusErrorf(http.StatusConflict, "OVN NIC cleanup cannot capture an inactive source member")
	}

	for _, id := range a.NetworkIDs {
		var prepared bool
		err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_nodes WHERE node_id=? AND network_id=? AND state IN (?,?))`, c.nodeID, id, networkPreparing, networkPrepared).Scan(&prepared)
		if err != nil {
			return err
		}

		if prepared {
			return api.StatusErrorf(http.StatusConflict, "OVN NIC cleanup cannot enter an already prepared network")
		}
	}

	networks, err := json.Marshal(a.NetworkIDs)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `INSERT INTO networks_ovn_nic_cleanup (generation, source_node_id, instance_uuid, device_name, version, network_ids, payload) VALUES (?,?,?,?,?,?,?)`, a.Generation, a.SourceNodeID, a.InstanceUUID, a.DeviceName, a.Version, string(networks), a.Payload)
	if err != nil {
		return err
	}

	for _, id := range a.NetworkIDs {
		_, err := c.tx.ExecContext(ctx, `INSERT INTO networks_ovn_nic_cleanup_networks (generation, source_node_id, network_id) VALUES (?,?,?)`, a.Generation, a.SourceNodeID, id)
		if err != nil {
			return err
		}
	}

	return nil
}

// CompleteOVNNICCleanup acknowledges only the exact original attempt, after all required effects succeed.
// The tombstone is retained so a repeated matching completion is distinguishable from missing state.
func (c *ClusterTx) CompleteOVNNICCleanup(ctx context.Context, a OVNNICCleanup, tokens map[int64]string) error {
	err := a.validate()
	if err != nil {
		return err
	}

	if a.SourceNodeID != c.nodeID {
		return api.StatusErrorf(http.StatusConflict, "OVN NIC cleanup belongs to another source member")
	}

	existing, err := c.OVNNICCleanupByGeneration(ctx, a.Generation)
	if err != nil {
		return err
	}

	if !a.matches(existing) {
		return api.StatusErrorf(http.StatusConflict, "OVN NIC cleanup completion identity changed")
	}

	if existing.Completed {
		return nil
	}

	err = c.validateOVNNICCleanupTokens(ctx, a, tokens, false)
	if err != nil {
		return err
	}

	result, err := c.tx.ExecContext(ctx, `UPDATE networks_ovn_nic_cleanup SET completed=1 WHERE generation=? AND source_node_id=? AND completed=0`, a.Generation, c.nodeID)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if count != 1 {
		return api.StatusErrorf(http.StatusConflict, "OVN NIC cleanup completion changed")
	}

	_, err = c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_nic_cleanup_networks WHERE generation=? AND source_node_id=?`, a.Generation, c.nodeID)
	return err
}

// EnsureOVNNICCleanupComplete prevents preparation acknowledging any original local cleanup debt for this network.
// Call in the same transaction as a preparation/deletion transition, under its network reservation.
func (c *ClusterTx) EnsureOVNNICCleanupComplete(ctx context.Context, networkID int64) error {
	{
		err := c.ensureOVNNICMigrationComplete(ctx, networkID, "")
		if err != nil {
			return err
		}
	}

	var pending bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_nic_cleanup_networks WHERE source_node_id=? AND network_id=?)`, c.nodeID, networkID).Scan(&pending)
	if err != nil {
		return err
	}

	if pending {
		return api.StatusErrorf(http.StatusConflict, "OVN network has unacknowledged source NIC cleanup")
	}

	return c.ensureOVNNICClaimsComplete(ctx, networkID, "")
}

// EnsureOVNNICCleanupCompleteForInstance also checks captured partial-Start claims.
func (c *ClusterTx) EnsureOVNNICCleanupCompleteForInstance(ctx context.Context, instanceUUID string) error {
	{
		err := c.ensureOVNNICMigrationComplete(ctx, 0, instanceUUID)
		if err != nil {
			return err
		}
	}

	err := c.EnsureOVNNICCleanupDebtCompleteForInstance(ctx, instanceUUID)
	if err != nil {
		return err
	}

	return c.ensureOVNNICClaimsComplete(ctx, 0, instanceUUID)
}

// EnsureOVNNICTransferSource requires that no member has unacknowledged NIC cleanup or migration
// work for the instance before a cold move hands its port producer to a new member.
func (c *ClusterTx) EnsureOVNNICTransferSource(ctx context.Context, instanceUUID string) error {
	err := c.ensureOVNNICMigrationComplete(ctx, 0, instanceUUID)
	if err != nil {
		return err
	}

	var pending bool
	err = c.tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM networks_ovn_nic_cleanup WHERE instance_uuid=? AND completed=0)", instanceUUID).Scan(&pending)
	if err != nil {
		return err
	}

	if pending {
		return api.StatusErrorf(http.StatusConflict, "Instance has unacknowledged OVN NIC cleanup on its previous member")
	}

	return nil
}

// EnsureOVNNetworkCleanupFree refuses deleting a network while any member still has unacknowledged
// NIC cleanup, an unresolved NIC migration or an outstanding NIC claim on it, before any effect.
func (c *ClusterTx) EnsureOVNNetworkCleanupFree(ctx context.Context, networkID int64) error {
	var pending bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_nic_cleanup_networks WHERE network_id=?)`, networkID).Scan(&pending)
	if err != nil {
		return err
	}

	if pending {
		return api.StatusErrorf(http.StatusConflict, "OVN network has unacknowledged NIC cleanup on a cluster member")
	}

	err = c.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM networks_ovn_nic_migrations m JOIN networks_ovn_nic_migration_devices d ON d.operation=m.operation JOIN networks_ovn_nic_cleanup a ON a.generation=d.generation WHERE m.phase NOT IN ('aborted','placed') AND EXISTS(SELECT 1 FROM json_each(a.network_ids) WHERE value=?))`, networkID).Scan(&pending)
	if err != nil {
		return err
	}

	if pending {
		return api.StatusErrorf(http.StatusConflict, "OVN network has an unresolved NIC migration")
	}

	return c.ensureOVNNICClaimsComplete(ctx, networkID, "")
}

// EnsureOVNNICCleanupDebtCompleteForInstance gates publication without acknowledging live claims.
func (c *ClusterTx) EnsureOVNNICCleanupDebtCompleteForInstance(ctx context.Context, instanceUUID string) error {
	var pending bool
	err := c.tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM networks_ovn_nic_cleanup WHERE source_node_id=? AND instance_uuid=? AND completed=0)", c.nodeID, instanceUUID).Scan(&pending)
	if err != nil {
		return err
	}

	if pending {
		return api.StatusErrorf(http.StatusConflict, "Instance has unacknowledged source OVN NIC cleanup")
	}

	return nil
}

// EnsureOVNNICCleanupStart refuses old claims or verifies this producer's exact durable claim.
func (c *ClusterTx) EnsureOVNNICCleanupStart(ctx context.Context, instanceUUID, deviceName string, networkID int64, producer map[string]string) error {
	err := c.EnsureOVNNICCleanupDebtCompleteForInstance(ctx, instanceUUID)
	if err != nil {
		return err
	}

	for _, field := range []string{"last_state.ovn.host", "last_state.ovn.physical"} {
		var current string
		err = c.tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT v.value FROM instances_config v JOIN instances_config u ON u.instance_id=v.instance_id AND u.key='volatile.uuid' JOIN instances i ON i.id=v.instance_id WHERE u.value=? AND i.node_id=? AND v.key=?),'')", instanceUUID, c.nodeID, "volatile."+deviceName+"."+field).Scan(&current)
		if err != nil {
			return err
		}

		if producer == nil {
			if current != "" {
				return errors.New("Original NIC claim remains quarantined before a new allocation")
			}

			continue
		}

		if current != producer[field] {
			return errors.New("NIC producer durable allocation changed before publication")
		}

		if current == "" {
			continue
		}

		var claim struct {
			Version      int
			NetworkID    int64
			SourceNodeID int64
			InstanceUUID string
			InstanceID   int
			DeviceName   string
		}

		err = json.Unmarshal([]byte(current), &claim)
		if err != nil || claim.Version != 1 || claim.NetworkID != networkID || claim.SourceNodeID != c.nodeID || claim.InstanceUUID != instanceUUID || claim.DeviceName != deviceName {
			return errors.New("NIC producer claim does not bind original network/source/device")
		}

		err = c.ensureOVNNICPreclaimInstance(ctx, claim.InstanceID, instanceUUID)
		if err != nil {
			return err
		}
	}

	return nil
}

// ensureOVNNICClaimsComplete checks finite persisted producer claims, independent of Desired.
func (c *ClusterTx) ensureOVNNICClaimsComplete(ctx context.Context, networkID int64, instanceUUID string) error {
	rows, err := c.tx.QueryContext(ctx, `SELECT v.key,v.value,i.id,i.node_id,COALESCE(u.value,'') FROM instances_config v
		JOIN instances i ON i.id=v.instance_id
		LEFT JOIN instances_config u ON u.instance_id=i.id AND u.key='volatile.uuid'
		WHERE (v.key GLOB 'volatile.*.last_state.ovn.physical' OR v.key GLOB 'volatile.*.last_state.ovn.host')
		AND v.value<>'' AND (?='' OR u.value=?)`, instanceUUID, instanceUUID)
	if err != nil {
		return err
	}

	type storedClaim struct {
		key, raw, instanceUUID string
		instanceID             int
		nodeID                 int64
	}

	var claims []storedClaim
	for rows.Next() {
		var entry storedClaim
		err = rows.Scan(&entry.key, &entry.raw, &entry.instanceID, &entry.nodeID, &entry.instanceUUID)
		if err != nil {
			_ = rows.Close()
			return err
		}

		claims = append(claims, entry)
	}

	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}

	for _, entry := range claims {
		raw := entry.raw
		var claim struct {
			Version      int
			NetworkID    int64
			SourceNodeID int64
			InstanceUUID string
			InstanceID   int
			DeviceName   string
		}

		err = json.Unmarshal([]byte(raw), &claim)
		// A malformed envelope cannot implicate an unrelated record or member.
		if entry.nodeID != c.nodeID && (err != nil || claim.SourceNodeID != c.nodeID) {
			continue
		}

		if err == nil && claim.SourceNodeID > 0 && claim.SourceNodeID != c.nodeID {
			continue
		}

		if err == nil && networkID > 0 && claim.NetworkID > 0 && claim.NetworkID != networkID {
			continue
		}

		if err != nil || claim.Version != 1 || claim.NetworkID <= 0 || claim.SourceNodeID <= 0 || claim.InstanceUUID == "" || claim.InstanceID <= 0 || claim.DeviceName == "" {
			return api.StatusErrorf(http.StatusConflict, "Original NIC claim is invalid; cleanup cannot be acknowledged")
		}

		field := strings.TrimPrefix(entry.key, "volatile."+claim.DeviceName+".")
		if field != "last_state.ovn.host" && field != "last_state.ovn.physical" {
			return errors.New("Original NIC claim device envelope changed")
		}

		completed, err := c.tx.QueryContext(ctx, "SELECT payload FROM networks_ovn_nic_cleanup WHERE source_node_id=? AND instance_uuid=? AND device_name=? AND completed=1", claim.SourceNodeID, claim.InstanceUUID, claim.DeviceName)
		if err != nil {
			return err
		}

		var acknowledged bool
		for completed.Next() {
			var payload string
			err = completed.Scan(&payload)
			if err != nil {
				_ = completed.Close()
				return err
			}

			var source struct {
				InstanceID   int
				HostVolatile map[string]string
			}

			err = json.Unmarshal([]byte(payload), &source)
			if err != nil {
				_ = completed.Close()
				return err
			}

			if source.InstanceID == claim.InstanceID && source.HostVolatile[field] == raw {
				acknowledged = true
			}
		}

		err = errors.Join(completed.Err(), completed.Close())
		if err != nil {
			return err
		}

		if !acknowledged {
			return api.StatusErrorf(http.StatusConflict, "Original NIC allocation remains quarantined without full cleanup acknowledgment")
		}
	}

	return nil
}

// OVNNICStopVolatileKeys is the existing Stop allocation set. IP/DNS/source
// inputs in the immutable cleanup payload are retained until full acknowledgment.
func OVNNICStopVolatileKeys() []string {
	return []string{
		"host_name", "last_state.ovn.host", "last_state.ovn.physical", "last_state.hwaddr", "last_state.mtu", "last_state.created",
		"last_state.vdpa.name", "last_state.vf.parent", "last_state.vf.id",
		"last_state.vf.hwaddr", "last_state.vf.vlan", "last_state.vf.spoofcheck", "last_state.vf.trusted", "last_state.pci.driver",
	}
}

// OVNNICHasOriginalInstance identifies an older record whose OVN identity a new copy inherited.
func (c *ClusterTx) OVNNICHasOriginalInstance(ctx context.Context, instanceID int, instanceUUID string) (bool, error) {
	var original bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM instances_config WHERE key='volatile.uuid' AND value=? AND instance_id<?)`, instanceUUID, instanceID).Scan(&original)
	return original, err
}

// EnsureOVNNICOriginalInstance admits only the local original and newer copies without OVN allocations.
func (c *ClusterTx) EnsureOVNNICOriginalInstance(ctx context.Context, instanceID int, identity string) error {
	var nodeID int64
	var currentIdentity string
	err := c.tx.QueryRowContext(ctx, "SELECT i.node_id,COALESCE(v.value,'') FROM instances i LEFT JOIN instances_config v ON v.instance_id=i.id AND v.key='volatile.uuid' WHERE i.id=?", instanceID).Scan(&nodeID, &currentIdentity)
	if err != nil {
		return err
	}

	if nodeID != c.nodeID || currentIdentity != identity || identity == "" {
		return api.StatusErrorf(http.StatusConflict, "Original NIC source placement or UUID owner changed")
	}

	var owners int
	err = c.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM instances_config WHERE key='volatile.uuid' AND value=?", identity).Scan(&owners)
	if err != nil {
		return err
	}

	if owners == 1 {
		return nil
	}

	original, err := c.OVNNICHasOriginalInstance(ctx, instanceID, identity)
	if err != nil {
		return err
	}

	if original {
		return api.StatusErrorf(http.StatusConflict, "Original NIC instance UUID owner is ambiguous")
	}

	var staged bool
	err = c.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM networks_ovn_nic_migrations WHERE instance_uuid=? AND phase IN ('authorized','handover'))", identity).Scan(&staged)
	if err != nil {
		return err
	}

	if staged {
		return api.StatusErrorf(http.StatusConflict, "Original NIC instance UUID owner is ambiguous")
	}

	rows, err := c.tx.QueryContext(ctx, "SELECT operation FROM networks_ovn_nic_migrations WHERE instance_uuid=? AND instance_id=? AND source_node_id=? AND phase='placed'", identity, instanceID, c.nodeID)
	if err != nil {
		return err
	}

	var historical []string
	for rows.Next() {
		var operation string
		err = rows.Scan(&operation)
		if err != nil {
			break
		}

		historical = append(historical, operation)
	}

	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}

	for _, operation := range historical {
		complete, err := c.ovnNICCompletedPlaced(ctx, operation, instanceID, identity)
		if err != nil {
			return err
		}

		if !complete {
			return api.StatusErrorf(http.StatusConflict, "Original NIC migration source terminal cleanup is unresolved")
		}
	}

	// Newer pre-copy records defer Add/Remove and Start while this original exists.
	devices := map[string]bool{}
	rows, err = c.tx.QueryContext(ctx, "SELECT device_name FROM networks_ovn_nic_cleanup WHERE source_node_id=? AND instance_uuid=? AND completed=0", c.nodeID, identity)
	if err != nil {
		return err
	}

	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			_ = rows.Close()
			return err
		}

		devices[name] = true
	}

	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}

	rows, err = c.tx.QueryContext(ctx, "SELECT key FROM instances_config WHERE instance_id=? AND value<>'' AND (key GLOB 'volatile.*.last_state.ovn.host' OR key GLOB 'volatile.*.last_state.ovn.physical')", instanceID)
	if err != nil {
		return err
	}

	for rows.Next() {
		var key string
		err = rows.Scan(&key)
		if err != nil {
			_ = rows.Close()
			return err
		}

		for _, suffix := range []string{".last_state.ovn.host", ".last_state.ovn.physical"} {
			if strings.HasSuffix(key, suffix) {
				devices[strings.TrimSuffix(strings.TrimPrefix(key, "volatile."), suffix)] = true
			}
		}
	}

	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}

	predicates := []string{"v.key GLOB ?", "v.key GLOB ?"}
	args := []any{identity, instanceID, "volatile.*.last_state.ovn.host", "volatile.*.last_state.ovn.physical"}
	for name := range devices {
		for _, field := range OVNNICStopVolatileKeys() {
			predicates = append(predicates, "v.key=?")
			args = append(args, "volatile."+name+"."+field)
		}
	}

	var allocated bool
	err = c.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM instances_config v
		JOIN instances_config u ON u.instance_id=v.instance_id AND u.key='volatile.uuid'
		WHERE u.value=? AND v.instance_id<>? AND v.value<>'' AND (`+strings.Join(predicates, " OR ")+"))", args...).Scan(&allocated)
	if err != nil {
		return err
	}

	if allocated {
		return api.StatusErrorf(http.StatusConflict, "Copied instance retains an original OVN NIC host allocation")
	}

	return nil
}

// RetireOVNNICCleanupVolatile retires only matching original local allocations
// and records that decision in the same transaction. It never acknowledges
// backend work, removes pending network links or touches moved/replaced metadata.
func (c *ClusterTx) RetireOVNNICCleanupVolatile(ctx context.Context, a OVNNICCleanup, tokens map[int64]string) (bool, error) {
	err := a.validate()
	if err != nil {
		return false, err
	}

	if a.SourceNodeID != c.nodeID {
		return false, api.StatusErrorf(http.StatusConflict, "NIC volatile retirement belongs to another source member")
	}

	existing, err := c.OVNNICCleanupByGeneration(ctx, a.Generation)
	if err != nil {
		return false, err
	}

	if !a.matches(existing) || existing.Completed {
		return false, api.StatusErrorf(http.StatusConflict, "NIC volatile retirement attempt changed or is no longer pending")
	}

	err = c.validateOVNNICCleanupTokens(ctx, existing, tokens, false)
	if err != nil {
		return false, err
	}

	var source struct {
		Kind         string
		Version      int
		InstanceID   int64
		HostVolatile map[string]string
	}

	err = json.Unmarshal([]byte(existing.Payload), &source)
	if err != nil || source.Kind != "incus-ovn-nic-stop" || source.Version != 1 || source.InstanceID <= 0 {
		return false, errors.New("Original NIC volatile retirement snapshot is unsupported")
	}

	var recordedClear bool
	err = c.tx.QueryRowContext(ctx, "SELECT cleared_source FROM networks_ovn_nic_cleanup_retirement WHERE generation=?", a.Generation).Scan(&recordedClear)
	alreadyRetired := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// A prior no-clear decision must not erase allocations after a later move back.
	if alreadyRetired && !recordedClear {
		return false, nil
	}

	var nodeID int64
	var instanceUUID string
	err = c.tx.QueryRowContext(ctx, "SELECT i.node_id, COALESCE(v.value,'') FROM instances i LEFT JOIN instances_config v ON v.instance_id=i.id AND v.key='volatile.uuid' WHERE i.id=?", source.InstanceID).Scan(&nodeID, &instanceUUID)
	localSource := err == nil && nodeID == c.nodeID && instanceUUID == a.InstanceUUID
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	transfer, err := c.OVNNICMigrationForCleanup(ctx, a.Generation)
	if err != nil {
		return false, err
	}

	if transfer != nil {
		localSource = false
	}

	if localSource {
		err = c.EnsureOVNNICOriginalInstance(ctx, int(source.InstanceID), a.InstanceUUID)
		if err != nil {
			return false, err
		}

		for _, key := range OVNNICStopVolatileKeys() {
			var current string
			err := c.tx.QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key=?", source.InstanceID, "volatile."+a.DeviceName+"."+key).Scan(&current)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return false, err
			}

			expected := source.HostVolatile[key]
			if alreadyRetired {
				expected = ""
			}

			if current != expected {
				return false, api.StatusErrorf(http.StatusConflict, "Original NIC volatile allocation %q changed; refusing replacement", key)
			}
		}
	}

	if alreadyRetired {
		return localSource, nil
	}

	if localSource {
		for _, key := range OVNNICStopVolatileKeys() {
			_, err = c.tx.ExecContext(ctx, "DELETE FROM instances_config WHERE instance_id=? AND key=?", source.InstanceID, "volatile."+a.DeviceName+"."+key)
			if err != nil {
				return false, err
			}
		}
	}

	_, err = c.tx.ExecContext(ctx, "INSERT INTO networks_ovn_nic_cleanup_retirement (generation, cleared_source) VALUES (?,?)", a.Generation, localSource)
	return localSource, err
}

// EnsureOVNNICCleanupRetired requires the exact durable successful NIC-hook receipt.
// Missing receipt cannot be initialized by a terminal driver acknowledgment.
func (c *ClusterTx) EnsureOVNNICCleanupRetired(ctx context.Context, a OVNNICCleanup) error {
	existing, err := c.OVNNICCleanupByGeneration(ctx, a.Generation)
	if err != nil {
		return err
	}

	if !a.matches(existing) || a.SourceNodeID != c.nodeID {
		return errors.New("Original NIC terminal retirement identity changed")
	}

	var retired bool
	err = c.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM networks_ovn_nic_cleanup_retirement WHERE generation=?)", a.Generation).Scan(&retired)
	if err != nil {
		return err
	}

	if !retired {
		return errors.New("Original NIC effects lack durable successful host-hook retirement")
	}

	return nil
}

// RetireOVNNICPreclaim clears exact metadata after same-process, pre-backend restore.
// The caller must hold allocation ownership and positively restore/release the original marker.
func (c *ClusterTx) RetireOVNNICPreclaim(ctx context.Context, instanceID int, instanceUUID, deviceName string, original map[string]string) error {
	var claim struct {
		Version      int
		NetworkID    int64
		SourceNodeID int64
		InstanceUUID string
		InstanceID   int
		DeviceName   string
	}

	field := "last_state.ovn.physical"
	if original["last_state.ovn.host"] != "" {
		field = "last_state.ovn.host"
	}

	err := json.Unmarshal([]byte(original[field]), &claim)
	if err != nil || claim.Version != 1 || claim.NetworkID <= 0 || claim.SourceNodeID != c.nodeID || claim.InstanceUUID != instanceUUID || claim.InstanceID != instanceID || claim.DeviceName != deviceName {
		return errors.New("Original NIC preclaim retirement identity is invalid")
	}

	err = c.ensureOVNNICPreclaimInstance(ctx, instanceID, instanceUUID)
	if err != nil {
		return err
	}

	var pending bool
	err = c.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM networks_ovn_nic_cleanup WHERE source_node_id=? AND instance_uuid=? AND device_name=? AND completed=0)", c.nodeID, instanceUUID, deviceName).Scan(&pending)
	if err != nil {
		return err
	}

	if pending {
		return errors.New("Published NIC source debt cannot retire as a pre-backend claim")
	}

	for _, key := range OVNNICStopVolatileKeys() {
		var current string
		err = c.tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key=?),'')", instanceID, "volatile."+deviceName+"."+key).Scan(&current)
		if err != nil {
			return err
		}

		if current != original[key] {
			return api.StatusErrorf(http.StatusConflict, "Original NIC preclaim volatile %q changed; preserving replacement", key)
		}
	}

	for _, key := range OVNNICStopVolatileKeys() {
		_, err = c.tx.ExecContext(ctx, "DELETE FROM instances_config WHERE instance_id=? AND key=?", instanceID, "volatile."+deviceName+"."+key)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *ClusterTx) ensureOVNNICPreclaimInstance(ctx context.Context, instanceID int, instanceUUID string) error {
	return c.EnsureOVNNICOriginalInstance(ctx, instanceID, instanceUUID)
}

// EnsureOVNNICPreclaimRetired reconciles only absent metadata after a proved restore.
func (c *ClusterTx) EnsureOVNNICPreclaimRetired(ctx context.Context, instanceID int, instanceUUID, deviceName string) error {
	err := c.ensureOVNNICPreclaimInstance(ctx, instanceID, instanceUUID)
	if err != nil {
		return err
	}

	for _, key := range OVNNICStopVolatileKeys() {
		var current string
		err = c.tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key=?),'')", instanceID, "volatile."+deviceName+"."+key).Scan(&current)
		if err != nil {
			return err
		}

		if current != "" {
			return errors.New("Original NIC preclaim retirement remains unacknowledged or changed")
		}
	}

	return nil
}

// OVNNICMigration is finite authorization for one dispatched same-name live move.
// Target allocations stay separate from the source instance config until placement.
type OVNNICMigration struct {
	Operation      string
	InstanceID     int
	ProjectID      int64
	InstanceUUID   string
	SourceNodeID   int64
	TargetNodeID   int64
	Phase          string
	DeviceName     string
	Source         OVNNICCleanup
	TargetVolatile map[string]string
	SharedPlan     string
	TargetOVS      string
	OVSAttempted   bool
	Ready          bool
	SourceTerminal string
}

// AuthorizeOVNNICMigration binds all captured source NICs before target dispatch.
func (c *ClusterTx) AuthorizeOVNNICMigration(ctx context.Context, operation string, instanceID int, instanceUUID string, targetNodeID int64) error {
	id, err := uuid.Parse(operation)
	if err != nil || id == uuid.Nil || id.String() != operation || targetNodeID <= 0 || targetNodeID == c.nodeID {
		return errors.New("Invalid NIC migration operation or target")
	}

	err = c.ensureOVNNICPreclaimInstance(ctx, instanceID, instanceUUID)
	if err != nil {
		return err
	}

	attempts, err := c.OVNNICCleanups(ctx)
	if err != nil {
		return err
	}

	var projectID int64
	err = c.tx.QueryRowContext(ctx, "SELECT project_id FROM instances WHERE id=?", instanceID).Scan(&projectID)
	if err != nil {
		return err
	}

	selected := []OVNNICCleanup{}
	for _, a := range attempts {
		if a.InstanceUUID != instanceUUID {
			continue
		}

		var source struct {
			InstanceID   int
			HostVolatile map[string]string
		}

		err = json.Unmarshal([]byte(a.Payload), &source)
		if err != nil || source.InstanceID != instanceID {
			return errors.New("Migration source capture changed")
		}

		err = c.ovnNICVolatileEqual(ctx, instanceID, a.DeviceName, source.HostVolatile)
		if err != nil {
			return err
		}

		selected = append(selected, a)
	}

	if len(selected) == 0 {
		return nil
	}

	var targetSchema, targetState int
	err = c.tx.QueryRowContext(ctx, "SELECT schema,state FROM nodes WHERE id=?", targetNodeID).Scan(&targetSchema, &targetState)
	if err != nil {
		return err
	}

	// Restore brings workloads back to a member whose networks it has already restored.
	if targetSchema < 85 || (targetState != ClusterMemberStateCreated && targetState != ClusterMemberStateRestoring) {
		return errors.New("OVN live migration target must advertise schema85 staging on an active or restoring member")
	}
	// Only a positively empty, never-published old dispatch can be replaced.
	_, err = c.tx.ExecContext(ctx, `UPDATE networks_ovn_nic_migrations SET phase='aborted' WHERE instance_uuid=? AND phase='authorized' AND operation<>? AND NOT EXISTS(SELECT 1 FROM networks_ovn_nic_migration_devices d WHERE d.operation=networks_ovn_nic_migrations.operation AND (d.target_volatile<>'{}' OR d.shared_plan<>'' OR d.ovs_attempted<>0 OR d.target_ovs<>'' OR d.ready<>0))`, instanceUUID, operation)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `INSERT INTO networks_ovn_nic_migrations(operation,project_id,instance_id,instance_uuid,source_node_id,target_node_id,phase) VALUES (?,?,?,?,?,?,'authorized') ON CONFLICT(operation) DO NOTHING`, operation, projectID, instanceID, instanceUUID, c.nodeID, targetNodeID)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_nic_migration_members WHERE operation IN (SELECT operation FROM networks_ovn_nic_migrations WHERE instance_uuid=? AND phase='aborted')`, instanceUUID)
	if err != nil {
		return err
	}

	for _, node := range []int64{c.nodeID, targetNodeID} {
		_, err = c.tx.ExecContext(ctx, "INSERT INTO networks_ovn_nic_migration_members(operation,node_id) VALUES (?,?) ON CONFLICT(operation,node_id) DO NOTHING", operation, node)
		if err != nil {
			return err
		}
	}

	for _, a := range selected {
		_, err = c.tx.ExecContext(ctx, `INSERT INTO networks_ovn_nic_migration_devices(operation,device_name,generation) VALUES (?,?,?) ON CONFLICT(operation,device_name) DO NOTHING`, operation, a.DeviceName, a.Generation)
		if err != nil {
			return err
		}

		m, err := c.OVNNICMigrationDevice(ctx, operation, a.DeviceName)
		if err != nil {
			return err
		}

		if m.InstanceID != instanceID || m.ProjectID != projectID || m.InstanceUUID != instanceUUID || m.SourceNodeID != c.nodeID || m.TargetNodeID != targetNodeID || m.Phase != "authorized" || !m.Source.matches(a) {
			return errors.New("NIC migration authorization changed")
		}
	}

	return nil
}

func (c *ClusterTx) ovnNICVolatileEqual(ctx context.Context, instanceID int, deviceName string, expected map[string]string) error {
	for _, key := range OVNNICStopVolatileKeys() {
		var value string
		err := c.tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key=?),'')", instanceID, "volatile."+deviceName+"."+key).Scan(&value)
		if err != nil {
			return err
		}

		if value != expected[key] {
			return api.StatusErrorf(http.StatusConflict, "NIC allocation %q changed", key)
		}
	}

	return nil
}

// OVNNICMigrationDevice reads exact durable capability, without granting admission.
func (c *ClusterTx) OVNNICMigrationDevice(ctx context.Context, operation, deviceName string) (OVNNICMigration, error) {
	m := OVNNICMigration{Operation: operation, DeviceName: deviceName}
	var generation, target string
	err := c.tx.QueryRowContext(ctx, `SELECT m.instance_id,m.project_id,m.instance_uuid,m.source_node_id,m.target_node_id,m.phase,m.source_terminal,d.generation,d.target_volatile,d.shared_plan,d.target_ovs,d.ovs_attempted,d.ready FROM networks_ovn_nic_migrations m JOIN networks_ovn_nic_migration_devices d ON d.operation=m.operation WHERE m.operation=? AND d.device_name=?`, operation, deviceName).Scan(&m.InstanceID, &m.ProjectID, &m.InstanceUUID, &m.SourceNodeID, &m.TargetNodeID, &m.Phase, &m.SourceTerminal, &generation, &target, &m.SharedPlan, &m.TargetOVS, &m.OVSAttempted, &m.Ready)
	if err != nil {
		return m, err
	}

	err = json.Unmarshal([]byte(target), &m.TargetVolatile)
	if err != nil || m.TargetVolatile == nil {
		return m, errors.New("Invalid staged target allocation")
	}

	m.Source, err = c.OVNNICCleanupByGeneration(ctx, generation)
	if err != nil {
		return m, err
	}

	if m.Source.SourceNodeID != m.SourceNodeID || m.Source.InstanceUUID != m.InstanceUUID || m.Source.DeviceName != deviceName {
		return m, errors.New("Migration original source binding changed")
	}

	return m, nil
}

func (c *ClusterTx) ovnNICMigrationInstance(ctx context.Context, m OVNNICMigration) error {
	var node, projectID int64
	var identity string
	var owners int
	err := c.tx.QueryRowContext(ctx, `SELECT i.node_id,i.project_id,COALESCE(v.value,'') FROM instances i LEFT JOIN instances_config v ON v.instance_id=i.id AND v.key='volatile.uuid' WHERE i.id=?`, m.InstanceID).Scan(&node, &projectID, &identity)
	if err != nil {
		return err
	}

	err = c.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM instances_config WHERE key='volatile.uuid' AND value=?", m.InstanceUUID).Scan(&owners)
	if err != nil {
		return err
	}

	if owners != 1 || identity != m.InstanceUUID || node != m.SourceNodeID || projectID != m.ProjectID {
		return errors.New("Migration original instance placement changed")
	}

	var source struct{ HostVolatile map[string]string }
	err = json.Unmarshal([]byte(m.Source.Payload), &source)
	if err != nil {
		return err
	}

	return c.ovnNICVolatileEqual(ctx, m.InstanceID, m.DeviceName, source.HostVolatile)
}

// EnsureOVNNICMigrationStart validates target admission against the staged claim.
func (c *ClusterTx) EnsureOVNNICMigrationStart(ctx context.Context, operation string, instanceID int, instanceUUID, deviceName string, networkID int64, producer map[string]string) error {
	m, err := c.OVNNICMigrationDevice(ctx, operation, deviceName)
	if err != nil {
		return err
	}

	if m.TargetNodeID != c.nodeID || m.InstanceID != instanceID || m.InstanceUUID != instanceUUID || m.Phase != "authorized" || m.Source.Completed || (networkID > 0 && !slices.Contains(m.Source.NetworkIDs, networkID)) {
		return errors.New("NIC target migration capability changed")
	}

	err = c.ovnNICMigrationInstance(ctx, m)
	if err != nil {
		return err
	}

	if producer == nil {
		if len(m.TargetVolatile) > 0 || m.SharedPlan != "" || m.OVSAttempted || m.TargetOVS != "" || m.Ready {
			return errors.New("Migration target retains an allocation; refusing another Start")
		}

		return nil
	}

	for _, key := range OVNNICStopVolatileKeys() {
		if m.TargetVolatile[key] != producer[key] {
			return errors.New("Migration target producer differs from durable stage")
		}
	}

	return validateOVNNICMigrationClaim(m, producer, networkID)
}

func validateOVNNICMigrationClaim(m OVNNICMigration, producer map[string]string, networkID int64) error {
	field := "last_state.ovn.host"
	if producer[field] == "" {
		field = "last_state.ovn.physical"
	}

	var claim struct {
		Version      int
		NetworkID    int64
		SourceNodeID int64
		InstanceID   int
		InstanceUUID string
		DeviceName   string
	}

	err := json.Unmarshal([]byte(producer[field]), &claim)
	if err != nil || claim.Version != 1 || claim.NetworkID <= 0 || !slices.Contains(m.Source.NetworkIDs, claim.NetworkID) || (networkID > 0 && claim.NetworkID != networkID) || claim.SourceNodeID != m.TargetNodeID || claim.InstanceID != m.InstanceID || claim.InstanceUUID != m.InstanceUUID || claim.DeviceName != m.DeviceName {
		return errors.New("Migration target claim lacks exact source/target allocation identity")
	}

	return nil
}

// SetOVNNICMigrationVolatile persists only finite target allocation keys. Target authority is
// required only for devices whose allocation keys change; other volatile keys stay unpersisted.
func (c *ClusterTx) SetOVNNICMigrationVolatile(ctx context.Context, operation string, instanceID int, instanceUUID string, changes map[string]string) error {
	rows, err := c.tx.QueryContext(ctx, "SELECT device_name FROM networks_ovn_nic_migration_devices WHERE operation=?", operation)
	if err != nil {
		return err
	}

	names := []string{}
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			break
		}

		names = append(names, name)
	}

	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}

	for _, name := range names {
		owned := false
		for _, key := range OVNNICStopVolatileKeys() {
			_, ok := changes["volatile."+name+"."+key]
			owned = owned || ok
		}

		if !owned {
			continue
		}

		m, err := c.OVNNICMigrationDevice(ctx, operation, name)
		if err != nil {
			return err
		}

		if m.TargetNodeID != c.nodeID || m.InstanceID != instanceID || m.InstanceUUID != instanceUUID || m.Phase != "authorized" {
			return errors.New("Migration volatile writer lost target authority")
		}

		err = c.ovnNICMigrationInstance(ctx, m)
		if err != nil {
			return err
		}

		prior := map[string]string{}
		for key, value := range m.TargetVolatile {
			prior[key] = value
		}

		changed := false
		for _, key := range OVNNICStopVolatileKeys() {
			value, ok := changes["volatile."+name+"."+key]
			if ok {
				m.TargetVolatile[key] = value
				changed = true
			}
		}

		if !changed {
			continue
		}

		for _, field := range []string{"last_state.ovn.host", "last_state.ovn.physical"} {
			if prior[field] == "" {
				continue
			}

			if m.TargetVolatile[field] == "" {
				return errors.New("Migration claim cannot be cleared without rooted retirement")
			}

			err = validateOVNNICMigrationContinuation(prior[field], m.TargetVolatile[field])
			if err != nil {
				return err
			}
		}

		if m.TargetVolatile["last_state.ovn.host"] != "" || m.TargetVolatile["last_state.ovn.physical"] != "" {
			err = validateOVNNICMigrationClaim(m, m.TargetVolatile, 0)
			if err != nil {
				return err
			}
		}

		raw, err := json.Marshal(m.TargetVolatile)
		if err != nil {
			return err
		}

		_, err = c.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET target_volatile=? WHERE operation=? AND device_name=?", string(raw), operation, name)
		if err != nil {
			return err
		}
	}

	return nil
}

// OVNNICMigrationTargetGenerations lists the target generations of an instance's live migrations
// that are in progress or whose source cleanup is still pending. That cleanup proves the transfer
// with the target generation's prefix receipt; completed migrations no longer need it.
func (c *ClusterTx) OVNNICMigrationTargetGenerations(ctx context.Context, instanceUUID string) ([]string, error) {
	rows, err := c.tx.QueryContext(ctx, "SELECT d.shared_plan FROM networks_ovn_nic_migration_devices d JOIN networks_ovn_nic_migrations m ON m.operation=d.operation LEFT JOIN networks_ovn_nic_cleanup c ON c.generation=d.generation WHERE m.instance_uuid=? AND d.shared_plan<>'' AND (m.phase IN ('authorized', 'handover') OR c.completed IS NULL OR c.completed=0)", instanceUUID)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()
	generations := []string{}
	for rows.Next() {
		var raw string
		err = rows.Scan(&raw)
		if err != nil {
			return nil, err
		}

		var plan struct{ TargetGeneration string }
		err = json.Unmarshal([]byte(raw), &plan)
		if err != nil {
			return nil, fmt.Errorf("Invalid stored migration shared plan: %w", err)
		}

		if plan.TargetGeneration != "" {
			generations = append(generations, plan.TargetGeneration)
		}
	}

	return generations, rows.Err()
}

// SetOVNNICMigrationShared stores the exact rooted plan before a backend attempt.
func (c *ClusterTx) SetOVNNICMigrationShared(ctx context.Context, m OVNNICMigration, plan string) error {
	current, err := c.OVNNICMigrationDevice(ctx, m.Operation, m.DeviceName)
	if err != nil {
		return err
	}

	if current.TargetNodeID != c.nodeID || current.Phase != "authorized" || !current.Source.matches(m.Source) || !json.Valid([]byte(plan)) || len(plan) > 4*1024*1024 {
		return errors.New("Invalid migration shared effect stage")
	}

	err = c.ovnNICMigrationInstance(ctx, current)
	if err != nil {
		return err
	}

	if current.SharedPlan != "" && current.SharedPlan != plan {
		return errors.New("Migration shared attempt changed")
	}

	_, err = c.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET shared_plan=? WHERE operation=? AND device_name=?", plan, m.Operation, m.DeviceName)
	return err
}

// RetireOVNNICMigrationPreclaim follows positively restored target-only effects.
func (c *ClusterTx) RetireOVNNICMigrationPreclaim(ctx context.Context, operation, deviceName string, original map[string]string) error {
	m, err := c.OVNNICMigrationDevice(ctx, operation, deviceName)
	if err != nil {
		return err
	}

	if m.TargetNodeID != c.nodeID || m.Phase != "authorized" || m.SharedPlan != "" || m.OVSAttempted || m.TargetOVS != "" || m.Ready {
		return errors.New("Migration target publication remains unresolved")
	}

	for _, key := range OVNNICStopVolatileKeys() {
		if m.TargetVolatile[key] != original[key] {
			return errors.New("Migration target preclaim changed")
		}
	}

	err = c.ovnNICMigrationInstance(ctx, m)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET target_volatile='{}' WHERE operation=? AND device_name=?", operation, deviceName)
	return err
}

// OVNNICMigrationReady records success of actual host, OVS and backend setup.
func (c *ClusterTx) OVNNICMigrationReady(ctx context.Context, operation, deviceName string) error {
	m, err := c.OVNNICMigrationDevice(ctx, operation, deviceName)
	if err != nil {
		return err
	}

	if m.TargetNodeID != c.nodeID || m.Phase != "authorized" || m.SharedPlan == "" || !m.OVSAttempted || m.TargetOVS == "" {
		return errors.New("Migration target shared/OVS effect is unacknowledged")
	}

	err = validateOVNNICMigrationClaim(m, m.TargetVolatile, 0)
	if err != nil {
		return err
	}

	err = c.ovnNICMigrationInstance(ctx, m)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET ready=1 WHERE operation=? AND device_name=?", operation, deviceName)
	return err
}

// OVNNICMigrationHandover is called only after the actual migration protocol commits.
func (c *ClusterTx) OVNNICMigrationHandover(ctx context.Context, operation string) error {
	var phase string
	var source int64
	err := c.tx.QueryRowContext(ctx, "SELECT phase,source_node_id FROM networks_ovn_nic_migrations WHERE operation=?", operation).Scan(&phase, &source)
	if err != nil {
		return err
	}

	if source != c.nodeID || (phase != "authorized" && phase != "handover" && phase != "placed") {
		return errors.New("Migration handover authority changed")
	}

	if phase != "authorized" {
		return nil
	}

	var devices int
	err = c.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM networks_ovn_nic_migration_devices WHERE operation=?", operation).Scan(&devices)
	if err != nil {
		return err
	}

	if devices == 0 {
		return errors.New("Migration handover has no bound target NICs")
	}

	var unready bool
	err = c.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM networks_ovn_nic_migration_devices WHERE operation=? AND (ready=0 OR shared_plan='' OR target_volatile='{}'))", operation).Scan(&unready)
	if err != nil {
		return err
	}

	if unready {
		return errors.New("Migration target has no positive NIC setup acknowledgment")
	}

	_, err = c.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase='handover' WHERE operation=? AND phase='authorized'", operation)
	return err
}

// OVNNICMigrationCommitted is an outcome read, never a process-loss certificate.
func (c *ClusterTx) OVNNICMigrationCommitted(ctx context.Context, operation string) (bool, error) {
	var phase string
	err := c.tx.QueryRowContext(ctx, "SELECT phase FROM networks_ovn_nic_migrations WHERE operation=?", operation).Scan(&phase)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	return phase == "handover" || phase == "placed", err
}

// PlaceOVNNICMigration installs exact staged allocation keys in the placement transaction.
// The enclosing caller changes node_id in this same transaction after this method.
func (c *ClusterTx) PlaceOVNNICMigration(ctx context.Context, operation string, instanceID int, instanceUUID string, targetNodeID int64) error {
	rows, err := c.tx.QueryContext(ctx, "SELECT device_name FROM networks_ovn_nic_migration_devices WHERE operation=?", operation)
	if err != nil {
		return err
	}

	names := []string{}
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			break
		}

		names = append(names, name)
	}

	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}

	for _, name := range names {
		m, err := c.OVNNICMigrationDevice(ctx, operation, name)
		if err != nil {
			return err
		}

		if m.SourceNodeID != c.nodeID || m.TargetNodeID != targetNodeID || m.InstanceID != instanceID || m.InstanceUUID != instanceUUID || m.Phase != "handover" || !m.Ready || m.SharedPlan == "" {
			return errors.New("Migration placement lacks exact positive handover")
		}

		err = c.ovnNICMigrationInstance(ctx, m)
		if err != nil {
			return err
		}

		for _, key := range OVNNICStopVolatileKeys() {
			full := "volatile." + name + "." + key
			_, err = c.tx.ExecContext(ctx, "DELETE FROM instances_config WHERE instance_id=? AND key=?", instanceID, full)
			if err != nil {
				return err
			}

			if m.TargetVolatile[key] != "" {
				_, err = c.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", instanceID, full, m.TargetVolatile[key])
				if err != nil {
					return err
				}
			}
		}
	}

	if len(names) > 0 {
		_, err = c.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase='placed' WHERE operation=? AND phase='handover'", operation)
	}

	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, "DELETE FROM networks_ovn_nic_migration_members WHERE operation=? AND EXISTS(SELECT 1 FROM networks_ovn_nic_migrations m WHERE m.operation=? AND m.phase='placed')", operation, operation)
	return err
}

func (c *ClusterTx) ensureOVNNICMigrationComplete(ctx context.Context, networkID int64, instanceUUID string) error {
	var pending bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM networks_ovn_nic_migrations m JOIN networks_ovn_nic_migration_devices d ON d.operation=m.operation JOIN networks_ovn_nic_cleanup a ON a.generation=d.generation WHERE (m.source_node_id=? OR m.target_node_id=?) AND m.phase NOT IN ('aborted','placed') AND (?='' OR m.instance_uuid=?) AND (?=0 OR EXISTS(SELECT 1 FROM json_each(a.network_ids) WHERE value=?)))`, c.nodeID, c.nodeID, instanceUUID, instanceUUID, networkID, networkID).Scan(&pending)
	if err != nil {
		return err
	}

	if pending {
		return api.StatusErrorf(http.StatusConflict, "Original NIC migration target/outcome remains unresolved")
	}

	return nil
}

// OVNNICMigrationForCleanup authorizes preserving positively transferred shared resources.
func (c *ClusterTx) OVNNICMigrationForCleanup(ctx context.Context, generation string) (*OVNNICMigration, error) {
	var operation, name string
	err := c.tx.QueryRowContext(ctx, `SELECT m.operation,d.device_name FROM networks_ovn_nic_migrations m JOIN networks_ovn_nic_migration_devices d ON d.operation=m.operation WHERE d.generation=? AND m.source_node_id=? AND m.phase IN ('handover','placed')`, generation, c.nodeID).Scan(&operation, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	m, err := c.OVNNICMigrationDevice(ctx, operation, name)
	return &m, err
}

// SetOVNNICMigrationOVS retains attempt uncertainty before target attachment.
func (c *ClusterTx) SetOVNNICMigrationOVS(ctx context.Context, operation, deviceName, plan string, attempted bool) error {
	m, err := c.OVNNICMigrationDevice(ctx, operation, deviceName)
	if err != nil {
		return err
	}

	if m.TargetNodeID != c.nodeID || m.Phase != "authorized" {
		return errors.New("Migration OVS allocation authority changed")
	}

	if plan != "" && !json.Valid([]byte(plan)) {
		return errors.New("Invalid migration OVS plan")
	}

	if m.TargetOVS != "" && plan != "" && m.TargetOVS != plan {
		return errors.New("Migration OVS allocation changed")
	}

	err = c.ovnNICMigrationInstance(ctx, m)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET target_ovs=?,ovs_attempted=? WHERE operation=? AND device_name=?", plan, attempted, operation, deviceName)
	return err
}

// RollbackOVNNICMigrationShared acknowledges verified restoration, never an absent reply.
func (c *ClusterTx) RollbackOVNNICMigrationShared(ctx context.Context, m OVNNICMigration) error {
	current, err := c.OVNNICMigrationDevice(ctx, m.Operation, m.DeviceName)
	if err != nil {
		return err
	}

	if current.TargetNodeID != c.nodeID || current.Phase != "authorized" || current.SharedPlan != m.SharedPlan {
		return errors.New("Migration rollback outcome changed")
	}

	err = c.ovnNICMigrationInstance(ctx, current)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET shared_plan='',ready=0 WHERE operation=? AND device_name=?", m.Operation, m.DeviceName)
	return err
}

// EnsureOVNNICMigrationSourceComplete is limited to the source's terminal teardown.
// Preparation still requires placement and the normal full barrier.
func (c *ClusterTx) EnsureOVNNICMigrationSourceComplete(ctx context.Context, operation, instanceUUID string) error {
	committed, err := c.OVNNICMigrationCommitted(ctx, operation)
	if err != nil {
		return err
	}

	if !committed {
		return c.EnsureOVNNICCleanupCompleteForInstance(ctx, instanceUUID)
	}

	err = c.EnsureOVNNICCleanupDebtCompleteForInstance(ctx, instanceUUID)
	if err != nil {
		return err
	}

	var unresolved bool
	err = c.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM networks_ovn_nic_migrations m JOIN networks_ovn_nic_migration_devices d ON d.operation=m.operation JOIN networks_ovn_nic_cleanup a ON a.generation=d.generation LEFT JOIN networks_ovn_nic_cleanup_retirement r ON r.generation=a.generation WHERE m.operation=? AND (m.source_node_id<>? OR m.instance_uuid<>? OR a.completed=0 OR r.generation IS NULL))`, operation, c.nodeID, instanceUUID).Scan(&unresolved)
	if err != nil {
		return err
	}

	if unresolved {
		return errors.New("Transferred source terminal cleanup remains unacknowledged")
	}

	rows, err := c.tx.QueryContext(ctx, `SELECT v.key,v.value FROM instances_config v JOIN instances_config u ON u.instance_id=v.instance_id AND u.key='volatile.uuid' WHERE u.value=? AND v.value<>'' AND (v.key GLOB 'volatile.*.last_state.ovn.host' OR v.key GLOB 'volatile.*.last_state.ovn.physical')`, instanceUUID)
	if err != nil {
		return err
	}

	claims := map[string]string{}
	for rows.Next() {
		var key, value string
		err = rows.Scan(&key, &value)
		if err != nil {
			break
		}

		claims[key] = value
	}

	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}

	for key, raw := range claims {
		var claim struct{ SourceNodeID int64 }
		if json.Unmarshal([]byte(raw), &claim) != nil {
			return errors.New("Malformed source claim at migration terminal boundary")
		}

		if claim.SourceNodeID != c.nodeID {
			continue
		}

		name := strings.TrimPrefix(key, "volatile.")
		field := "last_state.ovn.host"
		if strings.HasSuffix(name, ".last_state.ovn.physical") {
			field = "last_state.ovn.physical"
		}

		name = strings.TrimSuffix(name, "."+field)
		m, err := c.OVNNICMigrationDevice(ctx, operation, name)
		if err != nil {
			return err
		}

		var original struct{ HostVolatile map[string]string }
		if json.Unmarshal([]byte(m.Source.Payload), &original) != nil || raw != original.HostVolatile[field] {
			return errors.New("Unrelated or replaced source claim remains at migration terminal boundary")
		}
	}

	return nil
}

// EnsureOVNNICMigrationPlaced reconciles only the exact positively committed placement.
func (c *ClusterTx) EnsureOVNNICMigrationPlaced(ctx context.Context, operation string, instanceID int, instanceUUID string, target int64) error {
	rows, err := c.tx.QueryContext(ctx, "SELECT device_name FROM networks_ovn_nic_migration_devices WHERE operation=?", operation)
	if err != nil {
		return err
	}

	names := []string{}
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			break
		}

		names = append(names, name)
	}

	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}

	if len(names) == 0 {
		return sql.ErrNoRows
	}

	var node int64
	var identity string
	var owners int
	err = c.tx.QueryRowContext(ctx, `SELECT i.node_id,COALESCE(v.value,'') FROM instances i LEFT JOIN instances_config v ON v.instance_id=i.id AND v.key='volatile.uuid' WHERE i.id=?`, instanceID).Scan(&node, &identity)
	if err != nil {
		return err
	}

	err = c.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM instances_config WHERE key='volatile.uuid' AND value=?", instanceUUID).Scan(&owners)
	if err != nil {
		return err
	}

	if node != target || identity != instanceUUID || owners != 1 {
		return errors.New("Migration placement instance identity changed")
	}

	for _, name := range names {
		m, err := c.OVNNICMigrationDevice(ctx, operation, name)
		if err != nil {
			return err
		}

		if m.SourceNodeID != c.nodeID || m.TargetNodeID != target || m.InstanceID != instanceID || m.InstanceUUID != instanceUUID || m.Phase != "placed" || !m.Ready {
			return errors.New("Migration placement receipt changed")
		}

		err = c.ovnNICVolatileEqual(ctx, instanceID, name, m.TargetVolatile)
		if err != nil {
			return err
		}
	}

	return nil
}

// AbortEmptyOVNNICMigration revokes dispatch before any target allocation/effect.
// SQL claim admission and revocation serialize; a late target cannot begin effects.
func (c *ClusterTx) AbortEmptyOVNNICMigration(ctx context.Context, operation string) error {
	var phase string
	var source int64
	err := c.tx.QueryRowContext(ctx, "SELECT phase,source_node_id FROM networks_ovn_nic_migrations WHERE operation=?", operation).Scan(&phase, &source)
	if err != nil {
		return err
	}

	if source != c.nodeID {
		return errors.New("Migration abort belongs to another source")
	}

	if phase != "authorized" {
		return nil
	}

	var effects bool
	err = c.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM networks_ovn_nic_migration_devices WHERE operation=? AND (target_volatile<>'{}' OR shared_plan<>'' OR ovs_attempted<>0 OR target_ovs<>'' OR ready<>0))`, operation).Scan(&effects)
	if err != nil {
		return err
	}

	if effects {
		return errors.New("Migration target effects/outcome remain unresolved; retaining stage")
	}

	_, err = c.tx.ExecContext(ctx, "UPDATE networks_ovn_nic_migrations SET phase='aborted' WHERE operation=? AND phase='authorized'", operation)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, "DELETE FROM networks_ovn_nic_migration_members WHERE operation=? AND EXISTS(SELECT 1 FROM networks_ovn_nic_migrations m WHERE m.operation=? AND m.phase='aborted')", operation, operation)
	return err
}

func validateOVNNICMigrationContinuation(before, after string) error {
	var a, b map[string]json.RawMessage
	if json.Unmarshal([]byte(before), &a) != nil || json.Unmarshal([]byte(after), &b) != nil {
		return errors.New("Malformed migration claim continuation")
	}

	keys := []string{"NetworkID", "SourceNodeID", "InstanceID", "InstanceUUID", "DeviceName", "Name", "Alias", "BootID", "NamespaceDevice", "NamespaceInode", "Kind", "Parent", "VFID", "PFPath", "PFInode", "VFPath", "VFInode"}
	for _, key := range keys {
		if string(a[key]) != string(b[key]) {
			return errors.New("Migration claim original allocation identity changed")
		}
	}

	var originalIndex, nextIndex int
	_ = json.Unmarshal(a["Index"], &originalIndex)
	_ = json.Unmarshal(b["Index"], &nextIndex)
	if originalIndex > 0 && originalIndex != nextIndex {
		return errors.New("Migration virtual allocation index changed")
	}

	if a["Representor"] != nil {
		var ar, br map[string]json.RawMessage
		if json.Unmarshal(a["Representor"], &ar) != nil || json.Unmarshal(b["Representor"], &br) != nil {
			return errors.New("Migration representor claim changed")
		}

		for _, key := range []string{"Version", "Name", "Alias", "Index", "BootID", "NamespaceDevice", "NamespaceInode", "Kind", "HardwareAddr"} {
			if string(ar[key]) != string(br[key]) {
				return errors.New("Migration original representor changed")
			}
		}
	}

	return nil
}

// ovnNICCompletedPlaced excludes history only after exact positive source terminal and cleanup receipts.
func (c *ClusterTx) ovnNICCompletedPlaced(ctx context.Context, operation string, instanceID int, identity string) (bool, error) {
	rows, err := c.tx.QueryContext(ctx, "SELECT device_name FROM networks_ovn_nic_migration_devices WHERE operation=?", operation)
	if err != nil {
		return false, err
	}

	var names []string
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			break
		}

		names = append(names, name)
	}

	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return false, err
	}

	if len(names) == 0 {
		return false, nil
	}

	for _, name := range names {
		m, err := c.OVNNICMigrationDevice(ctx, operation, name)
		if err != nil {
			return false, err
		}

		if m.Phase != "placed" || m.InstanceID != instanceID || m.InstanceUUID != identity || m.SourceNodeID != c.nodeID || !m.Ready || !m.Source.Completed || m.SharedPlan == "" || len(m.TargetVolatile) == 0 {
			return false, nil
		}

		var original struct{ InstanceID int }
		err = json.Unmarshal([]byte(m.Source.Payload), &original)
		if err != nil || original.InstanceID != instanceID {
			return false, nil
		}

		err = c.EnsureOVNNICCleanupRetired(ctx, m.Source)
		if err != nil {
			return false, nil
		}

		err = c.ensureOVNNICMigrationSourceTerminal(ctx, m)
		if err != nil {
			return false, nil
		}
	}

	return true, nil
}

// OVNNICMigrationSourceHook discovers only the exact committed original source hook.
func (c *ClusterTx) OVNNICMigrationSourceHook(ctx context.Context, instanceID int, identity string, local map[string]string) (string, error) {
	rows, err := c.tx.QueryContext(ctx, `SELECT m.operation,m.phase FROM networks_ovn_nic_migrations m JOIN instances i ON i.id=m.instance_id JOIN instances_config v ON v.instance_id=i.id AND v.key='volatile.uuid' WHERE m.source_node_id=? AND m.instance_id=? AND m.instance_uuid=? AND m.phase IN ('handover','placed')`, c.nodeID, instanceID, identity)
	if err != nil {
		return "", err
	}

	var candidates [][2]string
	for rows.Next() {
		var item [2]string
		err = rows.Scan(&item[0], &item[1])
		if err != nil {
			break
		}

		candidates = append(candidates, item)
	}

	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return "", err
	}

	var currentNode, currentProject int64
	var currentIdentity string
	err = c.tx.QueryRowContext(ctx, "SELECT i.node_id,i.project_id,COALESCE(v.value,'') FROM instances i LEFT JOIN instances_config v ON v.instance_id=i.id AND v.key='volatile.uuid' WHERE i.id=?", instanceID).Scan(&currentNode, &currentProject, &currentIdentity)
	if err != nil {
		return "", err
	}

	if currentIdentity != identity {
		return "", errors.New("Migration source hook instance UUID changed")
	}

	if currentNode == c.nodeID {
		pending := candidates[:0]
		for _, candidate := range candidates {
			if candidate[1] == "placed" {
				complete, err := c.ovnNICCompletedPlaced(ctx, candidate[0], instanceID, identity)
				if err != nil {
					return "", err
				}

				if complete {
					continue
				}
			}

			pending = append(pending, candidate)
		}

		candidates = pending
	}

	var owners int
	err = c.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM instances_config WHERE key='volatile.uuid' AND value=?", identity).Scan(&owners)
	if err != nil {
		return "", err
	}

	if owners != 1 {
		if owners < 1 || len(candidates) > 0 || currentNode != c.nodeID {
			return "", errors.New("Migration source hook instance UUID is not unique")
		}

		err = c.EnsureOVNNICOriginalInstance(ctx, instanceID, identity)
		if err != nil {
			return "", err
		}
	}

	if len(candidates) == 0 {
		if currentNode != c.nodeID {
			return "", errors.New("Stop hook has no local placement or committed original source authority")
		}

		return "", nil
	}

	active := false
	for _, candidate := range candidates {
		active = active || candidate[1] == "handover"
	}

	var selected []string
	for _, candidate := range candidates {
		if active && candidate[1] != "handover" {
			continue
		}

		rows, err = c.tx.QueryContext(ctx, "SELECT device_name FROM networks_ovn_nic_migration_devices WHERE operation=?", candidate[0])
		if err != nil {
			return "", err
		}

		var devices []string
		for rows.Next() {
			var name string
			err = rows.Scan(&name)
			if err != nil {
				break
			}

			devices = append(devices, name)
		}

		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return "", err
		}

		if len(devices) == 0 {
			return "", errors.New("Migration source hook has no bound original NICs")
		}

		match := true
		for _, name := range devices {
			m, err := c.OVNNICMigrationDevice(ctx, candidate[0], name)
			if err != nil {
				return "", err
			}

			if m.SourceNodeID != c.nodeID || m.InstanceID != instanceID || m.InstanceUUID != identity || !m.Ready || m.SharedPlan == "" || len(m.TargetVolatile) == 0 {
				return "", errors.New("Migration source hook target receipt changed")
			}

			var original struct{ HostVolatile map[string]string }
			err = json.Unmarshal([]byte(m.Source.Payload), &original)
			if err != nil {
				return "", err
			}

			exact, empty := true, true
			for _, key := range OVNNICStopVolatileKeys() {
				value := local["volatile."+name+"."+key]
				exact = exact && value == original.HostVolatile[key]
				empty = empty && value == ""
			}

			retired := false
			if empty && (candidate[1] == "handover" || currentNode == m.TargetNodeID) {
				err = c.EnsureOVNNICCleanupRetired(ctx, m.Source)
				retired = err == nil
			}

			if !exact && !retired {
				match = false
			}
		}

		expectedNode := c.nodeID
		if candidate[1] == "placed" {
			m, err := c.OVNNICMigrationDevice(ctx, candidate[0], devices[0])
			if err != nil {
				return "", err
			}

			expectedNode = m.TargetNodeID
		}

		if match {
			m, err := c.OVNNICMigrationDevice(ctx, candidate[0], devices[0])
			if err != nil {
				return "", err
			}

			if m.ProjectID != currentProject {
				return "", errors.New("Migration source hook project changed")
			}

			if currentNode != expectedNode {
				return "", errors.New("Migration source hook placement changed")
			}

			selected = append(selected, candidate[0])
		} else if candidate[1] == "handover" || currentNode != c.nodeID {
			return "", errors.New("Migration source hook original allocation changed")
		}
	}

	if len(selected) == 0 && !active && currentNode == c.nodeID {
		return "", nil
	}

	if len(selected) != 1 {
		return "", errors.New("Migration source hook original allocation is ambiguous or replaced")
	}

	return selected[0], nil
}

// OVNNICCleanupRetry identifies an original source independently of current placement.
type OVNNICCleanupRetry struct {
	InstanceID   int
	ProjectID    int64
	InstanceUUID string
	Project      string
	Name         string
	Operation    string
	Transferred  bool
	Sources      []OVNNICCleanup
}

// OVNNICCleanupRetries refuses unknown placement and unacknowledged transferred terminal work.
func (c *ClusterTx) OVNNICCleanupRetries(ctx context.Context) ([]OVNNICCleanupRetry, error) {
	sources, err := c.OVNNICCleanups(ctx)
	if err != nil {
		return nil, err
	}

	var result []OVNNICCleanupRetry
	for _, a := range sources {
		var original struct{ InstanceID int }
		err = json.Unmarshal([]byte(a.Payload), &original)
		if err != nil || original.InstanceID <= 0 {
			return nil, errors.New("Original NIC cleanup retry instance identity is missing")
		}

		var plan OVNNICCleanupRetry
		var node int64
		var identity string
		err = c.tx.QueryRowContext(ctx, `SELECT i.id,i.project_id,p.name,i.name,i.node_id,COALESCE(v.value,'') FROM instances i JOIN projects p ON p.id=i.project_id LEFT JOIN instances_config v ON v.instance_id=i.id AND v.key='volatile.uuid' WHERE i.id=?`, original.InstanceID).Scan(&plan.InstanceID, &plan.ProjectID, &plan.Project, &plan.Name, &node, &identity)
		if err != nil {
			return nil, err
		}

		var owners int
		err = c.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM instances_config WHERE key='volatile.uuid' AND value=?", a.InstanceUUID).Scan(&owners)
		if err != nil {
			return nil, err
		}

		if identity != a.InstanceUUID {
			return nil, errors.New("Original NIC cleanup retry UUID owner changed")
		}

		if owners != 1 {
			err = c.EnsureOVNNICOriginalInstance(ctx, plan.InstanceID, identity)
			if err != nil {
				return nil, err
			}
		}

		plan.InstanceUUID = identity
		plan.Transferred = node != c.nodeID
		if plan.Transferred {
			m, err := c.OVNNICMigrationForCleanup(ctx, a.Generation)
			if err != nil {
				return nil, err
			}

			if m == nil || m.Phase != "placed" || m.SourceNodeID != c.nodeID || m.InstanceID != plan.InstanceID || m.ProjectID != plan.ProjectID || m.InstanceUUID != identity || m.TargetNodeID != node || !m.Ready || m.SharedPlan == "" || len(m.TargetVolatile) == 0 || !m.validSourceTerminal() || !m.Source.matches(a) {
				return nil, errors.New("Original transferred NIC retry lacks exact placement and positive source terminal receipt")
			}

			err = c.ensureOVNNICMigrationSourceTerminal(ctx, *m)
			if err != nil {
				return nil, err
			}

			plan.Operation = m.Operation
		}

		found := false
		for i := range result {
			if result[i].InstanceUUID != plan.InstanceUUID {
				continue
			}

			if result[i].InstanceID != plan.InstanceID || result[i].ProjectID != plan.ProjectID || result[i].Operation != plan.Operation || result[i].Transferred != plan.Transferred {
				return nil, errors.New("Original NIC cleanup retry lineage is ambiguous")
			}

			result[i].Sources = append(result[i].Sources, a)
			found = true
			break
		}

		if !found {
			plan.Sources = []OVNNICCleanup{a}
			result = append(result, plan)
		}
	}

	return result, nil
}

// RecordOVNNICMigrationSourceTerminal is called after actual original driver runtime teardown.
func (c *ClusterTx) RecordOVNNICMigrationSourceTerminal(ctx context.Context, operation string, instanceID int, identity string, local map[string]string) error {
	selected, err := c.OVNNICMigrationSourceHook(ctx, instanceID, identity, local)
	if err != nil {
		return err
	}

	if operation == "" || selected != operation {
		return errors.New("Original source terminal receipt authority changed")
	}

	rows, err := c.tx.QueryContext(ctx, "SELECT device_name FROM networks_ovn_nic_migration_devices WHERE operation=? ORDER BY device_name", operation)
	if err != nil {
		return err
	}

	var names []string
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			break
		}

		names = append(names, name)
	}

	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}

	var receipt ovnNICSourceTerminal
	existing := ""
	for _, name := range names {
		m, err := c.OVNNICMigrationDevice(ctx, operation, name)
		if err != nil {
			return err
		}

		if receipt.Devices == nil {
			receipt = ovnNICSourceTerminal{Operation: operation, InstanceID: m.InstanceID, ProjectID: m.ProjectID, InstanceUUID: m.InstanceUUID, SourceNodeID: m.SourceNodeID, TargetNodeID: m.TargetNodeID, Devices: map[string]ovnNICSourceTerminalDevice{}}
			existing = m.SourceTerminal
		}

		receipt.Devices[name] = m.sourceTerminalDevice()
	}

	if len(receipt.Devices) == 0 {
		return errors.New("Original source terminal stage is empty")
	}

	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}

	if existing != "" && existing != string(raw) {
		return errors.New("Original source terminal receipt was replaced")
	}

	result, err := c.tx.ExecContext(ctx, `UPDATE networks_ovn_nic_migrations SET source_terminal=? WHERE operation=? AND instance_id=? AND instance_uuid=? AND source_node_id=? AND phase IN ('handover','placed') AND source_terminal=?`, string(raw), operation, instanceID, identity, c.nodeID, existing)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if count != 1 {
		return errors.New("Original source terminal receipt changed")
	}

	return nil
}

type ovnNICSourceTerminalDevice struct {
	Generation string
	Payload    string
	Shared     string
	Target     string
}

type ovnNICSourceTerminal struct {
	Operation    string
	InstanceID   int
	ProjectID    int64
	InstanceUUID string
	SourceNodeID int64
	TargetNodeID int64
	Devices      map[string]ovnNICSourceTerminalDevice
}

func (m OVNNICMigration) sourceTerminalDevice() ovnNICSourceTerminalDevice {
	target, _ := json.Marshal(m.TargetVolatile)
	return ovnNICSourceTerminalDevice{Generation: m.Source.Generation, Payload: fmt.Sprintf("%x", sha256.Sum256([]byte(m.Source.Payload))), Shared: fmt.Sprintf("%x", sha256.Sum256([]byte(m.SharedPlan))), Target: fmt.Sprintf("%x", sha256.Sum256(target))}
}

func (m OVNNICMigration) validSourceTerminal() bool {
	var receipt ovnNICSourceTerminal
	err := json.Unmarshal([]byte(m.SourceTerminal), &receipt)
	return err == nil && receipt.Operation == m.Operation && receipt.InstanceID == m.InstanceID && receipt.ProjectID == m.ProjectID && receipt.InstanceUUID == m.InstanceUUID && receipt.SourceNodeID == m.SourceNodeID && receipt.TargetNodeID == m.TargetNodeID && receipt.Devices[m.DeviceName] == m.sourceTerminalDevice()
}

func (c *ClusterTx) ensureOVNNICMigrationSourceTerminal(ctx context.Context, selected OVNNICMigration) error {
	var receipt ovnNICSourceTerminal
	err := json.Unmarshal([]byte(selected.SourceTerminal), &receipt)
	if err != nil || !selected.validSourceTerminal() {
		return errors.New("Original source terminal receipt tuple changed")
	}

	rows, err := c.tx.QueryContext(ctx, "SELECT device_name FROM networks_ovn_nic_migration_devices WHERE operation=? ORDER BY device_name", selected.Operation)
	if err != nil {
		return err
	}

	var names []string
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			break
		}

		names = append(names, name)
	}

	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}

	if len(names) != len(receipt.Devices) {
		return errors.New("Original source terminal receipt device set changed")
	}

	for _, name := range names {
		m, err := c.OVNNICMigrationDevice(ctx, selected.Operation, name)
		if err != nil {
			return err
		}

		if !m.validSourceTerminal() {
			return errors.New("Original source terminal receipt sibling changed")
		}
	}

	return nil
}
