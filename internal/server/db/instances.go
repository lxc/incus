//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/query"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/shared/api"
)

// InstanceArgs is a value object holding all db-related details about an instance.
type InstanceArgs struct {
	// Don't set manually
	ID       int
	Node     string
	Type     instancetype.Type
	Snapshot bool

	// Creation only
	Project      string
	BaseImage    string
	CreationDate time.Time

	Architecture int
	Config       map[string]string
	Description  string
	Devices      deviceConfig.Devices
	Ephemeral    bool
	LastUsedDate time.Time
	Name         string
	Profiles     []api.Profile
	Stateful     bool
	ExpiryDate   time.Time
}

// GetInstanceNames returns the names of all containers the given project.
func (c *ClusterTx) GetInstanceNames(ctx context.Context, project string) ([]string, error) {
	stmt := `
SELECT instances.name FROM instances
  JOIN projects ON projects.id = instances.project_id
  WHERE projects.name = ?
`
	return query.SelectStrings(ctx, c.tx, stmt, project)
}

// GetNodeAddressOfInstance returns the address of the node hosting the
// instance with the given name in the given project.
//
// It returns the empty string if the container is hosted on this node.
func (c *ClusterTx) GetNodeAddressOfInstance(ctx context.Context, project string, name string, instType instancetype.Type) (string, error) {
	var stmt string

	args := make([]any, 0, 4) // Expect up to 4 filters.
	var filters strings.Builder

	// Project filter.
	filters.WriteString("projects.name = ?")
	args = append(args, project)

	// Instance type filter.
	if instType != instancetype.Any {
		filters.WriteString(" AND instances.type = ?")
		args = append(args, instType)
	}

	if strings.Contains(name, internalInstance.SnapshotDelimiter) {
		parts := strings.SplitN(name, internalInstance.SnapshotDelimiter, 2)

		// Instance name filter.
		filters.WriteString(" AND instances.name = ?")
		args = append(args, parts[0])

		// Snapshot name filter.
		filters.WriteString(" AND instances_snapshots.name = ?")
		args = append(args, parts[1])

		stmt = fmt.Sprintf(`
SELECT nodes.id, nodes.address
  FROM nodes
  JOIN instances ON instances.node_id = nodes.id
  JOIN projects ON projects.id = instances.project_id
  JOIN instances_snapshots ON instances_snapshots.instance_id = instances.id
 WHERE %s
`, filters.String())
	} else {
		// Instance name filter.
		filters.WriteString(" AND instances.name = ?")
		args = append(args, name)

		stmt = fmt.Sprintf(`
SELECT nodes.id, nodes.address
  FROM nodes
  JOIN instances ON instances.node_id = nodes.id
  JOIN projects ON projects.id = instances.project_id
 WHERE %s
`, filters.String())
	}

	var address string
	var id int64
	rows, err := c.tx.QueryContext(ctx, stmt, args...)
	if err != nil {
		return "", err
	}

	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return "", api.StatusErrorf(http.StatusNotFound, "Instance not found")
	}

	err = rows.Scan(&id, &address)
	if err != nil {
		return "", err
	}

	if rows.Next() {
		return "", fmt.Errorf("More than one cluster member associated with instance")
	}

	err = rows.Err()
	if err != nil {
		return "", err
	}

	if id == c.nodeID {
		return "", nil
	}

	return address, nil
}

// Instance represents basic instance info.
type Instance struct {
	ID       int64
	Name     string
	Project  string
	Location string
	Type     instancetype.Type
}

