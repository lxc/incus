//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/shared/api"
)

// ProfileReferenceProfile preserves an ordered profile input, including its database identity.
type ProfileReferenceProfile struct {
	ID         int64
	Generation int64
	Profile    api.Profile
}

// InstanceReferenceFields records scalar inputs absent from the persisted v1 snapshot.
type InstanceReferenceFields struct {
	Name         string
	Type         instancetype.Type
	Snapshot     bool
	Architecture int
	Description  string
	Ephemeral    bool
	Stateful     bool
	CreationDate time.Time
	LastUsedDate sql.NullTime
	ExpiryDate   sql.NullTime
	BaseImage    string
}

// InstanceUpdatePrecondition binds an ordinary proposal to its original transaction read.
type InstanceUpdatePrecondition struct {
	Version                                                            int
	Current                                                            ProfileReferenceSnapshot
	Fields                                                             InstanceReferenceFields
	DesiredSequence, AppliedSequence, PlacementRevision, InputRevision int64
	ObservationWatermark                                               int64
}

// OrdinaryProfileReferenceUpdate is a complete local replacement with explicit ordered profiles.
type OrdinaryProfileReferenceUpdate struct {
	Expected InstanceUpdatePrecondition
	Config   map[string]string
	Devices  map[string]map[string]string
	Profiles []ProfileReferenceProfile
	Fields   InstanceReferenceFields
}

// ProfileReferenceResource identifies a resolved managed NIC reference without relying on names.
type ProfileReferenceResource struct {
	Device           string
	NetworkID        int64
	NetworkProjectID int64
	NetworkProject   string
	NetworkName      string
	NetworkType      string
	ACLID            int64
	ACLProjectID     int64
	Config           map[string]string
}

// ProfileReferenceSnapshot is immutable versioned input for a later instance application.
type ProfileReferenceSnapshot struct {
	Version         int
	InstanceID      int64
	ProjectID       int64
	MemberID        int64
	Project         api.Project
	Profiles        []ProfileReferenceProfile
	LocalConfig     map[string]string
	LocalDevices    map[string]map[string]string
	ExpandedConfig  map[string]string
	ExpandedDevices map[string]map[string]string
	Resources       []ProfileReferenceResource
}

// ProfileReferenceInputsEqual compares fresh application inputs, ignoring only descriptive
// project metadata. It must not be used for exact attempt identity, target acknowledgment
// or completion. Detached project copies keep the caller's immutable snapshots untouched.
func ProfileReferenceInputsEqual(left, right ProfileReferenceSnapshot) bool {
	for _, snapshot := range []*ProfileReferenceSnapshot{&left, &right} {
		snapshot.Project.Description = ""
		config := make(map[string]string, len(snapshot.Project.Config))
		for key, value := range snapshot.Project.Config {
			if !strings.HasPrefix(key, "user.") {
				config[key] = value
			}
		}
		snapshot.Project.Config = config
	}

	return reflect.DeepEqual(left, right)
}

// ProfileReferenceState distinguishes known applied state from current desired state.
type ProfileReferenceState struct {
	InstanceID        int64
	ProjectID         int64
	MemberID          int64
	PlacementRevision int64
	DesiredSequence   int64
	AppliedSequence   int64
	Applied           ProfileReferenceSnapshot
	Desired           ProfileReferenceSnapshot
	ActiveToken       string
}

// ProfileReferenceConsumer is the immutable before/after target of one profile change.
type ProfileReferenceConsumer struct {
	ID                int64
	ChangeToken       string
	InstanceID        int64
	ProjectID         int64
	MemberID          int64
	PlacementRevision int64
	Sequence          int64
	Before            ProfileReferenceSnapshot
	After             ProfileReferenceSnapshot
	Satisfied         bool
}

// ProfileReferenceApply identifies one admitted attempt; every field participates in acknowledgment.
type ProfileReferenceApply struct {
	Token             string
	ChangeToken       string
	InstanceID        int64
	ProjectID         int64
	MemberID          int64
	PlacementRevision int64
	Sequence          int64
	Owner             string
}

// ProfileReferenceChild binds an attempt to an actual network reservation, before device effects.
type ProfileReferenceChild struct {
	NetworkID int64
	ProjectID int64
	Name      string
	Token     string
}

