//go:build linux && cgo && !agent

package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/util"
)

// ProfileReferenceUpdate is an internal, version-checked profile commit, not a public completion capability.
type ProfileReferenceUpdate struct {
	Project    string
	Name       string
	ProfileID  int64
	Generation int64
	Profile    api.ProfilePut
}

// ProfileReferenceCommit reports saved desired state even if subsequent application fails.
type ProfileReferenceCommit struct {
	Token      string
	ProfileID  int64
	Generation int64
	Consumers  []db.ProfileReferenceConsumer
}

// CaptureProfileReferenceSnapshot uses production whole-device expansion and named project resolution.
func CaptureProfileReferenceSnapshot(ctx context.Context, tx *db.ClusterTx, instanceID int64) (*db.ProfileReferenceSnapshot, error) {
	return captureProfileReferenceSnapshot(ctx, tx, instanceID, nil, 0)
}

func captureProfileReferenceSnapshot(ctx context.Context, tx *db.ClusterTx, instanceID int64, replacement *api.Profile, generation int64) (*db.ProfileReferenceSnapshot, error) {
	snapshot, _, err := loadProfileReferenceInputs(ctx, tx, instanceID)
	if err != nil {
		return nil, err
	}

	for i := range snapshot.Profiles {
		profile := &snapshot.Profiles[i]
		if replacement != nil && profile.Profile.Project == replacement.Project && profile.Profile.Name == replacement.Name {
			profile.Profile = *replacement
			profile.Generation = generation
		}
	}
	return buildProfileReferenceSnapshot(ctx, tx, *snapshot)
}

func profileReferenceInputConflict() error {
	return api.StatusErrorf(http.StatusConflict, "Instance reference inputs changed or are unsupported")
}

// loadProfileReferenceInputs preserves the mapper's historical snapshot representation.
func loadProfileReferenceInputs(ctx context.Context, tx *db.ClusterTx, instanceID int64) (*db.ProfileReferenceSnapshot, db.InstanceReferenceFields, error) {
	var fields db.InstanceReferenceFields
	id := int(instanceID)
	instances, err := cluster.GetInstances(ctx, tx.Tx(), cluster.InstanceFilter{ID: &id})
	if err != nil {
		return nil, fields, err
	}

	if len(instances) != 1 {
		return nil, fields, profileReferenceInputConflict()
	}

	record := instances[0]
	projectRecord, err := cluster.GetProject(ctx, tx.Tx(), record.Project)
	if err != nil {
		return nil, fields, err
	}

	project, err := projectRecord.ToAPI(ctx, tx.Tx())
	if err != nil {
		return nil, fields, err
	}

	snapshot := &db.ProfileReferenceSnapshot{Version: 1, InstanceID: instanceID, ProjectID: int64(projectRecord.ID), Project: *project}
	err = tx.Tx().QueryRowContext(ctx, `SELECT node_id FROM instances WHERE id=?`, instanceID).Scan(&snapshot.MemberID)
	if err != nil {
		return nil, fields, err
	}

	profiles, err := cluster.GetInstanceProfiles(ctx, tx.Tx(), id)
	if err != nil {
		return nil, fields, err
	}

	snapshot.Profiles, err = profileReferenceProfiles(ctx, tx, profiles)
	if err != nil {
		return nil, fields, err
	}

	snapshot.LocalConfig, err = cluster.GetInstanceConfig(ctx, tx.Tx(), id)
	if err != nil {
		return nil, fields, err
	}

	devices, err := cluster.GetInstanceDevices(ctx, tx.Tx(), id)
	if err != nil {
		return nil, fields, err
	}

	snapshot.LocalDevices = cluster.DevicesToAPI(devices)
	// GetInstances selects the live instances table, never the separate snapshots table.
	fields = db.InstanceReferenceFields{
		Name: record.Name, Type: record.Type, Snapshot: record.Snapshot,
		Architecture: record.Architecture, Description: record.Description, Ephemeral: record.Ephemeral,
		Stateful: record.Stateful, CreationDate: record.CreationDate, LastUsedDate: record.LastUseDate,
		ExpiryDate: record.ExpiryDate, BaseImage: snapshot.LocalConfig["volatile.base_image"],
	}

	return snapshot, normalizeProfileReferenceFields(fields), nil
}