// GetInstancesByMemberAddress returns the instances associated to each cluster member address.
// The member address of instances running on the local member is set to the empty string, to distinguish it from
// remote nodes. Instances whose member is down are added to the special address "0.0.0.0".
func (c *ClusterTx) GetInstancesByMemberAddress(ctx context.Context, offlineThreshold time.Duration, projects []string, instType instancetype.Type) (map[string][]Instance, error) {
	args := make([]any, 0, 2) // Expect up to 2 filters.
	var q strings.Builder

	q.WriteString(`SELECT
		instances.id, instances.name, instances.type,
		nodes.id, nodes.name, nodes.address, nodes.heartbeat,
		projects.name
	FROM instances
	JOIN nodes ON nodes.id = instances.node_id
	JOIN projects ON projects.id = instances.project_id
	`)

	// Project filter.
	q.WriteString(fmt.Sprintf("WHERE projects.name IN %s", query.Params(len(projects))))
	for _, project := range projects {
		args = append(args, project)
	}

	// Instance type filter.
	if instType != instancetype.Any {
		q.WriteString(" AND instances.type = ?")
		args = append(args, instType)
	}

	q.WriteString(" ORDER BY instances.id")

	rows, err := c.tx.QueryContext(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	memberAddressInstances := make(map[string][]Instance)

	for rows.Next() {
		var inst Instance
		var memberAddress string
		var memberID int64
		var memberHeartbeat time.Time
		err := rows.Scan(&inst.ID, &inst.Name, &inst.Type, &memberID, &inst.Location, &memberAddress, &memberHeartbeat, &inst.Project)
		if err != nil {
			return nil, err
		}

		if memberID == c.nodeID {
			memberAddress = ""
		} else if nodeIsOffline(offlineThreshold, memberHeartbeat) {
			memberAddress = "0.0.0.0"
		}

		memberAddressInstances[memberAddress] = append(memberAddressInstances[memberAddress], inst)
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return memberAddressInstances, nil
}

// ErrInstanceListStop used as return value from InstanceList's instanceFunc when prematurely stopping the search.
var ErrInstanceListStop = fmt.Errorf("search stopped")

// InstanceList loads all instances across all projects and for each instance runs the instanceFunc passing in the
// instance and it's project and profiles. Accepts optional filter arguments to specify a subset of instances.
func (c *ClusterTx) InstanceList(ctx context.Context, instanceFunc func(inst InstanceArgs, project api.Project) error, filters ...cluster.InstanceFilter) error {
	projectsByName := make(map[string]*api.Project)
	var instances map[int]InstanceArgs

	emptyFilter := cluster.InstanceFilter{}
	validFilters := []cluster.InstanceFilter{}
	for _, filter := range filters {
		if filter.Type != nil && *filter.Type == instancetype.Any {
			filter.Type = nil
		}

		if filter != emptyFilter {
			validFilters = append(validFilters, filter)
		}
	}

	// Retrieve required info from the database in single transaction for performance.
	// Get all projects.
	projects, err := cluster.GetProjects(ctx, c.tx)
	if err != nil {
		return fmt.Errorf("Failed loading projects: %w", err)
	}

	// Get all instances using supplied filter.
	dbInstances, err := cluster.GetInstances(ctx, c.tx, validFilters...)
	if err != nil {
		return fmt.Errorf("Failed loading instances: %w", err)
	}

	// Fill instances with config, devices and profiles.
	instances, err = c.InstancesToInstanceArgs(ctx, true, dbInstances...)
	if err != nil {
		return err
	}

	// Record which projects are referenced by at least one instance in the list.
	for _, instance := range instances {
		_, ok := projectsByName[instance.Project]
		if !ok {
			projectsByName[instance.Project] = nil
		}
	}

	// Populate projectsByName map entry for referenced projects.
	// This way we only call ToAPI() on the projects actually referenced by the instances in
	// the list, which can reduce the number of queries run.
	for _, project := range projects {
		_, ok := projectsByName[project.Name]
		if !ok {
			continue
		}

		projectsByName[project.Name], err = project.ToAPI(ctx, c.tx)
		if err != nil {
			return err
		}
	}

	// Call the instanceFunc provided for each instance after the transaction has ended, as we don't know if
	// the instanceFunc will be slow or may need to make additional DB queries.
	for _, instance := range instances {
		project := projectsByName[instance.Project]
		if project == nil {
			return fmt.Errorf("Instance references %d project %q that isn't loaded", instance.ID, instance.Project)
		}

		err = instanceFunc(instance, *project)
		if err != nil {
			return err
		}
	}

	return nil
}

// instanceConfigFill function loads config for all specified instances in a single query and then updates
// the entries in the instances map.
func (c *ClusterTx) instanceConfigFill(ctx context.Context, snapshotsMode bool, instanceArgs *map[int]InstanceArgs) error {
	instances := *instanceArgs

	// Don't use query parameters for the IN statement to workaround an issue in Dqlite (apparently)
	// that means that >255 query parameters causes partial result sets. See #10705
	// This is safe as the inputs are ints.
	var q strings.Builder

	if snapshotsMode {
		q.WriteString(`SELECT
			instance_snapshot_id,
			key,
			value
		FROM instances_snapshots_config
		WHERE instance_snapshot_id IN (`)
	} else {
		q.WriteString(`SELECT
			instance_id,
			key,
			value
		FROM instances_config
		WHERE instance_id IN (`)
	}

	q.Grow(len(instances) * 2) // We know the minimum length of the separators and integers.

	first := true
	for instanceID := range instances {
		if !first {
			q.WriteString(",")
		}

		first = false

		q.WriteString(fmt.Sprintf("%d", instanceID))
	}

	q.WriteString(`)`)

	return query.Scan(ctx, c.Tx(), q.String(), func(scan func(dest ...any) error) error {
		var instanceID int
		var key, value string

		err := scan(&instanceID, &key, &value)
		if err != nil {
			return err
		}

		_, found := instances[instanceID]
		if !found {
			return fmt.Errorf("Failed loading instance config, referenced instance %d not loaded", instanceID)
		}

		if instances[instanceID].Config == nil {
			inst := instances[instanceID]
			inst.Config = make(map[string]string)
			instances[instanceID] = inst
		}

		_, found = instances[instanceID].Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for instance ID %d", key, instanceID)
		}

		instances[instanceID].Config[key] = value

		return nil
	})
}

// instanceDevicesFill loads the device config for all instances specified in a single query and then updates
// the entries in the instances map.
func (c *ClusterTx) instanceDevicesFill(ctx context.Context, snapshotsMode bool, instanceArgs *map[int]InstanceArgs) error {
	instances := *instanceArgs

	// Don't use query parameters for the IN statement to workaround an issue in Dqlite (apparently)
	// that means that >255 query parameters causes partial result sets. See #10705
	// This is safe as the inputs are ints.
	var q strings.Builder

	if snapshotsMode {
		q.WriteString(`
		SELECT
			instances_snapshots_devices.instance_snapshot_id AS instance_snapshot_id,
			instances_snapshots_devices.name AS device_name,
			instances_snapshots_devices.type AS device_type,
			instances_snapshots_devices_config.key,
			instances_snapshots_devices_config.value
		FROM instances_snapshots_devices_config
		JOIN instances_snapshots_devices ON instances_snapshots_devices.id = instances_snapshots_devices_config.instance_snapshot_device_id
		WHERE instances_snapshots_devices.instance_snapshot_id IN (`)
	} else {
		q.WriteString(`
		SELECT
			instances_devices.instance_id AS instance_id,
			instances_devices.name AS device_name,
			instances_devices.type AS device_type,
			instances_devices_config.key,
			instances_devices_config.value
		FROM instances_devices_config
		JOIN instances_devices ON instances_devices.id = instances_devices_config.instance_device_id
		WHERE instances_devices.instance_id IN (`)
	}

	q.Grow(len(instances) * 2) // We know the minimum length of the separators and integers.

	first := true
	for instanceID := range instances {
		if !first {
			q.WriteString(",")
		}

		first = false

		q.WriteString(fmt.Sprintf("%d", instanceID))
	}

	q.WriteString(`)`)

	return query.Scan(ctx, c.Tx(), q.String(), func(scan func(dest ...any) error) error {
		var instanceID int
		var deviceType cluster.DeviceType
		var deviceName, key, value string

		err := scan(&instanceID, &deviceName, &deviceType, &key, &value)
		if err != nil {
			return err
		}

		_, found := instances[instanceID]
		if !found {
			return fmt.Errorf("Failed loading instance device, referenced instance %d not loaded", instanceID)
		}

		if instances[instanceID].Devices == nil {
			inst := instances[instanceID]
			inst.Devices = make(deviceConfig.Devices)
			instances[instanceID] = inst
		}

		_, found = instances[instanceID].Devices[deviceName]
		if !found {
			instances[instanceID].Devices[deviceName] = deviceConfig.Device{
				"type": deviceType.String(), // Map instances_devices type to config field.
			}
		}

		_, found = instances[instanceID].Devices[deviceName][key]
		if found && key != "type" {
			// For legacy reasons the type value is in both the instances_devices and
			// instances_devices_config tables. We use the one from the instances_devices.
			return fmt.Errorf("Duplicate device row found for device %q key %q for instance ID %d", deviceName, key, instanceID)
		}

		instances[instanceID].Devices[deviceName][key] = value

		return nil
	})
}

// instanceProfiles loads the profile IDs to apply to an instance (in the application order) for all
// instanceIDs in a single query and then updates the instanceApplyProfileIDs and profilesByID maps.
func (c *ClusterTx) instanceProfilesFill(ctx context.Context, snapshotsMode bool, instanceArgs *map[int]InstanceArgs) error {
	instances := *instanceArgs

	// Get profiles referenced by instances.
	// Don't use query parameters for the IN statement to workaround an issue in Dqlite (apparently)
	// that means that >255 query parameters causes partial result sets. See #10705
	// This is safe as the inputs are ints.
	var q strings.Builder

	if snapshotsMode {
		q.WriteString(`
		SELECT
			instances_snapshots.id AS snapshot_id,
			instances_profiles.profile_id AS profile_id
		FROM instances_profiles
		JOIN instances_snapshots ON instances_snapshots.instance_id = instances_profiles.instance_id
		WHERE instances_snapshots.id IN (`)
	} else {
		q.WriteString(`
		SELECT
			instances_profiles.instance_id AS instance_id,
			instances_profiles.profile_id AS profile_id
		FROM instances_profiles
		WHERE instances_profiles.instance_id IN (`)
	}

	q.Grow(len(instances) * 2) // We know the minimum length of the separators and integers.

	first := true
	for instanceID := range instances {
		if !first {
			q.WriteString(",")
		}

		first = false

		q.WriteString(fmt.Sprintf("%d", instanceID))
	}

	q.WriteString(`)
		ORDER BY instances_profiles.instance_id, instances_profiles.apply_order`)

	profilesByID := make(map[int]*api.Profile)
	instanceApplyProfileIDs := make(map[int64][]int, len(instances))

	err := query.Scan(ctx, c.Tx(), q.String(), func(scan func(dest ...any) error) error {
		var instanceID int64
		var profileID int

		err := scan(&instanceID, &profileID)
		if err != nil {
			return err
		}

		instanceApplyProfileIDs[instanceID] = append(instanceApplyProfileIDs[instanceID], profileID)

		// Record that this profile is referenced by at least one instance in the list.
		_, ok := profilesByID[profileID]
		if !ok {
			profilesByID[profileID] = nil
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Get all profiles.
	profiles, err := cluster.GetProfiles(context.TODO(), c.Tx())
	if err != nil {
		return fmt.Errorf("Failed loading profiles: %w", err)
	}

	// Populate profilesByID map entry for referenced profiles.
	// This way we only call ToAPI() on the profiles actually referenced by the instances in
	// the list, which can reduce the number of queries run.
	for _, profile := range profiles {
		_, ok := profilesByID[profile.ID]
		if !ok {
			continue
		}

		profilesByID[profile.ID], err = profile.ToAPI(context.TODO(), c.tx)
		if err != nil {
			return err
		}
	}

	// Populate instance profiles list in apply order.
	for instanceID := range instances {
		inst := instances[instanceID]

		inst.Profiles = make([]api.Profile, 0, len(inst.Profiles))
		for _, applyProfileID := range instanceApplyProfileIDs[int64(inst.ID)] {
			profile := profilesByID[applyProfileID]
			if profile == nil {
				return fmt.Errorf("Instance %d references profile %d that isn't loaded", inst.ID, applyProfileID)
			}

			inst.Profiles = append(inst.Profiles, *profile)
		}

		instances[instanceID] = inst
	}

	return nil
}

// InstancesToInstanceArgs converts many cluster.Instance to a map of InstanceArgs in as few queries as possible.
// Accepts fillProfiles argument that controls whether or not the returned InstanceArgs have their Profiles field
// populated. This avoids the need to load profile info from the database if it is already available in the
// caller's context and can be populated afterwards.
func (c *ClusterTx) InstancesToInstanceArgs(ctx context.Context, fillProfiles bool, instances ...cluster.Instance) (map[int]InstanceArgs, error) {
	var instanceCount, snapshotCount uint

	// Convert instances to partial InstanceArgs slice (Config, Devices and Profiles not populated yet).
	instanceArgs := make(map[int]InstanceArgs, len(instances))
	for _, instance := range instances {
		if instance.Snapshot {
			snapshotCount++
		} else {
			instanceCount++
		}

		args := InstanceArgs{
			ID:           instance.ID,
			Project:      instance.Project,
			Name:         instance.Name,
			Node:         instance.Node,
			Type:         instance.Type,
			Snapshot:     instance.Snapshot,
			Architecture: instance.Architecture,
			Ephemeral:    instance.Ephemeral,
			CreationDate: instance.CreationDate,
			Stateful:     instance.Stateful,
			LastUsedDate: instance.LastUseDate.Time,
			Description:  instance.Description,
			ExpiryDate:   instance.ExpiryDate.Time,
		}

		instanceArgs[instance.ID] = args
	}

	if instanceCount > 0 && snapshotCount > 0 {
		return nil, fmt.Errorf("Cannot use InstancesToInstanceArgs with mixed instance and instance snapshots")
	}

	// Populate instance config.
	err := c.instanceConfigFill(ctx, snapshotCount > 0, &instanceArgs)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance config: %w", err)
	}

	// Populate instance devices.
	err = c.instanceDevicesFill(ctx, snapshotCount > 0, &instanceArgs)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance devices: %w", err)
	}

	// Populate instance profiles if requested.
	if fillProfiles {
		err = c.instanceProfilesFill(ctx, snapshotCount > 0, &instanceArgs)
		if err != nil {
			return nil, fmt.Errorf("Failed loading instance profiles: %w", err)
		}
	}

	return instanceArgs, nil
}

// UpdateInstanceNode changes the name of an instance and the cluster member hosting it.
// It's meant to be used when moving a non-running instance backed by remote storage from one cluster node to another.
func (c *ClusterTx) UpdateInstanceNode(ctx context.Context, project string, oldName string, newName string, newMemberName string, poolID int64, volumeType int) error {
	// Update the name of the instance and its snapshots, and the member ID they are associated with.
	instanceID, err := cluster.GetInstanceID(ctx, c.tx, project, oldName)
	if err != nil {
		return fmt.Errorf("Failed to get instance's ID: %w", err)
	}

	member, err := c.GetNodeByName(ctx, newMemberName)
	if err != nil {
		return fmt.Errorf("Failed to get new member %q info: %w", newMemberName, err)
	}

	stmt := "UPDATE instances SET node_id=?, name=? WHERE id=?"
	result, err := c.tx.Exec(stmt, member.ID, newName, instanceID)
	if err != nil {
		return fmt.Errorf("Failed to update instance's name and member ID: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to get rows affected by instance update: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Unexpected number of updated rows in instances table: %d", n)
	}

	// No need to update storage_volumes if the name is identical
	if newName == oldName {
		return nil
	}

	stmt = "UPDATE storage_volumes SET name=? WHERE name=? AND storage_pool_id=? AND type=?"
	result, err = c.tx.Exec(stmt, newName, oldName, poolID, volumeType)
	if err != nil {
		return fmt.Errorf("Failed to update instance's volume name: %w", err)
	}

	n, err = result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to get rows affected by instance volume update: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Unexpected number of updated rows in volumes table: %d", n)
	}

	return nil
}

// GetLocalInstancesInProject retuurns all instances of the given type on the local member in the given project.
// If projectName is empty then all instances in all projects are returned.
func (c *ClusterTx) GetLocalInstancesInProject(ctx context.Context, filter cluster.InstanceFilter) ([]cluster.Instance, error) {
	node, err := c.GetLocalNodeName(ctx)
	if err != nil {
		return nil, fmt.Errorf("Local node name: %w", err)
	}

	if node != "" {
		filter.Node = &node
	}

	return cluster.GetInstances(ctx, c.tx, filter)
}

// CreateInstanceConfig inserts a new config for the container with the given ID.
func (c *ClusterTx) CreateInstanceConfig(ctx context.Context, id int, config map[string]string) error {
	return CreateInstanceConfig(ctx, c.tx, id, config)
}

// UpdateInstanceConfig inserts/updates/deletes the provided keys.
func (c *ClusterTx) UpdateInstanceConfig(id int, values map[string]string) error {
	insertSQL := "INSERT OR REPLACE INTO instances_config (instance_id, key, value) VALUES"
	deleteSQL := "DELETE FROM instances_config WHERE key IN %s AND instance_id=?"
	return c.configUpdate(id, values, insertSQL, deleteSQL)
}

func (c *ClusterTx) configUpdate(id int, values map[string]string, insertSQL, deleteSQL string) error {
	changes := map[string]string{}
	deletes := []string{}

	// Figure out which key to set/unset
	for key, value := range values {
		if value == "" {
			deletes = append(deletes, key)
			continue
		}

		changes[key] = value
	}

	// Insert/update keys
	if len(changes) > 0 {
		query := insertSQL
		exprs := []string{}
		params := []any{}
		for key, value := range changes {
			exprs = append(exprs, "(?, ?, ?)")
			params = append(params, []any{id, key, value}...)
		}

		query += strings.Join(exprs, ",")
		_, err := c.tx.Exec(query, params...)
		if err != nil {
			return err
		}
	}

	// Delete keys
	if len(deletes) > 0 {
		query := fmt.Sprintf(deleteSQL, query.Params(len(deletes)))
		params := []any{}
		for _, key := range deletes {
			params = append(params, key)
		}

		params = append(params, id)
		_, err := c.tx.Exec(query, params...)
		if err != nil {
			return err
		}
	}

	return nil
}

// DeleteInstanceConfigKey removes the given key from the config of the instance
// with the given ID.
func (c *ClusterTx) DeleteInstanceConfigKey(ctx context.Context, id int64, key string) error {
	q := "DELETE FROM instances_config WHERE key=? AND instance_id=?"
	_, err := c.tx.ExecContext(ctx, q, key, id)
	return err
}

// UpdateInstancePowerState sets the power state of the container with the given ID.
func (c *ClusterTx) UpdateInstancePowerState(id int, state string) error {
	// Set the new value
	str := "INSERT OR REPLACE INTO instances_config (instance_id, key, value) VALUES (?, 'volatile.last_state.power', ?)"
	_, err := c.tx.Exec(str, id, state)
	if err != nil {
		return err
	}

	return nil
}

// UpdateInstanceLastUsedDate updates the last_use_date field of the instance
// with the given ID.
func (c *ClusterTx) UpdateInstanceLastUsedDate(id int, date time.Time) error {
	str := `UPDATE instances SET last_use_date=? WHERE id=?`
	_, err := c.tx.Exec(str, date, id)
	if err != nil {
		return err
	}

	return nil
}

// GetInstanceSnapshotsWithName returns all snapshots of a given instance in date created order, oldest first.
func (c *ClusterTx) GetInstanceSnapshotsWithName(ctx context.Context, project string, name string) ([]cluster.Instance, error) {
	instance, err := cluster.GetInstance(ctx, c.tx, project, name)
	if err != nil {
		return nil, err
	}

	filter := cluster.InstanceSnapshotFilter{
		Project:  &project,
		Instance: &name,
	}

	snapshots, err := cluster.GetInstanceSnapshots(ctx, c.tx, filter)
	if err != nil {
		return nil, err
	}

	sort.SliceStable(snapshots, func(i, j int) bool {
		return snapshots[i].CreationDate.Before(snapshots[j].CreationDate)
	})

	instances := make([]cluster.Instance, len(snapshots))
	for i, snapshot := range snapshots {
		instances[i] = snapshot.ToInstance(instance.Name, instance.Node, instance.Type, instance.Architecture)
	}

	return instances, nil
}

// GetLocalInstanceWithVsockID returns all available instances with the given config key and value.
func (c *ClusterTx) GetLocalInstanceWithVsockID(ctx context.Context, vsockID int) (*cluster.Instance, error) {
	q := `
SELECT instances.id, projects.name AS project, instances.name, nodes.name AS node, instances.type, instances.architecture, instances.ephemeral, instances.creation_date, instances.stateful, instances.last_use_date, coalesce(instances.description, ''), instances.expiry_date
  FROM instances JOIN projects ON instances.project_id = projects.id JOIN nodes ON instances.node_id = nodes.id JOIN instances_config ON instances.id = instances_config.instance_id
  WHERE instances.node_id = ? AND instances.type = ? AND instances_config.key = "volatile.vsock_id" AND instances_config.value = ? LIMIT 1
  `

	inargs := []any{c.nodeID, instancetype.VM, vsockID}
	inst := cluster.Instance{}

	err := c.tx.QueryRowContext(ctx, q, inargs...).Scan(&inst.ID, &inst.Project, &inst.Name, &inst.Node, &inst.Type, &inst.Architecture, &inst.Ephemeral, &inst.CreationDate, &inst.Stateful, &inst.LastUseDate, &inst.Description, &inst.ExpiryDate)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, api.StatusErrorf(http.StatusNotFound, "Instance not found")
		}

		return nil, err
	}

	return &inst, nil
}

