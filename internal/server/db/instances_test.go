//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/shared/api"
)

func TestContainerList(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local member

	nodeID2, err := tx.CreateNode("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID2, "c3")

	addContainerConfig(t, tx, "c2", "x", "y")
	addContainerConfig(t, tx, "c3", "z", "w")
	addContainerConfig(t, tx, "c3", "a", "b")

	addContainerDevice(t, tx, "c2", "eth0", "nic", nil)
	addContainerDevice(t, tx, "c3", "root", "disk", map[string]string{"x": "y"})

	instType := instancetype.Container
	containers, err := cluster.GetInstances(context.TODO(), tx.Tx(), cluster.InstanceFilter{Type: &instType})
	require.NoError(t, err)
	assert.Len(t, containers, 3)

	c1 := containers[0]
	c1Devices, err := cluster.GetInstanceDevices(context.TODO(), tx.Tx(), c1.ID)
	require.NoError(t, err)
	c1Config, err := cluster.GetInstanceConfig(context.TODO(), tx.Tx(), c1.ID)
	require.NoError(t, err)

	assert.Equal(t, "c1", c1.Name)
	assert.Equal(t, "node2", c1.Node)
	assert.Equal(t, map[string]string{}, c1Config)
	assert.Len(t, c1Devices, 0)

	c2 := containers[1]
	c2Devices, err := cluster.GetInstanceDevices(context.TODO(), tx.Tx(), c2.ID)
	require.NoError(t, err)
	c2Config, err := cluster.GetInstanceConfig(context.TODO(), tx.Tx(), c2.ID)
	require.NoError(t, err)
	assert.Equal(t, "c2", c2.Name)
	assert.Equal(t, map[string]string{"x": "y"}, c2Config)
	assert.Equal(t, "none", c2.Node)
	assert.Len(t, c2Devices, 1)
	assert.Equal(t, "eth0", c2Devices["eth0"].Name)
	assert.Equal(t, "nic", c2Devices["eth0"].Type.String())

	c3 := containers[2]
	c3Devices, err := cluster.GetInstanceDevices(context.TODO(), tx.Tx(), c3.ID)
	require.NoError(t, err)
	c3Config, err := cluster.GetInstanceConfig(context.TODO(), tx.Tx(), c3.ID)
	require.NoError(t, err)
	assert.Equal(t, "c3", c3.Name)
	assert.Equal(t, map[string]string{"z": "w", "a": "b"}, c3Config)
	assert.Equal(t, "node2", c3.Node)
	assert.Len(t, c3Devices, 1)
	assert.Equal(t, "root", c3Devices["root"].Name)
	assert.Equal(t, "disk", c3Devices["root"].Type.String())
	assert.Equal(t, map[string]string{"x": "y"}, c3Devices["root"].Config)
}

func TestContainerList_FilterByNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local member

	nodeID2, err := tx.CreateNode("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID2, "c3")

	project := "default"
	node := "node2"
	instType := instancetype.Container
	filter := cluster.InstanceFilter{Project: &project, Node: &node, Type: &instType}

	containers, err := cluster.GetInstances(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Len(t, containers, 2)

	assert.Equal(t, 1, containers[0].ID)
	assert.Equal(t, "c1", containers[0].Name)
	assert.Equal(t, "node2", containers[0].Node)
	assert.Equal(t, 3, containers[1].ID)
	assert.Equal(t, "c3", containers[1].Name)
	assert.Equal(t, "node2", containers[1].Node)
}

func TestInstanceList_ContainerWithSameNameInDifferentProjects(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()

	// Create a project with no features
	project1 := cluster.Project{}
	project1.Name = "blah"
	_, err := cluster.CreateProject(ctx, tx.Tx(), project1)
	require.NoError(t, err)

	// Create a project with the profiles feature and a custom profile.
	project2 := cluster.Project{}
	project2.Name = "test"
	project2Config := map[string]string{"features.profiles": "true"}
	id, err := cluster.CreateProject(ctx, tx.Tx(), project2)
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, project2Config)
	require.NoError(t, err)

	profile := cluster.Profile{
		Project: "test",
		Name:    "intranet",
	}

	_, err = cluster.CreateProfile(ctx, tx.Tx(), profile)
	require.NoError(t, err)

	// Create a container in project1 using the default profile from the
	// default project.
	c1p1 := cluster.Instance{
		Project:      "blah",
		Name:         "c1",
		Node:         "none",
		Type:         instancetype.Container,
		Architecture: 1,
		Ephemeral:    false,
		Stateful:     true,
	}

	id, err = cluster.CreateInstance(context.TODO(), tx.Tx(), c1p1)
	require.NoError(t, err)

	err = cluster.UpdateInstanceProfiles(context.TODO(), tx.Tx(), int(id), c1p1.Project, []string{"default"})
	require.NoError(t, err)

	// Create a container in project2 using the custom profile from the
	// project.
	c1p2 := cluster.Instance{
		Project:      "test",
		Name:         "c1",
		Node:         "none",
		Type:         instancetype.Container,
		Architecture: 1,
		Ephemeral:    false,
		Stateful:     true,
	}

	id, err = cluster.CreateInstance(context.TODO(), tx.Tx(), c1p2)
	require.NoError(t, err)

	err = cluster.UpdateInstanceProfiles(context.TODO(), tx.Tx(), int(id), c1p2.Project, []string{"intranet"})
	require.NoError(t, err)

	containers, err := cluster.GetInstances(context.TODO(), tx.Tx())
	require.NoError(t, err)

	c1Profiles, err := cluster.GetInstanceProfiles(context.TODO(), tx.Tx(), containers[0].ID)
	require.NoError(t, err)

	c2Profiles, err := cluster.GetInstanceProfiles(context.TODO(), tx.Tx(), containers[1].ID)
	require.NoError(t, err)

	assert.Len(t, containers, 2)

	assert.Equal(t, "blah", containers[0].Project)
	assert.Len(t, c1Profiles, 1)
	assert.Equal(t, "default", c1Profiles[0].Name)

	assert.Equal(t, "test", containers[1].Project)
	assert.Len(t, c2Profiles, 1)
	assert.Equal(t, "intranet", c2Profiles[0].Name)
}

func TestInstanceList(t *testing.T) {
	c, clusterCleanup := db.NewTestCluster(t)
	defer clusterCleanup()

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		profile := cluster.Profile{
			Project: "default",
			Name:    "profile1",
		}

		profileConfig := map[string]string{"a": "1"}
		profileDevices := map[string]cluster.Device{
			"root": {
				Name:   "root",
				Type:   cluster.TypeDisk,
				Config: map[string]string{"b": "2"},
			},
		}

		id, err := cluster.CreateProfile(ctx, tx.Tx(), profile)
		if err != nil {
			return err
		}

		err = cluster.CreateProfileConfig(ctx, tx.Tx(), id, profileConfig)
		if err != nil {
			return err
		}

		err = cluster.CreateProfileDevices(ctx, tx.Tx(), id, profileDevices)
		if err != nil {
			return err
		}

		container := cluster.Instance{
			Project:      "default",
			Name:         "c1",
			Node:         "none",
			Type:         instancetype.Container,
			Architecture: 1,
			Ephemeral:    false,
			Stateful:     true,
		}

		id, err = cluster.CreateInstance(context.TODO(), tx.Tx(), container)
		if err != nil {
			return err
		}

		err = cluster.CreateInstanceConfig(context.TODO(), tx.Tx(), id, map[string]string{"c": "3"})
		if err != nil {
			return err
		}

		err = cluster.CreateInstanceDevices(context.TODO(), tx.Tx(), id, map[string]cluster.Device{"eth0": {Name: "eth0", Type: cluster.TypeNIC, Config: map[string]string{"d": "4"}}})
		if err != nil {
			return err
		}

		err = cluster.UpdateInstanceProfiles(context.TODO(), tx.Tx(), int(id), container.Project, []string{"default", "profile1"})
		if err != nil {
			return err
		}

		return nil
	})
	require.NoError(t, err)

	var instances []db.InstanceArgs

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
			dbInst.Config = db.ExpandInstanceConfig(dbInst.Config, dbInst.Profiles)
			dbInst.Devices = db.ExpandInstanceDevices(dbInst.Devices, dbInst.Profiles)

			instances = append(instances, dbInst)

			return nil
		})
	})
	require.NoError(t, err)

	assert.Len(t, instances, 1)

	assert.Equal(t, map[string]string{"a": "1", "c": "3"}, instances[0].Config)
	assert.Equal(t, map[string]map[string]string{
		"root": {"type": "disk", "b": "2"},
		"eth0": {"type": "nic", "d": "4"},
	}, instances[0].Devices.CloneNative())
}