func normalizeProfileReferenceFields(fields db.InstanceReferenceFields) db.InstanceReferenceFields {
	fields.CreationDate = fields.CreationDate.Round(0).UTC()
	fields.LastUsedDate.Time = fields.LastUsedDate.Time.Round(0).UTC()
	fields.ExpiryDate.Time = fields.ExpiryDate.Time.Round(0).UTC()
	return fields
}

func profileReferenceProfiles(ctx context.Context, tx *db.ClusterTx, profiles []cluster.Profile) ([]db.ProfileReferenceProfile, error) {
	result := make([]db.ProfileReferenceProfile, 0, len(profiles))
	for _, profile := range profiles {
		value, err := profile.ToAPI(ctx, tx.Tx(), nil, nil)
		if err != nil {
			return nil, err
		}

		var generation int64
		err = tx.Tx().QueryRowContext(ctx, `SELECT reference_generation FROM profiles WHERE id=?`, profile.ID).Scan(&generation)
		if err != nil {
			return nil, err
		}

		result = append(result, db.ProfileReferenceProfile{ID: int64(profile.ID), Generation: generation, Profile: *value})
	}

	return result, nil
}

func cloneProfileReferenceDevices(devices map[string]map[string]string) map[string]map[string]string {
	result := make(map[string]map[string]string, len(devices))
	for name, device := range devices {
		result[name] = maps.Clone(device)
	}

	return result
}

// buildProfileReferenceSnapshot is shared by attached, replacement and ordinary proposed inputs.
func buildProfileReferenceSnapshot(ctx context.Context, tx *db.ClusterTx, input db.ProfileReferenceSnapshot) (*db.ProfileReferenceSnapshot, error) {
	snapshot := new(db.ProfileReferenceSnapshot)
	*snapshot = input
	snapshot.Project.Config = maps.Clone(input.Project.Config)
	snapshot.Project.UsedBy = slices.Clone(input.Project.UsedBy)
	snapshot.LocalConfig = maps.Clone(input.LocalConfig)
	snapshot.LocalDevices = cloneProfileReferenceDevices(input.LocalDevices)
	snapshot.Profiles = make([]db.ProfileReferenceProfile, 0, len(input.Profiles))
	snapshot.Resources = []db.ProfileReferenceResource{}
	apiProfiles := make([]api.Profile, 0, len(input.Profiles))
	for _, profile := range input.Profiles {
		profile.Profile.Config = maps.Clone(profile.Profile.Config)
		profile.Profile.Devices = cloneProfileReferenceDevices(profile.Profile.Devices)
		profile.Profile.UsedBy = slices.Clone(profile.Profile.UsedBy)
		snapshot.Profiles = append(snapshot.Profiles, profile)
		apiProfiles = append(apiProfiles, profile.Profile)
	}

	snapshot.ExpandedConfig = cluster.ExpandInstanceConfig(snapshot.LocalConfig, apiProfiles)
	snapshot.ExpandedDevices = cluster.ExpandInstanceDevices(deviceConfig.NewDevices(snapshot.LocalDevices), apiProfiles).CloneNative()
	names := make([]string, 0, len(snapshot.ExpandedDevices))
	for name := range snapshot.ExpandedDevices {
		names = append(names, name)
	}

	slices.Sort(names)
	for _, name := range names {
		device := snapshot.ExpandedDevices[name]
		if device["type"] != "nic" || device["network"] == "" {
			continue
		}

		networkProject := NetworkProjectForNameFromRecord(&snapshot.Project, device["network"])
		networkID, network, _, err := tx.GetNetworkInAnyState(ctx, networkProject, device["network"])
		if err != nil {
			return nil, err
		}

		networkProjectID, err := cluster.GetProjectID(ctx, tx.Tx(), networkProject)
		if err != nil {
			return nil, err
		}

		resource := db.ProfileReferenceResource{Device: name, NetworkID: networkID, NetworkProjectID: networkProjectID, NetworkProject: networkProject, NetworkName: network.Name, NetworkType: network.Type, Config: device}
		// Every managed NIC retains network ownership even without an explicit ACL.
		snapshot.Resources = append(snapshot.Resources, resource)
		acls := util.SplitNTrimSpace(device["security.acls"], ",", -1, true)
		slices.Sort(acls)
		for _, acl := range acls {
			aclID, err := cluster.GetNetworkACLID(ctx, tx.Tx(), networkProject, acl)
			if err != nil {
				return nil, err
			}

			resource.ACLID = aclID
			resource.ACLProjectID = networkProjectID
			snapshot.Resources = append(snapshot.Resources, resource)
		}
	}
	return snapshot, nil
}