// GetInstancePool returns the storage pool of a given instance (or snapshot).
func (c *ClusterTx) GetInstancePool(ctx context.Context, projectName string, instanceName string) (string, error) {
	// Strip snapshot name if supplied in instanceName, and lookup the storage pool of the parent instance
	// as that must always be the same as the snapshot's storage pool.
	instanceName, _, _ = api.GetParentAndSnapshotName(instanceName)

	remoteDrivers := StorageRemoteDriverNames()

	// Get container storage volume. Since container names are globally
	// unique, and their storage volumes carry the same name, their storage
	// volumes are unique too.
	poolName := ""
	query := fmt.Sprintf(`
SELECT storage_pools.name FROM storage_pools
  JOIN storage_volumes_all ON storage_pools.id=storage_volumes_all.storage_pool_id
  JOIN instances ON instances.name=storage_volumes_all.name
  JOIN projects ON projects.id=instances.project_id
 WHERE projects.name=?
   AND storage_volumes_all.name=?
   AND storage_volumes_all.type IN (?,?)
   AND storage_volumes_all.project_id = instances.project_id
   AND (storage_volumes_all.node_id=? OR storage_volumes_all.node_id IS NULL AND storage_pools.driver IN %s)`, query.Params(len(remoteDrivers)))
	inargs := []any{projectName, instanceName, StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeVM, c.nodeID}
	outargs := []any{&poolName}

	for _, driver := range remoteDrivers {
		inargs = append(inargs, driver)
	}

	err := c.tx.QueryRowContext(ctx, query, inargs...).Scan(outargs...)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", api.StatusErrorf(http.StatusNotFound, "Instance storage pool not found")
		}

		return "", err
	}

	return poolName, nil
}