func TestCreateInstance(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	object := cluster.Instance{
		Project:      "default",
		Name:         "c1",
		Type:         0,
		Node:         "none",
		Architecture: 1,
		Ephemeral:    true,
		Stateful:     true,
		LastUseDate:  sql.NullTime{Time: time.Now(), Valid: true},
		Description:  "container 1",
	}

	id, err := cluster.CreateInstance(context.TODO(), tx.Tx(), object)
	require.NoError(t, err)

	err = cluster.CreateInstanceConfig(context.TODO(), tx.Tx(), id, map[string]string{"x": "y"})
	require.NoError(t, err)

	err = cluster.CreateInstanceDevices(context.TODO(), tx.Tx(), id, map[string]cluster.Device{"root": {Name: "root", Config: map[string]string{"type": "disk", "x": "y"}}})
	require.NoError(t, err)

	err = cluster.UpdateInstanceProfiles(context.TODO(), tx.Tx(), int(id), object.Project, []string{"default"})
	require.NoError(t, err)

	assert.Equal(t, int64(1), id)

	c1, err := cluster.GetInstance(context.TODO(), tx.Tx(), "default", "c1")
	require.NoError(t, err)

	c1Devices, err := cluster.GetInstanceDevices(context.TODO(), tx.Tx(), c1.ID)
	require.NoError(t, err)

	c1Config, err := cluster.GetInstanceConfig(context.TODO(), tx.Tx(), c1.ID)
	require.NoError(t, err)

	c1Profiles, err := cluster.GetInstanceProfiles(context.TODO(), tx.Tx(), c1.ID)
	require.NoError(t, err)

	assert.Equal(t, "c1", c1.Name)
	assert.Equal(t, map[string]string{"x": "y"}, c1Config)
	assert.Len(t, c1Devices, 1)
	assert.Equal(t, "root", c1Devices["root"].Name)
	assert.Equal(t, map[string]string{"type": "disk", "x": "y"}, c1Devices["root"].Config)
	assert.Len(t, c1Profiles, 1)
	assert.Equal(t, "default", c1Profiles[0].Name)
}