// CaptureOrdinaryProfileReferenceInputs binds current inputs and explicitly selected profiles.
// Nil or empty names selects no profiles; compatibility callers must supply attached names.
func CaptureOrdinaryProfileReferenceInputs(ctx context.Context, tx *db.ClusterTx, instanceID int64, selectedProfileNames []string) (*db.OrdinaryProfileReferenceUpdate, error) {
	current, fields, err := loadProfileReferenceInputs(ctx, tx, instanceID)
	if err != nil {
		return nil, err
	}

	current, err = buildProfileReferenceSnapshot(ctx, tx, *current)
	if err != nil {
		return nil, err
	}

	state, err := tx.ProfileReferenceState(ctx, instanceID)
	if err != nil {
		return nil, err
	}

	if fields.Snapshot || state.Applied.Version != 1 || state.Desired.Version != 1 ||
		state.ProjectID != current.ProjectID || state.MemberID != current.MemberID ||
		!db.ProfileReferenceInputsEqual(*current, state.Desired) {
		return nil, profileReferenceInputConflict()
	}

	profiles, err := selectedProfileReferenceProfiles(ctx, tx, current.Project.Name, selectedProfileNames)
	if err != nil {
		return nil, err
	}

	return &db.OrdinaryProfileReferenceUpdate{
		Expected: db.InstanceUpdatePrecondition{
			Version: 1, Current: *current, Fields: fields,
			DesiredSequence: state.DesiredSequence, AppliedSequence: state.AppliedSequence, PlacementRevision: state.PlacementRevision,
		},
		Config: maps.Clone(current.LocalConfig), Devices: cloneProfileReferenceDevices(current.LocalDevices), Profiles: profiles, Fields: fields,
	}, nil
}

func selectedProfileReferenceProfiles(ctx context.Context, tx *db.ClusterTx, projectName string, names []string) ([]db.ProfileReferenceProfile, error) {
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return nil, profileReferenceInputConflict()
		}

		seen[name] = true
	}

	records, err := cluster.GetProfilesIfEnabled(ctx, tx.Tx(), projectName, names)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, profileReferenceInputConflict()
		}

		return nil, err
	}

	return profileReferenceProfiles(ctx, tx, records)
}

// ClaimOrdinaryProfileReferenceApply admits only reference edits against the captured v1 read.
func ClaimOrdinaryProfileReferenceApply(ctx context.Context, tx *db.ClusterTx, identity db.ProfileReferenceApply, proposed db.OrdinaryProfileReferenceUpdate) error {
	expected := proposed.Expected
	if identity.ChangeToken != "" || expected.Version != 1 || expected.Current.Version != 1 ||
		expected.InputRevision != 0 || expected.ObservationWatermark != 0 || expected.Fields.Snapshot || proposed.Fields.Snapshot {
		return profileReferenceInputConflict()
	}

	current, fields, err := loadProfileReferenceInputs(ctx, tx, identity.InstanceID)
	if err != nil {
		return err
	}

	current, err = buildProfileReferenceSnapshot(ctx, tx, *current)
	if err != nil {
		return err
	}

	state, err := tx.ProfileReferenceState(ctx, identity.InstanceID)
	if err != nil {
		return err
	}

	if fields.Snapshot || state.Applied.Version != 1 || state.Desired.Version != 1 ||
		identity.InstanceID != expected.Current.InstanceID || identity.ProjectID != current.ProjectID || identity.MemberID != current.MemberID ||
		identity.PlacementRevision != expected.PlacementRevision || identity.Sequence != expected.DesiredSequence+1 ||
		state.ProjectID != current.ProjectID || state.MemberID != current.MemberID ||
		state.PlacementRevision != expected.PlacementRevision || state.DesiredSequence != expected.DesiredSequence || state.AppliedSequence != expected.AppliedSequence ||
		!db.ProfileReferenceInputsEqual(*current, expected.Current) || !db.ProfileReferenceInputsEqual(*current, state.Desired) {
		return profileReferenceInputConflict()
	}

	proposalFields := normalizeProfileReferenceFields(proposed.Fields)
	if proposalFields.Architecture == 0 {
		proposalFields.Architecture = fields.Architecture
	}

	if fields != normalizeProfileReferenceFields(expected.Fields) || proposalFields != fields || proposed.Config["volatile.base_image"] != fields.BaseImage {
		return profileReferenceInputConflict()
	}

	names := make([]string, 0, len(proposed.Profiles))
	ids := map[int64]bool{}
	for _, profile := range proposed.Profiles {
		if profile.ID <= 0 || ids[profile.ID] {
			return profileReferenceInputConflict()
		}

		ids[profile.ID] = true
		names = append(names, profile.Profile.Name)
	}

	profiles, err := selectedProfileReferenceProfiles(ctx, tx, current.Project.Name, names)
	if err != nil {
		return err
	}

	for i := range profiles {
		if !reflect.DeepEqual(profiles[i], proposed.Profiles[i]) {
			return profileReferenceInputConflict()
		}
	}
	config := make(map[string]string, len(proposed.Config))
	for key, value := range proposed.Config {
		if value != "" {
			config[key] = value
		}
	}
	input := *current
	input.LocalConfig = config
	input.LocalDevices = deviceConfig.NewDevices(proposed.Devices).CloneNative()
	input.Profiles = profiles
	target, err := buildProfileReferenceSnapshot(ctx, tx, input)
	if err != nil {
		return err
	}

	err = ValidateDeviceNetworkReferences(ctx, tx, target.Project.Name, deviceConfig.NewDevices(target.ExpandedDevices))
	if err != nil {
		return err
	}

	return tx.ClaimProfileReferenceApply(ctx, identity, *target)
}