// DeleteInstance removes the instance with the given name from the database.
func (c *ClusterTx) DeleteInstance(ctx context.Context, project, name string) error {
	if strings.Contains(name, internalInstance.SnapshotDelimiter) {
		parts := strings.SplitN(name, internalInstance.SnapshotDelimiter, 2)
		return cluster.DeleteInstanceSnapshot(ctx, c.tx, project, parts[0], parts[1])
	}

	return cluster.DeleteInstance(ctx, c.tx, project, name)
}

// GetInstanceProjectAndName returns the project and the name of the instance
// with the given ID.
func (c *ClusterTx) GetInstanceProjectAndName(ctx context.Context, id int) (string, string, error) {
	var project string
	var name string
	q := `
SELECT projects.name, instances.name
  FROM instances
  JOIN projects ON projects.id = instances.project_id
WHERE instances.id=?
`
	err := c.tx.QueryRowContext(ctx, q, id).Scan(&project, &name)
	if err == sql.ErrNoRows {
		return "", "", api.StatusErrorf(http.StatusNotFound, "Instance not found")
	}

	return project, name, err
}

// GetInstanceID returns the ID of the instance with the given name.
func (c *ClusterTx) GetInstanceID(ctx context.Context, project, name string) (int, error) {
	id, err := cluster.GetInstanceID(ctx, c.tx, project, name)

	return int(id), err
}