// ProfileReferenceUsage is retained usage, distinct from current desired instance devices.
type ProfileReferenceUsage struct {
	ConsumerID        int64
	AttemptToken      string
	InstanceID        int64
	ProjectID         int64
	MemberID          int64
	PlacementRevision int64
	Sequence          int64
	Role              string
	ProfileReferenceResource
}

func profileReferenceConflict() error {
	return api.StatusErrorf(http.StatusConflict, "Profile reference state or application ownership changed")
}

func profileReferenceEncode(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}

func profileReferenceDecode(data string, value any) error {
	return json.Unmarshal([]byte(data), value)
}

func profileReferenceDecodeSnapshot(data string) (ProfileReferenceSnapshot, error) {
	var snapshot ProfileReferenceSnapshot
	err := profileReferenceDecode(data, &snapshot)
	if err != nil {
		return ProfileReferenceSnapshot{}, err
	}

	if snapshot.Version != 1 {
		return ProfileReferenceSnapshot{}, profileReferenceConflict()
	}

	return snapshot, nil
}

// ProfileReferenceState returns an existing baseline; missing legacy state is never synthesized.
func (c *ClusterTx) ProfileReferenceState(ctx context.Context, instanceID int64) (*ProfileReferenceState, error) {
	s := &ProfileReferenceState{InstanceID: instanceID}
	var applied, desired string
	var contractVersion, inputRevision, materializedObservationID int64
	err := c.tx.QueryRowContext(ctx, `SELECT project_id, member_id, placement_revision, desired_sequence, applied_sequence, applied_snapshot, desired_snapshot, active_token, contract_version, input_revision, materialized_observation_id FROM instances_profile_reference_apply WHERE instance_id=?`, instanceID).Scan(&s.ProjectID, &s.MemberID, &s.PlacementRevision, &s.DesiredSequence, &s.AppliedSequence, &applied, &desired, &s.ActiveToken, &contractVersion, &inputRevision, &materializedObservationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, api.StatusErrorf(http.StatusConflict, "Instance has no evidenced profile reference baseline")
	}

	if err != nil {
		return nil, err
	}

	if contractVersion != 1 || inputRevision != 0 || materializedObservationID != 0 {
		return nil, profileReferenceConflict()
	}

	s.Applied, err = profileReferenceDecodeSnapshot(applied)
	if err != nil {
		return nil, err
	}

	s.Desired, err = profileReferenceDecodeSnapshot(desired)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// InitializeProfileReferenceBaseline is for a successful creation or separately evidenced reconciliation transaction.
// Reading current desired state alone is not evidence that a legacy instance has applied it.
func (c *ClusterTx) InitializeProfileReferenceBaseline(ctx context.Context, snapshot ProfileReferenceSnapshot, placementRevision int64) error {
	if snapshot.Version != 1 || placementRevision < 1 {
		return profileReferenceConflict()
	}

	err := c.profileReferencePlacement(ctx, snapshot.InstanceID, snapshot.ProjectID, snapshot.MemberID)
	if err != nil {
		return err
	}

	data, err := profileReferenceEncode(snapshot)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `INSERT INTO instances_profile_reference_apply (instance_id, project_id, member_id, placement_revision, applied_snapshot, desired_snapshot) VALUES (?, ?, ?, ?, ?, ?)`, snapshot.InstanceID, snapshot.ProjectID, snapshot.MemberID, placementRevision, data, data)
	return err
}

func (c *ClusterTx) profileReferencePlacement(ctx context.Context, instanceID, projectID, memberID int64) error {
	var valid bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM instances WHERE id=? AND project_id=? AND node_id=?)`, instanceID, projectID, memberID).Scan(&valid)
	if err != nil {
		return err
	}

	if !valid {
		return profileReferenceConflict()
	}

	return nil
}

// CheckProfileReferenceWriters checks both removed and desired resources without requiring old Created state.
func (c *ClusterTx) CheckProfileReferenceWriters(ctx context.Context, snapshots ...ProfileReferenceSnapshot) error {
	for _, snapshot := range snapshots {
		for _, resource := range snapshot.Resources {
			var conflict bool
			err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_operations WHERE (project_id=(SELECT id FROM projects WHERE name=?) AND name=?) OR (project_id=? AND name=?))`, api.ProjectDefaultName, OVNPeerOperationName, resource.NetworkProjectID, resource.NetworkName).Scan(&conflict)
			if err != nil {
				return err
			}

			if conflict {
				return profileReferenceConflict()
			}
		}
	}

	return nil
}