// CaptureAndCommitProfileReferenceUpdate saves actual profile data and durable consumers in one transaction.
// Syntax and device/root-disk validation remain the later caller's responsibility before activation.
func CaptureAndCommitProfileReferenceUpdate(ctx context.Context, tx *db.ClusterTx, request ProfileReferenceUpdate) (*ProfileReferenceCommit, error) {
	// Match mapper normalization without mutating caller-owned input maps.
	config := map[string]string{}
	for key, value := range request.Profile.Config {
		if value != "" {
			config[key] = value
		}
	}
	request.Profile.Config = config
	request.Profile.Devices = deviceConfig.NewDevices(request.Profile.Devices).CloneNative()
	projectRecord, err := cluster.GetProject(ctx, tx.Tx(), request.Project)
	if err != nil {
		return nil, err
	}

	project, err := projectRecord.ToAPI(ctx, tx.Tx())
	if err != nil {
		return nil, err
	}

	profileProject := ProfileProjectFromRecord(project)
	profile, err := cluster.GetProfile(ctx, tx.Tx(), profileProject, request.Name)
	if err != nil {
		return nil, err
	}

	var generation int64
	err = tx.Tx().QueryRowContext(ctx, `SELECT reference_generation FROM profiles WHERE id=?`, profile.ID).Scan(&generation)
	if err != nil {
		return nil, err
	}

	if request.ProfileID != int64(profile.ID) || generation != request.Generation {
		return nil, api.StatusErrorf(http.StatusConflict, "Profile reference version changed")
	}

	oldProfile, err := profile.ToAPI(ctx, tx.Tx(), nil, nil)
	if err != nil {
		return nil, err
	}

	if reflect.DeepEqual(oldProfile.ProfilePut, request.Profile) {
		commit := &ProfileReferenceCommit{ProfileID: int64(profile.ID), Generation: generation}
		if generation > 0 {
			err = tx.Tx().QueryRowContext(ctx, `SELECT token FROM profiles_reference_changes WHERE profile_id=? AND generation=?`, profile.ID, generation).Scan(&commit.Token)
			if err != nil {
				return nil, err
			}

			commit.Consumers, err = tx.ProfileReferenceConsumers(ctx, commit.Token)
		}

		return commit, err
	}

	var shared bool
	err = tx.Tx().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM networks_ovn_operations WHERE project_id=(SELECT id FROM projects WHERE name=?) AND name=?)`, api.ProjectDefaultName, db.OVNPeerOperationName).Scan(&shared)
	if err != nil {
		return nil, err
	}

	if shared {
		return nil, api.StatusErrorf(http.StatusConflict, "Profile commit conflicts with a shared resource writer")
	}

	err = AllowProfileUpdate(tx, profileProject, request.Name, request.Profile)
	if err != nil {
		return nil, err
	}

	err = ValidateDeviceNetworkReferences(ctx, tx, profileProject, deviceConfig.NewDevices(request.Profile.Devices))
	if err != nil {
		return nil, err
	}

	rows, err := tx.Tx().QueryContext(ctx, `SELECT instance_id FROM instances_profiles WHERE profile_id=? ORDER BY instance_id`, profile.ID)
	if err != nil {
		return nil, err
	}

	ids := []int64{}
	for rows.Next() {
		var instanceID int64
		err = rows.Scan(&instanceID)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}

		ids = append(ids, instanceID)
	}

	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}

	commit := &ProfileReferenceCommit{Token: uuid.NewString(), ProfileID: int64(profile.ID), Generation: generation + 1, Consumers: []db.ProfileReferenceConsumer{}}
	replacement := &api.Profile{Name: profile.Name, Project: profileProject, ProfilePut: request.Profile}
	for _, instanceID := range ids {
		state, err := tx.ProfileReferenceState(ctx, instanceID)
		if err != nil {
			return nil, err
		}

		if state.ActiveToken != "" {
			return nil, api.StatusErrorf(http.StatusConflict, "Profile consumer already has an admitted writer")
		}

		before, err := CaptureProfileReferenceSnapshot(ctx, tx, instanceID)
		if err != nil {
			return nil, err
		}

		after, err := captureProfileReferenceSnapshot(ctx, tx, instanceID, replacement, generation+1)
		if err != nil {
			return nil, err
		}

		err = ValidateDeviceNetworkReferences(ctx, tx, before.Project.Name, deviceConfig.NewDevices(after.ExpandedDevices))
		if err != nil {
			return nil, err
		}

		commit.Consumers = append(commit.Consumers, db.ProfileReferenceConsumer{ChangeToken: commit.Token, InstanceID: instanceID, ProjectID: before.ProjectID, MemberID: before.MemberID, PlacementRevision: state.PlacementRevision, Sequence: state.DesiredSequence + 1, Before: *before, After: *after})
	}

	err = tx.CreateProfileReferenceChange(ctx, commit.Token, int64(profile.ID), int64(profile.ProjectID), generation+1, oldProfile.ProfilePut, request.Profile, commit.Consumers)
	if err != nil {
		return nil, err
	}

	devices, err := cluster.APIToDevices(request.Profile.Devices)
	if err != nil {
		return nil, err
	}

	err = cluster.UpdateProfile(ctx, tx.Tx(), profileProject, profile.Name, cluster.Profile{Project: profileProject, Name: profile.Name, Description: request.Profile.Description})
	if err != nil {
		return nil, err
	}

	err = cluster.UpdateProfileConfig(ctx, tx.Tx(), int64(profile.ID), request.Profile.Config)
	if err != nil {
		return nil, err
	}

	err = cluster.UpdateProfileDevices(ctx, tx.Tx(), int64(profile.ID), devices)
	if err != nil {
		return nil, err
	}

	_, err = tx.Tx().ExecContext(ctx, `UPDATE profiles SET reference_generation=? WHERE id=?`, generation+1, profile.ID)
	if err != nil {
		return nil, err
	}

	commit.Consumers, err = tx.ProfileReferenceConsumers(ctx, commit.Token)
	return commit, err
}

// CommitProfileReferenceUpdate exposes the production post-commit boundary for future orchestration.
// It is intentionally unwired; delivery success does not acknowledge any consumer.
func CommitProfileReferenceUpdate(ctx context.Context, database *db.Cluster, request ProfileReferenceUpdate, dispatch func(context.Context, db.ProfileReferenceConsumer) error) (*ProfileReferenceCommit, error) {
	var commit *ProfileReferenceCommit
	err := database.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		commit, err = CaptureAndCommitProfileReferenceUpdate(ctx, tx, request)
		return err
	})
	if err != nil {
		return nil, err
	}

	if dispatch == nil {
		return commit, nil
	}

	failures := []error{}
	for _, consumer := range commit.Consumers {
		if consumer.Satisfied {
			continue
		}

		err = dispatch(ctx, consumer)
		if err != nil {
			failures = append(failures, err)
		}
	}
	return commit, errors.Join(failures...)
}

// ClaimProfileReferenceApply revalidates current profile order, local inputs and resolution before admission.
func ClaimProfileReferenceApply(ctx context.Context, tx *db.ClusterTx, identity db.ProfileReferenceApply) error {
	if identity.ChangeToken == "" {
		attached, err := cluster.GetInstanceProfiles(ctx, tx.Tx(), int(identity.InstanceID))
		if err != nil {
			return err
		}

		names := make([]string, 0, len(attached))
		for _, profile := range attached {
			names = append(names, profile.Name)
		}

		proposed, err := CaptureOrdinaryProfileReferenceInputs(ctx, tx, identity.InstanceID, names)
		if err != nil {
			return err
		}
		// This same-transaction compatibility path cannot protect stale external request reads.
		return ClaimOrdinaryProfileReferenceApply(ctx, tx, identity, *proposed)
	}

	current, err := CaptureProfileReferenceSnapshot(ctx, tx, identity.InstanceID)
	if err != nil {
		return err
	}

	state, err := tx.ProfileReferenceState(ctx, identity.InstanceID)
	if err != nil {
		return err
	}

	if !db.ProfileReferenceInputsEqual(*current, state.Desired) {
		return api.StatusErrorf(http.StatusConflict, "Instance desired inputs changed before profile application")
	}

	err = ValidateDeviceNetworkReferences(ctx, tx, current.Project.Name, deviceConfig.NewDevices(current.ExpandedDevices))
	if err != nil {
		return err
	}
	// A profile attempt retains the exact persisted consumer target.
	return tx.ClaimProfileReferenceApply(ctx, identity, state.Desired)
}

// allowProjectReferenceUpdate protects project resolution inputs until durable instance work settles.
func allowProjectReferenceUpdate(ctx context.Context, tx *db.ClusterTx, projectName string, config map[string]string) error {
	current, err := cluster.GetProject(ctx, tx.Tx(), projectName)
	if err != nil {
		return err
	}

	currentConfig, err := cluster.GetProjectConfig(ctx, tx.Tx(), current.ID)
	if err != nil {
		return err
	}

	// The caller's changed keys can reflect an earlier snapshot than this transaction.
	sensitiveChanged := false
	for _, key := range []string{"features.profiles", "features.networks", "restricted", "restricted.networks.access"} {
		if currentConfig[key] != config[key] {
			sensitiveChanged = true
			break
		}
	}

	if !sensitiveChanged {
		return nil
	}

	// Preserve both durable ownership and exact current ownership even when placement disagrees.
	var pending bool
	err = tx.Tx().QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM (
        SELECT instance_id, project_id FROM profiles_reference_consumers WHERE satisfied=0
        UNION ALL
        SELECT instance_id, project_id FROM instances_profile_reference_apply
            WHERE active_token!='' OR desired_sequence>applied_sequence
        UNION ALL
        SELECT instance_id, project_id FROM profiles_reference_attempts WHERE phase IN ('applying', 'applied', 'recovery')
        UNION ALL
        SELECT instance_id, project_id FROM profiles_reference_usage
    ) AS pending
    LEFT JOIN instances ON instances.id=pending.instance_id
    WHERE pending.project_id=? OR instances.project_id=?
)`, current.ID, current.ID).Scan(&pending)
	if err != nil {
		return err
	}

	if pending {
		return api.StatusErrorf(http.StatusConflict, "Project %q has unresolved profile reference work", projectName)
	}

	return nil
}