// GetInstanceConfig returns the value of the given key in the configuration
// of the instance with the given ID.
func (c *ClusterTx) GetInstanceConfig(ctx context.Context, id int, key string) (string, error) {
	q := "SELECT value FROM instances_config WHERE instance_id=? AND key=?"
	value := ""

	err := c.tx.QueryRowContext(ctx, q, id, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", api.StatusErrorf(http.StatusNotFound, "Instance config not found")
	}

	return value, err
}

// UpdateInstanceStatefulFlag toggles the stateful flag of the instance with
// the given ID.
func (c *ClusterTx) UpdateInstanceStatefulFlag(ctx context.Context, id int, stateful bool) error {
	statefulInt := 0
	if stateful {
		statefulInt = 1
	}

	_, err := c.tx.ExecContext(ctx, "UPDATE instances SET stateful=? WHERE id=?", statefulInt, id)
	if err != nil {
		return fmt.Errorf("Failed updating instance stateful flag: %w", err)
	}

	return nil
}

// UpdateInstanceSnapshotCreationDate updates the creation_date field of the instance snapshot with ID.
func (c *ClusterTx) UpdateInstanceSnapshotCreationDate(ctx context.Context, instanceID int, date time.Time) error {
	stmt := `UPDATE instances_snapshots SET creation_date=? WHERE id=?`

	_, err := c.tx.ExecContext(ctx, stmt, date, instanceID)
	if err != nil {
		return fmt.Errorf("Failed updating instance snapshot creation date: %w", err)
	}

	return nil
}