func TestCreateInstance_Snapshot(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	instance := cluster.Instance{
		Project:      "default",
		Name:         "foo",
		Type:         0,
		Node:         "none",
		Architecture: 2,
		Ephemeral:    false,
		Stateful:     false,
		LastUseDate:  sql.NullTime{Time: time.Now(), Valid: true},
		Description:  "container 1",
	}

	id, err := cluster.CreateInstance(context.TODO(), tx.Tx(), instance)
	require.NoError(t, err)

	err = cluster.CreateInstanceConfig(context.TODO(), tx.Tx(), id, map[string]string{
		"image.architecture":  "x86_64",
		"image.description":   "BusyBox x86_64",
		"image.name":          "busybox-x86_64",
		"image.os":            "BusyBox",
		"volatile.base_image": "1f7f054e6ccb",
	})
	require.NoError(t, err)

	err = cluster.UpdateInstanceProfiles(context.TODO(), tx.Tx(), int(id), instance.Project, []string{"default"})
	require.NoError(t, err)

	assert.Equal(t, int64(1), id)

	snapshot := cluster.Instance{
		Project:      "default",
		Name:         "foo/snap0",
		Type:         1,
		Node:         "none",
		Architecture: 2,
		Ephemeral:    false,
		Stateful:     false,
		LastUseDate:  sql.NullTime{Time: time.Now(), Valid: true},
		Description:  "container 1",
	}

	id, err = cluster.CreateInstance(context.TODO(), tx.Tx(), snapshot)
	require.NoError(t, err)

	err = cluster.CreateInstanceConfig(context.TODO(), tx.Tx(), id, map[string]string{
		"image.architecture":      "x86_64",
		"image.description":       "BusyBox x86_64",
		"image.name":              "busybox-x86_64",
		"image.os":                "BusyBox",
		"volatile.apply_template": "create",
		"volatile.base_image":     "1f7f054e6ccb",
		"volatile.eth0.hwaddr":    "00:16:3e:2a:3f:e2",
		"volatile.idmap.base":     "0",
	})
	require.NoError(t, err)

	err = cluster.UpdateInstanceProfiles(context.TODO(), tx.Tx(), int(id), instance.Project, []string{"default"})
	require.NoError(t, err)

	assert.Equal(t, int64(2), id)

	_, err = cluster.GetInstance(context.TODO(), tx.Tx(), "default", "foo/snap0")
	require.NoError(t, err)
}