// RenameProfileWithReferences preserves identities in current and retained reference accounting.
func RenameProfileWithReferences(ctx context.Context, tx *db.ClusterTx, projectName, name, to string) error {
	profileID, err := cluster.GetProfileID(ctx, tx.Tx(), projectName, name)
	if err != nil {
		return err
	}

	err = guardProfileReferenceIdentity(ctx, tx, profileID, false)
	if err != nil {
		return err
	}

	return cluster.RenameProfile(ctx, tx.Tx(), projectName, name, to)
}

// DeleteProfileWithReferences uses the literal project even after its profile feature changes.
func DeleteProfileWithReferences(ctx context.Context, tx *db.ClusterTx, projectName, name string) error {
	profileID, err := cluster.GetProfileID(ctx, tx.Tx(), projectName, name)
	if err != nil {
		return err
	}

	err = guardProfileReferenceIdentity(ctx, tx, profileID, true)
	if err != nil {
		return err
	}

	return cluster.DeleteProfile(ctx, tx.Tx(), projectName, name)
}

func profileReferenceIdentityConflict() error {
	return api.StatusErrorf(http.StatusConflict, "Profile identity is retained by reference accounting or cannot be verified")
}

// Reject duplicate object keys so contradictory identities cannot disappear during JSON decoding.
func profileReferenceIdentityObject(data json.RawMessage) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, profileReferenceIdentityConflict()
	}

	object := map[string]json.RawMessage{}
	keys := map[string]bool{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, profileReferenceIdentityConflict()
		}

		key, ok := token.(string)
		if !ok || keys[strings.ToLower(key)] {
			return nil, profileReferenceIdentityConflict()
		}

		keys[strings.ToLower(key)] = true
		var value json.RawMessage
		err = decoder.Decode(&value)
		if err != nil {
			return nil, profileReferenceIdentityConflict()
		}

		object[key] = value
	}

	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return nil, profileReferenceIdentityConflict()
	}

	_, err = decoder.Token()
	if err != io.EOF {
		return nil, profileReferenceIdentityConflict()
	}

	return object, nil
}