// CreateProfileReferenceChange records a profile transition in the transaction that writes the actual profile.
func (c *ClusterTx) CreateProfileReferenceChange(ctx context.Context, token string, profileID, projectID, generation int64, before, after api.ProfilePut, consumers []ProfileReferenceConsumer) error {
	if token == "" {
		return profileReferenceConflict()
	}

	oldData, err := profileReferenceEncode(before)
	if err != nil {
		return err
	}

	newData, err := profileReferenceEncode(after)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `INSERT INTO profiles_reference_changes (token, profile_id, project_id, generation, old_profile, new_profile, completed) VALUES (?, ?, ?, ?, ?, ?, ?)`, token, profileID, projectID, generation, oldData, newData, len(consumers) == 0)
	if err != nil {
		return err
	}

	for _, consumer := range consumers {
		if consumer.Before.Version != 1 || consumer.After.Version != 1 {
			return profileReferenceConflict()
		}

		s, err := c.ProfileReferenceState(ctx, consumer.InstanceID)
		if err != nil {
			return err
		}

		if s.ActiveToken != "" || s.ProjectID != consumer.ProjectID || s.MemberID != consumer.MemberID || s.PlacementRevision != consumer.PlacementRevision || s.DesiredSequence+1 != consumer.Sequence || !ProfileReferenceInputsEqual(s.Desired, consumer.Before) {
			return profileReferenceConflict()
		}

		err = c.profileReferencePlacement(ctx, consumer.InstanceID, consumer.ProjectID, consumer.MemberID)
		if err != nil {
			return err
		}

		err = c.CheckProfileReferenceWriters(ctx, s.Applied, consumer.Before, consumer.After)
		if err != nil {
			return err
		}

		beforeData, err := profileReferenceEncode(consumer.Before)
		if err != nil {
			return err
		}

		afterData, err := profileReferenceEncode(consumer.After)
		if err != nil {
			return err
		}

		result, err := c.tx.ExecContext(ctx, `INSERT INTO profiles_reference_consumers (change_token, instance_id, project_id, member_id, placement_revision, sequence, before_snapshot, after_snapshot) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, token, consumer.InstanceID, consumer.ProjectID, consumer.MemberID, consumer.PlacementRevision, consumer.Sequence, beforeData, afterData)
		if err != nil {
			return err
		}

		consumerID, err := result.LastInsertId()
		if err != nil {
			return err
		}

		for role, snapshot := range map[string]ProfileReferenceSnapshot{"baseline": s.Applied, "before": consumer.Before, "after": consumer.After} {
			err = c.retainProfileReferenceUsage(ctx, consumerID, "", consumer.PlacementRevision, consumer.Sequence, role, snapshot)
			if err != nil {
				return err
			}
		}
		_, err = c.tx.ExecContext(ctx, `UPDATE instances_profile_reference_apply SET desired_sequence=?, desired_snapshot=? WHERE instance_id=?`, consumer.Sequence, afterData, consumer.InstanceID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *ClusterTx) retainProfileReferenceUsage(ctx context.Context, consumerID int64, attemptToken string, placement, sequence int64, role string, snapshot ProfileReferenceSnapshot) error {
	for _, resource := range snapshot.Resources {
		config, err := profileReferenceEncode(resource.Config)
		if err != nil {
			return err
		}

		_, err = c.tx.ExecContext(ctx, `INSERT INTO profiles_reference_usage (consumer_id, attempt_token, instance_id, project_id, member_id, placement_revision, sequence, role, device, network_id, network_project_id, network_type, acl_id, acl_project_id, config) VALUES (NULLIF(?, 0), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, 0), NULLIF(?, 0), ?)`, consumerID, attemptToken, snapshot.InstanceID, snapshot.ProjectID, snapshot.MemberID, placement, sequence, role, resource.Device, resource.NetworkID, resource.NetworkProjectID, resource.NetworkType, resource.ACLID, resource.ACLProjectID, config)
		if err != nil {
			return err
		}
	}
	return nil
}

// ProfileReferenceConsumers returns immutable targets and individual completion state.
func (c *ClusterTx) ProfileReferenceConsumers(ctx context.Context, token string) ([]ProfileReferenceConsumer, error) {
	rows, err := c.tx.QueryContext(ctx, `SELECT id, change_token, instance_id, project_id, member_id, placement_revision, sequence, before_snapshot, after_snapshot, satisfied FROM profiles_reference_consumers WHERE change_token=? ORDER BY instance_id`, token)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()
	result := []ProfileReferenceConsumer{}
	for rows.Next() {
		var consumer ProfileReferenceConsumer
		var before, after string
		err = rows.Scan(&consumer.ID, &consumer.ChangeToken, &consumer.InstanceID, &consumer.ProjectID, &consumer.MemberID, &consumer.PlacementRevision, &consumer.Sequence, &before, &after, &consumer.Satisfied)
		if err != nil {
			return nil, err
		}

		consumer.Before, err = profileReferenceDecodeSnapshot(before)
		if err != nil {
			return nil, err
		}

		consumer.After, err = profileReferenceDecodeSnapshot(after)
		if err != nil {
			return nil, err
		}

		result = append(result, consumer)
	}

	return result, rows.Err()
}

// ProfileReferenceUsage returns retained usage by resource ID; zero selects all resources.
func (c *ClusterTx) ProfileReferenceUsage(ctx context.Context, networkID, aclID int64) ([]ProfileReferenceUsage, error) {
	rows, err := c.tx.QueryContext(ctx, `SELECT COALESCE(consumer_id, 0), COALESCE(attempt_token, ''), instance_id, project_id, member_id, placement_revision, sequence, role, device, network_id, network_project_id, network_type, COALESCE(acl_id, 0), COALESCE(acl_project_id, 0), config FROM profiles_reference_usage WHERE (?=0 OR network_id=?) AND (?=0 OR acl_id=?) ORDER BY id`, networkID, networkID, aclID, aclID)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()
	result := []ProfileReferenceUsage{}
	for rows.Next() {
		var usage ProfileReferenceUsage
		var config string
		err = rows.Scan(&usage.ConsumerID, &usage.AttemptToken, &usage.InstanceID, &usage.ProjectID, &usage.MemberID, &usage.PlacementRevision, &usage.Sequence, &usage.Role, &usage.Device, &usage.NetworkID, &usage.NetworkProjectID, &usage.NetworkType, &usage.ACLID, &usage.ACLProjectID, &config)
		if err != nil {
			return nil, err
		}

		err = profileReferenceDecode(config, &usage.Config)
		if err != nil {
			return nil, err
		}

		result = append(result, usage)
	}

	return result, rows.Err()
}

// ClaimProfileReferenceApply admits one writer; an empty ChangeToken denotes an ordinary instance writer.
// Callers must validate the target against current desired inputs in this same transaction before claiming.
func (c *ClusterTx) ClaimProfileReferenceApply(ctx context.Context, identity ProfileReferenceApply, target ProfileReferenceSnapshot) error {
	if identity.Token == "" || identity.Owner == "" || identity.MemberID != c.nodeID || target.Version != 1 || target.InstanceID != identity.InstanceID || target.ProjectID != identity.ProjectID || target.MemberID != identity.MemberID {
		return profileReferenceConflict()
	}

	s, err := c.ProfileReferenceState(ctx, identity.InstanceID)
	if err != nil {
		return err
	}

	expectedSequence := s.DesiredSequence
	if identity.ChangeToken == "" {
		expectedSequence++
	}

	if s.ActiveToken != "" || s.ProjectID != identity.ProjectID || s.MemberID != identity.MemberID || s.PlacementRevision != identity.PlacementRevision || expectedSequence != identity.Sequence {
		return profileReferenceConflict()
	}

	err = c.profileReferencePlacement(ctx, identity.InstanceID, identity.ProjectID, identity.MemberID)
	if err != nil {
		return err
	}

	var shared bool
	err = c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_operations WHERE project_id=(SELECT id FROM projects WHERE name=?) AND name=?)`, api.ProjectDefaultName, OVNPeerOperationName).Scan(&shared)
	if err != nil {
		return err
	}

	if shared {
		return profileReferenceConflict()
	}

	if identity.ChangeToken != "" {
		var valid bool
		err = c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM profiles_reference_consumers WHERE change_token=? AND instance_id=? AND sequence<=? AND member_id=? AND placement_revision=? AND satisfied=0)`, identity.ChangeToken, identity.InstanceID, identity.Sequence, identity.MemberID, identity.PlacementRevision).Scan(&valid)
		if err != nil {
			return err
		}

		if !valid || !reflect.DeepEqual(s.Desired, target) {
			return profileReferenceConflict()
		}
	}
	err = c.CheckProfileReferenceWriters(ctx, s.Applied, target)
	if err != nil {
		return err
	}

	baseline, err := profileReferenceEncode(s.Applied)
	if err != nil {
		return err
	}

	desired, err := profileReferenceEncode(target)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `INSERT INTO profiles_reference_attempts (token, change_token, instance_id, project_id, member_id, placement_revision, sequence, owner, baseline_snapshot, target_snapshot, phase) VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, 'applying')`, identity.Token, identity.ChangeToken, identity.InstanceID, identity.ProjectID, identity.MemberID, identity.PlacementRevision, identity.Sequence, identity.Owner, baseline, desired)
	if err != nil {
		return err
	}

	for role, snapshot := range map[string]ProfileReferenceSnapshot{"baseline": s.Applied, "after": target} {
		err = c.retainProfileReferenceUsage(ctx, 0, identity.Token, identity.PlacementRevision, identity.Sequence, role, snapshot)
		if err != nil {
			return err
		}
	}
	_, err = c.tx.ExecContext(ctx, `UPDATE instances_profile_reference_apply SET active_token=?, desired_sequence=? WHERE instance_id=?`, identity.Token, identity.Sequence, identity.InstanceID)
	return err
}

// Static compatibility also applies to historical attempts without current admission ownership.
type profileReferenceAttemptContract struct {
	version              int64
	claimedInputRevision int64
	resultInputRevision  int64
	observationCutoff    int64
	generationPlan       string
	resultSnapshot       string
	baseline             string
	target               string
}

func (contract profileReferenceAttemptContract) snapshots() (ProfileReferenceSnapshot, ProfileReferenceSnapshot, error) {
	if contract.version != 1 || contract.claimedInputRevision != 0 || contract.resultInputRevision != 0 || contract.observationCutoff != 0 || contract.generationPlan != "" || contract.resultSnapshot != "" {
		return ProfileReferenceSnapshot{}, ProfileReferenceSnapshot{}, profileReferenceConflict()
	}

	before, err := profileReferenceDecodeSnapshot(contract.baseline)
	if err != nil {
		return ProfileReferenceSnapshot{}, ProfileReferenceSnapshot{}, err
	}

	after, err := profileReferenceDecodeSnapshot(contract.target)
	if err != nil {
		return ProfileReferenceSnapshot{}, ProfileReferenceSnapshot{}, err
	}

	return before, after, nil
}

func (c *ClusterTx) profileReferenceAttempt(ctx context.Context, identity ProfileReferenceApply) (string, bool, ProfileReferenceSnapshot, ProfileReferenceSnapshot, error) {
	var actual ProfileReferenceApply
	var phase string
	var sealed bool
	var contract profileReferenceAttemptContract
	err := c.tx.QueryRowContext(ctx, `SELECT token, COALESCE(change_token, ''), instance_id, project_id, member_id, placement_revision, sequence, owner, phase, children_sealed, contract_version, claimed_input_revision, result_input_revision, observation_cutoff, generation_plan, result_snapshot, baseline_snapshot, target_snapshot FROM profiles_reference_attempts WHERE token=?`, identity.Token).Scan(&actual.Token, &actual.ChangeToken, &actual.InstanceID, &actual.ProjectID, &actual.MemberID, &actual.PlacementRevision, &actual.Sequence, &actual.Owner, &phase, &sealed, &contract.version, &contract.claimedInputRevision, &contract.resultInputRevision, &contract.observationCutoff, &contract.generationPlan, &contract.resultSnapshot, &contract.baseline, &contract.target)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && actual != identity) {
		return "", false, ProfileReferenceSnapshot{}, ProfileReferenceSnapshot{}, profileReferenceConflict()
	}

	if err != nil {
		return "", false, ProfileReferenceSnapshot{}, ProfileReferenceSnapshot{}, err
	}

	before, after, err := contract.snapshots()
	if err != nil {
		return "", false, before, after, err
	}

	if phase != "completed" {
		if identity.MemberID != c.nodeID {
			return "", false, before, after, profileReferenceConflict()
		}

		s, err := c.ProfileReferenceState(ctx, identity.InstanceID)
		if err != nil {
			return "", false, before, after, err
		}

		if s.ActiveToken != identity.Token || s.DesiredSequence != identity.Sequence || s.ProjectID != identity.ProjectID || s.MemberID != identity.MemberID || s.PlacementRevision != identity.PlacementRevision {
			return "", false, before, after, profileReferenceConflict()
		}

		err = c.profileReferencePlacement(ctx, identity.InstanceID, identity.ProjectID, identity.MemberID)
		if err != nil {
			return "", false, before, after, err
		}
	}
	return phase, sealed, before, after, nil
}

func (c *ClusterTx) verifyProfileReferenceChild(ctx context.Context, identity ProfileReferenceApply, child ProfileReferenceChild) error {
	var valid bool
	err := c.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks n JOIN networks_ovn_operations op ON op.project_id=n.project_id AND op.name=n.name WHERE n.id=? AND n.project_id=? AND n.name=? AND op.token=? AND op.node_id=? AND op.operation='nic' AND NOT EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE token=op.token))`, child.NetworkID, child.ProjectID, child.Name, child.Token, identity.MemberID).Scan(&valid)
	if err != nil {
		return err
	}

	if !valid || child.Token == "" {
		return profileReferenceConflict()
	}

	return nil
}