// Containers are grouped by node address.
func TestGetInstancesByMemberAddress(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local member

	nodeID2, err := tx.CreateNode("node2", "1.2.3.4:666")
	require.NoError(t, err)

	nodeID3, err := tx.CreateNode("node3", "5.6.7.8:666")
	require.NoError(t, err)
	require.NoError(t, tx.SetNodeHeartbeat("5.6.7.8:666", time.Now().Add(-time.Minute)))

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID3, "c3")
	addContainer(t, tx, nodeID2, "c4")

	instType := instancetype.Container
	result, err := tx.GetInstancesByMemberAddress(context.Background(), time.Duration(db.DefaultOfflineThreshold)*time.Second, []string{"default"}, instType)
	require.NoError(t, err)
	assert.Equal(
		t,
		map[string][]db.Instance{
			"":            {{ID: 2, Project: api.ProjectDefaultName, Name: "c2", Location: "none"}},
			"1.2.3.4:666": {{ID: 1, Project: api.ProjectDefaultName, Name: "c1", Location: "node2"}, {ID: 4, Project: api.ProjectDefaultName, Name: "c4", Location: "node2"}},
			"0.0.0.0":     {{ID: 3, Project: api.ProjectDefaultName, Name: "c3", Location: "node3"}},
		}, result)
}

func TestGetInstancePool(t *testing.T) {
	dbCluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	err := dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		poolID, err := tx.CreateStoragePool(ctx, "default", "", "dir", nil)
		if err != nil {
			return err
		}

		_, err = tx.CreateStoragePoolVolume(ctx, "default", "c1", "", db.StoragePoolVolumeTypeContainer, poolID, nil, db.StoragePoolVolumeContentTypeFS, time.Now())
		if err != nil {
			return err
		}

		container := cluster.Instance{
			Project: "default",
			Name:    "c1",
			Node:    "none",
		}

		id, err := cluster.CreateInstance(context.TODO(), tx.Tx(), container)
		if err != nil {
			return err
		}

		err = cluster.CreateInstanceDevices(context.TODO(), tx.Tx(), id, map[string]cluster.Device{
			"root": {
				Name: "root",
				Config: map[string]string{"path": "/",
					"pool": "default",
					"type": "disk",
				},
			},
		})
		if err != nil {
			return err
		}

		poolName, err := tx.GetInstancePool(ctx, "default", "c1")
		if err != nil {
			return err
		}

		assert.Equal(t, "default", poolName)

		return nil
	})
	require.NoError(t, err)
}