// Only numeric IDs prove profile ownership; saved names are never resolved against live profiles.
func profileReferenceIdentitySnapshot(data string, instanceID, projectID, memberID, profileID int64) error {
	object, err := profileReferenceIdentityObject(json.RawMessage(data))
	if err != nil {
		return err
	}

	for key, expected := range map[string]int64{"Version": 1, "InstanceID": instanceID, "ProjectID": projectID, "MemberID": memberID} {
		var value int64
		err = json.Unmarshal(object[key], &value)
		if err != nil || expected <= 0 || value != expected {
			return profileReferenceIdentityConflict()
		}
	}
	var profiles []json.RawMessage
	err = json.Unmarshal(object["Profiles"], &profiles)
	if err != nil {
		return profileReferenceIdentityConflict()
	}

	seen := map[int64]bool{}
	names := map[[2]string]int64{}
	retained := false
	for _, data := range profiles {
		entry, err := profileReferenceIdentityObject(data)
		if err != nil {
			return err
		}

		var id int64
		err = json.Unmarshal(entry["ID"], &id)
		if err != nil || id <= 0 || seen[id] {
			return profileReferenceIdentityConflict()
		}

		seen[id] = true
		// Check present names for contradictions, without requiring or inferring an ID from them.
		if entry["Profile"] != nil {
			value, err := profileReferenceIdentityObject(entry["Profile"])
			if err != nil {
				return err
			}

			var identity [2]string
			for i, key := range []string{"project", "name"} {
				if value[key] != nil {
					err = json.Unmarshal(value[key], &identity[i])
					if err != nil {
						return profileReferenceIdentityConflict()
					}
				}
			}

			if identity[0] != "" && identity[1] != "" {
				if names[identity] != 0 {
					return profileReferenceIdentityConflict()
				}

				names[identity] = id
			}
		}
		retained = retained || id == profileID
	}

	if retained {
		return profileReferenceIdentityConflict()
	}

	return nil
}