// GetInstanceSnapshotsNames returns the names of all snapshots of the instance
// in the given project with the given name.
// Returns snapshots slice ordered by when they were created, oldest first.
func (c *ClusterTx) GetInstanceSnapshotsNames(ctx context.Context, project, name string) ([]string, error) {
	result := []string{}

	q := `
SELECT instances_snapshots.name
  FROM instances_snapshots
  JOIN instances ON instances.id = instances_snapshots.instance_id
  JOIN projects ON projects.id = instances.project_id
WHERE projects.name=? AND instances.name=?
ORDER BY instances_snapshots.creation_date, instances_snapshots.id
`
	inargs := []any{project, name}
	outfmt := []any{name}

	dbResults, err := queryScan(ctx, c, q, inargs, outfmt)
	if err != nil {
		return result, err
	}

	for _, r := range dbResults {
		result = append(result, name+internalInstance.SnapshotDelimiter+r[0].(string))
	}

	return result, nil
}

// GetNextInstanceSnapshotIndex returns the index that the next snapshot of the
// instance with the given name and pattern should have.
func (c *ClusterTx) GetNextInstanceSnapshotIndex(ctx context.Context, project string, name string, pattern string) int {
	q := `
SELECT instances_snapshots.name
  FROM instances_snapshots
  JOIN instances ON instances.id = instances_snapshots.instance_id
  JOIN projects ON projects.id = instances.project_id
WHERE projects.name=? AND instances.name=?
ORDER BY instances_snapshots.creation_date, instances_snapshots.id
`
	var numstr string
	inargs := []any{project, name}
	outfmt := []any{numstr}

	results, err := queryScan(ctx, c, q, inargs, outfmt)
	if err != nil {
		return 0
	}

	max := 0

	for _, r := range results {
		snapOnlyName := r[0].(string)
		fields := strings.SplitN(pattern, "%d", 2)

		var num int
		count, err := fmt.Sscanf(snapOnlyName, fmt.Sprintf("%s%%d%s", fields[0], fields[1]), &num)
		if err != nil || count != 1 {
			continue
		}

		if num >= max {
			max = num + 1
		}
	}

	return max
}