// SealProfileReferenceChildren persists the entire exact OVN reservation set before device effects.
func (c *ClusterTx) SealProfileReferenceChildren(ctx context.Context, identity ProfileReferenceApply, children []ProfileReferenceChild) error {
	phase, sealed, before, after, err := c.profileReferenceAttempt(ctx, identity)
	if err != nil {
		return err
	}

	if phase != "applying" || sealed {
		return profileReferenceConflict()
	}

	plan, err := c.profileReferenceChildNetworks(ctx, identity, before, after)
	if err != nil {
		return err
	}

	expected := make(map[int64]profileReferenceChildNetwork, len(plan))
	for _, network := range plan {
		expected[network.NetworkID] = network
	}

	if len(expected) != len(children) {
		return profileReferenceConflict()
	}

	for _, child := range children {
		resource, ok := expected[child.NetworkID]
		if !ok || resource.ProjectID != child.ProjectID || resource.Name != child.Name {
			return profileReferenceConflict()
		}

		delete(expected, child.NetworkID)
		err = c.verifyProfileReferenceChild(ctx, identity, child)
		if err != nil {
			return err
		}

		_, err = c.tx.ExecContext(ctx, `INSERT INTO profiles_reference_children (attempt_token, network_id, project_id, name, token) VALUES (?, ?, ?, ?, ?)`, identity.Token, child.NetworkID, child.ProjectID, child.Name, child.Token)
		if err != nil {
			return err
		}
	}
	children = slices.Clone(children)
	slices.SortFunc(children, profileReferenceChildOrder)
	childSet, err := profileReferenceEncode(children)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE profiles_reference_attempts SET children_sealed=1, child_set=? WHERE token=?`, childSet, identity.Token)
	return err
}

// MarkProfileReferenceApplied must share the driver's successful instance commit transaction.
func (c *ClusterTx) MarkProfileReferenceApplied(ctx context.Context, identity ProfileReferenceApply) error {
	phase, sealed, _, target, err := c.profileReferenceAttempt(ctx, identity)
	if err != nil {
		return err
	}

	if phase != "applying" || !sealed {
		return profileReferenceConflict()
	}

	data, err := profileReferenceEncode(target)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE instances_profile_reference_apply SET applied_sequence=?, applied_snapshot=?, desired_snapshot=? WHERE instance_id=?`, identity.Sequence, data, data, identity.InstanceID)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE profiles_reference_attempts SET phase='applied' WHERE token=?`, identity.Token)
	return err
}

func (c *ClusterTx) releaseProfileReferenceChildren(ctx context.Context, identity ProfileReferenceApply) error {
	rows, err := c.tx.QueryContext(ctx, `SELECT network_id, project_id, name, token FROM profiles_reference_children WHERE attempt_token=? ORDER BY network_id`, identity.Token)
	if err != nil {
		return err
	}

	children := []ProfileReferenceChild{}
	for rows.Next() {
		var child ProfileReferenceChild
		err = rows.Scan(&child.NetworkID, &child.ProjectID, &child.Name, &child.Token)
		if err != nil {
			_ = rows.Close()
			return err
		}

		children = append(children, child)
	}

	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}

	var childSet string
	var sealed bool
	err = c.tx.QueryRowContext(ctx, `SELECT child_set, children_sealed FROM profiles_reference_attempts WHERE token=?`, identity.Token).Scan(&childSet, &sealed)
	if err != nil {
		return err
	}

	if sealed {
		var expected []ProfileReferenceChild
		err = profileReferenceDecode(childSet, &expected)
		if err != nil {
			return err
		}

		if len(expected) != len(children) || (len(children) > 0 && !reflect.DeepEqual(expected, children)) {
			return profileReferenceConflict()
		}
	} else if len(children) != 0 {
		return profileReferenceConflict()
	}

	for _, child := range children {
		err = c.verifyProfileReferenceChild(ctx, identity, child)
		if err != nil {
			return err
		}
	}
	for _, child := range children {
		_, err = c.tx.ExecContext(ctx, `DELETE FROM networks_ovn_operations WHERE project_id=? AND name=? AND token=? AND node_id=?`, child.ProjectID, child.Name, child.Token, identity.MemberID)
		if err != nil {
			return err
		}
	}
	_, err = c.tx.ExecContext(ctx, `DELETE FROM profiles_reference_children WHERE attempt_token=?`, identity.Token)
	return err
}

// Preflight only historical rows whose usage or satisfaction this finalization will change.
func (c *ClusterTx) profileReferenceFinalizeChanges(ctx context.Context, identity ProfileReferenceApply) ([]string, error) {
	rows, err := c.tx.QueryContext(ctx, `SELECT contract_version, claimed_input_revision, result_input_revision, observation_cutoff, generation_plan, result_snapshot, baseline_snapshot, target_snapshot FROM profiles_reference_attempts WHERE instance_id=? AND member_id=? AND placement_revision=? AND sequence<=? AND phase='retryable' AND EXISTS (SELECT 1 FROM profiles_reference_usage WHERE attempt_token=profiles_reference_attempts.token)`, identity.InstanceID, identity.MemberID, identity.PlacementRevision, identity.Sequence)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var contract profileReferenceAttemptContract
		err = rows.Scan(&contract.version, &contract.claimedInputRevision, &contract.resultInputRevision, &contract.observationCutoff, &contract.generationPlan, &contract.resultSnapshot, &contract.baseline, &contract.target)
		if err == nil {
			_, _, err = contract.snapshots()
		}

		if err != nil {
			_ = rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}

	rows, err = c.tx.QueryContext(ctx, `SELECT change_token, before_snapshot, after_snapshot FROM profiles_reference_consumers WHERE instance_id=? AND member_id=? AND placement_revision=? AND sequence<=? AND satisfied=0 ORDER BY change_token`, identity.InstanceID, identity.MemberID, identity.PlacementRevision, identity.Sequence)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()
	changes := []string{}
	for rows.Next() {
		var token, before, after string
		err = rows.Scan(&token, &before, &after)
		if err != nil {
			return nil, err
		}

		_, err = profileReferenceDecodeSnapshot(before)
		if err != nil {
			return nil, err
		}

		_, err = profileReferenceDecodeSnapshot(after)
		if err != nil {
			return nil, err
		}

		if len(changes) == 0 || changes[len(changes)-1] != token {
			changes = append(changes, token)
		}
	}
	return changes, rows.Err()
}

// FinalizeProfileReferenceApply atomically releases exact children, records a receipt and satisfies covered consumers.
func (c *ClusterTx) FinalizeProfileReferenceApply(ctx context.Context, identity ProfileReferenceApply) error {
	phase, sealed, _, target, err := c.profileReferenceAttempt(ctx, identity)
	if err != nil {
		return err
	}

	if phase == "completed" {
		var result, evidence string
		err = c.tx.QueryRowContext(ctx, `SELECT result_snapshot, post_commit_evidence FROM profiles_reference_receipts WHERE attempt_token=?`, identity.Token).Scan(&result, &evidence)
		if errors.Is(err, sql.ErrNoRows) {
			return profileReferenceConflict()
		}

		if err != nil {
			return err
		}

		if result != "" || evidence != "" {
			return profileReferenceConflict()
		}

		return nil
	}

	if phase != "applied" || !sealed {
		return profileReferenceConflict()
	}

	s, err := c.ProfileReferenceState(ctx, identity.InstanceID)
	if err != nil {
		return err
	}

	if s.AppliedSequence != identity.Sequence || !reflect.DeepEqual(s.Applied, target) {
		return profileReferenceConflict()
	}

	changes, err := c.profileReferenceFinalizeChanges(ctx, identity)
	if err != nil {
		return err
	}

	err = c.releaseProfileReferenceChildren(ctx, identity)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `DELETE FROM profiles_reference_usage WHERE attempt_token=? OR attempt_token IN (SELECT token FROM profiles_reference_attempts WHERE instance_id=? AND member_id=? AND placement_revision=? AND sequence<=? AND phase='retryable') OR consumer_id IN (SELECT id FROM profiles_reference_consumers WHERE instance_id=? AND member_id=? AND placement_revision=? AND sequence<=? AND satisfied=0)`, identity.Token, identity.InstanceID, identity.MemberID, identity.PlacementRevision, identity.Sequence, identity.InstanceID, identity.MemberID, identity.PlacementRevision, identity.Sequence)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE profiles_reference_consumers SET satisfied=1 WHERE instance_id=? AND member_id=? AND placement_revision=? AND sequence<=? AND satisfied=0`, identity.InstanceID, identity.MemberID, identity.PlacementRevision, identity.Sequence)
	if err != nil {
		return err
	}

	for _, token := range changes {
		_, err = c.tx.ExecContext(ctx, `UPDATE profiles_reference_changes SET completed=1 WHERE token=? AND NOT EXISTS (SELECT 1 FROM profiles_reference_consumers WHERE change_token=profiles_reference_changes.token AND satisfied=0)`, token)
		if err != nil {
			return err
		}
	}
	_, err = c.tx.ExecContext(ctx, `INSERT INTO profiles_reference_receipts (attempt_token) VALUES (?)`, identity.Token)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE profiles_reference_attempts SET phase='completed' WHERE token=?`, identity.Token)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE instances_profile_reference_apply SET active_token='' WHERE instance_id=?`, identity.InstanceID)
	return err
}