func guardProfileReferenceIdentity(ctx context.Context, tx *db.ClusterTx, profileID int64, deleting bool) error {
	var changed bool
	// Completed changes still impose the existing deletion FK, but alone do not prohibit rename.
	err := tx.Tx().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM profiles_reference_changes WHERE profile_id=? AND (completed!=1 OR ?))`, profileID, deleting).Scan(&changed)
	if err != nil {
		return err
	}

	if changed {
		return profileReferenceIdentityConflict()
	}
	// Normalize selected v1 columns, excluding inert history before interpreting its payload.
	rows, err := tx.Tx().QueryContext(ctx, `
SELECT instance_id, project_id, member_id, applied_snapshot, desired_snapshot,
       contract_version, input_revision, materialized_observation_id, 0, '', '', '', '', 'applying'
FROM instances_profile_reference_apply
UNION ALL
SELECT instance_id, project_id, member_id, before_snapshot, after_snapshot,
       1, 0, 0, 0, '', '', '', '', 'applying'
FROM profiles_reference_consumers AS consumer
WHERE satisfied!=1 OR EXISTS (SELECT 1 FROM profiles_reference_usage WHERE consumer_id=consumer.id)
UNION ALL
SELECT attempt.instance_id, attempt.project_id, attempt.member_id, baseline_snapshot, target_snapshot,
       contract_version, claimed_input_revision, result_input_revision, observation_cutoff,
       generation_plan, attempt.result_snapshot, COALESCE(receipt.result_snapshot, ''),
       COALESCE(receipt.post_commit_evidence, ''), phase