// DeleteReadyStateFromLocalInstances deletes the volatile.last_state.ready config key
// from all local instances.
func (c *ClusterTx) DeleteReadyStateFromLocalInstances(ctx context.Context) error {
	nodeID := c.GetNodeID()

	_, err := c.tx.ExecContext(ctx, `
DELETE FROM instances_config
WHERE instances_config.id IN (
	SELECT instances_config.id FROM instances_config
	JOIN instances ON instances_config.instance_id=instances.id
	JOIN nodes ON instances.node_id=nodes.id
	WHERE key="volatile.last_state.ready" AND nodes.id=?
)`, nodeID)
	if err != nil {
		return fmt.Errorf("Failed deleting ready state from local instances: %w", err)
	}

	return nil
}

// CreateInstanceConfig inserts a new config for the instance with the given ID.
func CreateInstanceConfig(ctx context.Context, tx *sql.Tx, id int, config map[string]string) error {
	sql := "INSERT INTO instances_config (instance_id, key, value) values (?, ?, ?)"
	for k, v := range config {
		if v == "" {
			continue
		}

		_, err := tx.ExecContext(ctx, sql, id, k, v)
		if err != nil {
			return fmt.Errorf("Error adding configuration item %q = %q to instance %d: %w", k, v, id, err)
		}
	}

	return nil
}

// UpdateInstance updates the description, architecture and ephemeral flag of
// the instance with the given ID.
func UpdateInstance(tx *sql.Tx, id int, description string, architecture int, ephemeral bool,
	expiryDate time.Time) error {
	str := "UPDATE instances SET description=?, architecture=?, ephemeral=?, expiry_date=? WHERE id=?"
	ephemeralInt := 0
	if ephemeral {
		ephemeralInt = 1
	}

	var err error
	if expiryDate.IsZero() {
		_, err = tx.Exec(str, description, architecture, ephemeralInt, "", id)
	} else {
		_, err = tx.Exec(str, description, architecture, ephemeralInt, expiryDate, id)
	}

	if err != nil {
		return err
	}

	return nil
}