// ProfileReferenceFailure is a driver's explicit outcome, never an inference from a returned error or absent token.
type ProfileReferenceFailure int

// ProfileReferenceUnknown retains reference protection when a driver's outcome is uncertain.
const (
	ProfileReferenceUnknown ProfileReferenceFailure = iota
	ProfileReferenceBeforeDispatch
	ProfileReferenceRolledBack
)

// FailProfileReferenceApply keeps usage on every failure and keeps admission on uncertain outcomes.
func (c *ClusterTx) FailProfileReferenceApply(ctx context.Context, identity ProfileReferenceApply, outcome ProfileReferenceFailure) error {
	phase, sealed, _, _, err := c.profileReferenceAttempt(ctx, identity)
	if err != nil {
		return err
	}

	if phase != "applying" && phase != "applied" {
		return profileReferenceConflict()
	}

	if outcome == ProfileReferenceUnknown {
		_, err = c.tx.ExecContext(ctx, `UPDATE profiles_reference_attempts SET phase='recovery' WHERE token=?`, identity.Token)
		return err
	}

	if phase != "applying" || (outcome != ProfileReferenceBeforeDispatch && outcome != ProfileReferenceRolledBack) || (!sealed && outcome == ProfileReferenceRolledBack) {
		return profileReferenceConflict()
	}

	err = c.releaseProfileReferenceChildren(ctx, identity)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE profiles_reference_attempts SET phase='retryable' WHERE token=?`, identity.Token)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `UPDATE instances_profile_reference_apply SET active_token='' WHERE instance_id=?`, identity.InstanceID)
	return err
}