FROM profiles_reference_attempts AS attempt
LEFT JOIN profiles_reference_receipts AS receipt ON receipt.attempt_token=attempt.token
WHERE phase NOT IN ('retryable', 'completed')
   OR EXISTS (SELECT 1 FROM instances_profile_reference_apply WHERE active_token=attempt.token)
   OR EXISTS (SELECT 1 FROM profiles_reference_usage WHERE attempt_token=attempt.token)
   OR EXISTS (SELECT 1 FROM profiles_reference_children WHERE attempt_token=attempt.token)`)
	if err != nil {
		return err
	}

	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var instanceID, projectID, memberID, version, revision, resultRevision, observation int64
		var before, after, plan, result, receipt, evidence, phase string
		err = rows.Scan(&instanceID, &projectID, &memberID, &before, &after, &version, &revision, &resultRevision, &observation, &plan, &result, &receipt, &evidence, &phase)
		if err != nil {
			return err
		}

		if version != 1 || revision != 0 || resultRevision != 0 || observation != 0 || plan != "" || result != "" || receipt != "" || evidence != "" ||
			!slices.Contains([]string{"applying", "applied", "recovery", "retryable", "completed"}, phase) {
			return profileReferenceIdentityConflict()
		}

		for _, snapshot := range []string{before, after} {
			err = profileReferenceIdentitySnapshot(snapshot, instanceID, projectID, memberID, profileID)
			if err != nil {
				return err
			}
		}
	}

	return rows.Err()
}

// CheckProjectReferenceDeletion protects exact FK dependencies before project deletion effects.
func CheckProjectReferenceDeletion(ctx context.Context, tx *db.ClusterTx, projectID int64) error {
	if projectID <= 0 {
		return api.StatusErrorf(http.StatusConflict, "Project identity changed before deletion")
	}

	var exists bool
	err := tx.Tx().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id=?)`, projectID).Scan(&exists)
	if err != nil {
		return err
	}

	if !exists {
		return api.StatusErrorf(http.StatusNotFound, "Project not found")
	}

	// Recorded owners and current entity owners independently protect every retained accounting row.
	var retained bool
	err = tx.Tx().QueryRowContext(ctx, `
WITH target(id) AS (VALUES (?))
SELECT EXISTS (
    SELECT 1 FROM instances_profile_reference_apply AS state
    LEFT JOIN instances AS instance ON instance.id=state.instance_id
    CROSS JOIN target
    WHERE state.project_id=target.id OR instance.project_id=target.id
    UNION ALL
    SELECT 1 FROM profiles_reference_changes AS change
    LEFT JOIN profiles AS profile ON profile.id=change.profile_id
    CROSS JOIN target
    WHERE change.project_id=target.id OR profile.project_id=target.id
    UNION ALL
    SELECT 1 FROM profiles_reference_consumers AS consumer
    LEFT JOIN instances AS instance ON instance.id=consumer.instance_id
    CROSS JOIN target
    WHERE consumer.project_id=target.id OR instance.project_id=target.id
    UNION ALL
    SELECT 1 FROM profiles_reference_attempts AS attempt
    LEFT JOIN instances AS instance ON instance.id=attempt.instance_id
    CROSS JOIN target
    WHERE attempt.project_id=target.id OR instance.project_id=target.id
    UNION ALL
    SELECT 1 FROM profiles_reference_children AS child
    LEFT JOIN networks AS network ON network.id=child.network_id
    CROSS JOIN target
    WHERE child.project_id=target.id OR network.project_id=target.id
    UNION ALL
    SELECT 1 FROM profiles_reference_usage AS usage
    LEFT JOIN instances AS instance ON instance.id=usage.instance_id
    LEFT JOIN networks AS network ON network.id=usage.network_id
    LEFT JOIN networks_acls AS acl ON acl.id=usage.acl_id
    CROSS JOIN target
    WHERE usage.project_id=target.id OR usage.network_project_id=target.id OR usage.acl_project_id=target.id
       OR instance.project_id=target.id OR network.project_id=target.id OR acl.project_id=target.id
)`, projectID).Scan(&retained)
	if err != nil {
		return err
	}

	if retained {
		return api.StatusErrorf(http.StatusConflict, "Project has retained profile reference accounting")
	}

	return nil
}

// DeleteProjectWithReferences rechecks the admitted identity and dependencies in the delete transaction.
func DeleteProjectWithReferences(ctx context.Context, tx *db.ClusterTx, projectName string, expectedProjectID int64) error {
	projectID, err := cluster.GetProjectID(ctx, tx.Tx(), projectName)
	if err != nil {
		return err
	}

	if expectedProjectID <= 0 || projectID <= 0 || projectID != expectedProjectID {
		return api.StatusErrorf(http.StatusConflict, "Project identity changed before deletion")
	}

	err = CheckProjectReferenceDeletion(ctx, tx, projectID)
	if err != nil {
		return err
	}

	return cluster.DeleteProject(ctx, tx.Tx(), projectName)
}