// All containers on a node are loaded in bulk.
func TestGetLocalInstancesInProject(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local member

	nodeID2, err := tx.CreateNode("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID1, "c3")
	addContainer(t, tx, nodeID1, "c4")

	addContainerConfig(t, tx, "c2", "x", "y")
	addContainerConfig(t, tx, "c3", "z", "w")
	addContainerConfig(t, tx, "c3", "a", "b")

	addContainerDevice(t, tx, "c2", "eth0", "nic", nil)
	addContainerDevice(t, tx, "c4", "root", "disk", map[string]string{"x": "y"})

	instType := instancetype.Container
	containers, err := tx.GetLocalInstancesInProject(context.TODO(), cluster.InstanceFilter{Type: &instType})
	require.NoError(t, err)
	assert.Len(t, containers, 3)

	assert.Equal(t, "c2", containers[0].Name)
	assert.Equal(t, "c3", containers[1].Name)
	assert.Equal(t, "c4", containers[2].Name)

	assert.Equal(t, "none", containers[0].Node)
	assert.Equal(t, "none", containers[1].Node)
	assert.Equal(t, "none", containers[2].Node)

	c1Config, err := cluster.GetInstanceConfig(context.TODO(), tx.Tx(), containers[0].ID)
	require.NoError(t, err)
	c1Devices, err := cluster.GetInstanceDevices(context.TODO(), tx.Tx(), containers[0].ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"x": "y"}, c1Config)
	assert.Equal(t, map[string]map[string]string{"eth0": {"type": "nic"}}, cluster.DevicesToAPI(c1Devices))

	c2Config, err := cluster.GetInstanceConfig(context.TODO(), tx.Tx(), containers[1].ID)
	require.NoError(t, err)
	c2Devices, err := cluster.GetInstanceDevices(context.TODO(), tx.Tx(), containers[1].ID)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"z": "w", "a": "b"}, c2Config)
	assert.Len(t, c2Devices, 0)

	c3Config, err := cluster.GetInstanceConfig(context.TODO(), tx.Tx(), containers[2].ID)
	require.NoError(t, err)
	c3Devices, err := cluster.GetInstanceDevices(context.TODO(), tx.Tx(), containers[2].ID)
	require.NoError(t, err)
	assert.Len(t, c3Config, 0)
	assert.Equal(t, map[string]map[string]string{"root": {"type": "disk", "x": "y"}}, cluster.DevicesToAPI(c3Devices))
}

func addContainer(t *testing.T, tx *db.ClusterTx, nodeID int64, name string) {
	stmt := `
INSERT INTO instances(node_id, name, architecture, type, project_id, description) VALUES (?, ?, 1, ?, 1, '')
`
	_, err := tx.Tx().Exec(stmt, nodeID, name, instancetype.Container)
	require.NoError(t, err)
}

func addContainerConfig(t *testing.T, tx *db.ClusterTx, container, key, value string) {
	id := getContainerID(t, tx, container)

	stmt := `
INSERT INTO instances_config(instance_id, key, value) VALUES (?, ?, ?)
`
	_, err := tx.Tx().Exec(stmt, id, key, value)
	require.NoError(t, err)
}

func addContainerDevice(t *testing.T, tx *db.ClusterTx, container, name, typ string, config map[string]string) {
	id := getContainerID(t, tx, container)

	code, err := cluster.NewDeviceType(typ)
	require.NoError(t, err)

	stmt := `
INSERT INTO instances_devices(instance_id, name, type) VALUES (?, ?, ?)
`
	_, err = tx.Tx().Exec(stmt, id, name, code)
	require.NoError(t, err)

	deviceID := getDeviceID(t, tx, id, name)

	for key, value := range config {
		stmt := `
INSERT INTO instances_devices_config(instance_device_id, key, value) VALUES (?, ?, ?)
`
		_, err = tx.Tx().Exec(stmt, deviceID, key, value)
		require.NoError(t, err)
	}
}

// Return the container ID given its name.
func getContainerID(t *testing.T, tx *db.ClusterTx, name string) int64 {
	var id int64

	stmt := "SELECT id FROM instances WHERE name=?"
	row := tx.Tx().QueryRow(stmt, name)
	err := row.Scan(&id)
	require.NoError(t, err)

	return id
}

// Return the device ID given its container ID and name.
func getDeviceID(t *testing.T, tx *db.ClusterTx, containerID int64, name string) int64 {
	var id int64

	stmt := "SELECT id FROM instances_devices WHERE instance_id=? AND name=?"
	row := tx.Tx().QueryRow(stmt, containerID, name)
	err := row.Scan(&id)
	require.NoError(t, err)

	return id
}
