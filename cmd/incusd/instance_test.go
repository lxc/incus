package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	storagePools "github.com/lxc/incus/v7/internal/server/storage"
	internalUtil "github.com/lxc/incus/v7/internal/util"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/idmap"
)

type containerTestSuite struct {
	daemonTestSuite
}

func (s *containerTestSuite) TestContainer_ProfilesDefault() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(s.d.State(), args, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true, true) }()

	profiles := c.Profiles()
	s.Len(
		profiles,
		1,
		"No default profile created on instanceCreateInternal.",
	)

	s.Equal(
		"default",
		profiles[0].Name,
		"First profile should be the default profile.",
	)
}

func (s *containerTestSuite) TestContainer_ProfilesMulti() {
	// Create an unprivileged profile
	err := s.d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		profile := cluster.Profile{
			Name:        "unprivileged",
			Description: "unprivileged",
			Project:     "default",
		}

		id, err := cluster.CreateProfile(ctx, tx.Tx(), profile)
		if err != nil {
			return err
		}

		err = cluster.CreateProfileConfig(ctx, tx.Tx(), id, map[string]string{"security.privileged": "true"})
		if err != nil {
			return err
		}

		return err
	})

	s.Req.Nil(err, "Failed to create the unprivileged profile.")
	defer func() {
		_ = s.d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return cluster.DeleteProfile(ctx, tx.Tx(), "default", "unprivileged")
		})
	}()

	var testProfiles []api.Profile

	err = s.d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		testProfiles, err = tx.GetProfiles(ctx, "default", []string{"default", "unprivileged"})

		return err
	})
	s.Req.Nil(err)

	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Profiles:  testProfiles,
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(s.d.State(), args, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true, true) }()

	profiles := c.Profiles()
	s.Len(
		profiles,
		2,
		"Didn't get both profiles in instanceCreateInternal.",
	)

	s.True(
		c.IsPrivileged(),
		"The container is not privileged (didn't apply the unprivileged profile?).",
	)
}

func (s *containerTestSuite) TestContainer_ProfilesOverwriteDefaultNic() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Devices: deviceConfig.Devices{
			"eth0": deviceConfig.Device{
				"type":    "nic",
				"nictype": "bridged",
				"parent":  "unknownbr0",
			},
		},
		Name: "testFoo",
	}

	err := s.d.State().DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "unknownbr0", "", db.NetworkTypeBridge, nil)

		return err
	})
	s.Req.Nil(err)

	c, op, _, err := instance.CreateInternal(s.d.State(), args, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	s.True(c.IsPrivileged(), "This container should be privileged.")

	out, _, err := c.Render()
	s.Req.Nil(err)

	state, ok := out.(*api.Instance)
	s.Req.True(ok)
	defer func() { _ = c.Delete(true, true) }()

	s.Equal(
		"unknownbr0",
		state.Devices["eth0"]["parent"],
		"Container config doesn't overwrite profile config.",
	)
}

func (s *containerTestSuite) TestContainer_LoadFromDB() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Devices: deviceConfig.Devices{
			"eth0": deviceConfig.Device{
				"type":    "nic",
				"nictype": "bridged",
				"parent":  "unknownbr0",
			},
		},
		Name: "testFoo",
	}

	state := s.d.State()

	err := state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "unknownbr0", "", db.NetworkTypeBridge, nil)

		return err
	})
	s.Req.Nil(err)

	// Create the container
	c, op, _, err := instance.CreateInternal(s.d.State(), args, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true, true) }()

	poolName, err := c.StoragePool()
	s.Req.Nil(err)

	pool, err := storagePools.LoadByName(state, poolName)
	s.Req.Nil(err)

	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err = tx.CreateStoragePoolVolume(ctx, c.Project().Name, c.Name(), "", db.StoragePoolVolumeContentTypeFS, pool.ID(), nil, db.StoragePoolVolumeContentTypeFS, time.Now())

		return err
	})
	s.Req.Nil(err)

	// Load the container and trigger initLXC()
	c2, err := instance.LoadByProjectAndName(state, "default", "testFoo")
	c2.IsRunning()
	s.Req.Nil(err)

	hostInterfaces, _ := net.Interfaces()

	apiC1, etagC1, err := c.RenderFull(hostInterfaces)
	s.Req.Nil(err)

	apiC2, etagC2, err := c2.RenderFull(hostInterfaces)
	s.Req.Nil(err)

	s.Equal(etagC1, etagC2)
	s.Exactly(
		apiC1,
		apiC2,
		"The loaded container isn't exactly the same as the created one.",
	)
}

func (s *containerTestSuite) TestContainer_Path_Regular() {
	// Regular
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(s.d.State(), args, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true, true) }()

	s.Req.False(c.IsSnapshot(), "Shouldn't be a snapshot.")
	s.Req.Equal(internalUtil.VarPath("containers", "testFoo"), c.Path())
	s.Req.Equal(internalUtil.VarPath("containers", "testFoo2"), storagePools.InstancePath(instancetype.Container, "default", "testFoo2", false))
}

func (s *containerTestSuite) TestContainer_LogPath() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(s.d.State(), args, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true, true) }()

	s.Req.Equal(internalUtil.VarPath("logs", "testFoo"), c.LogPath())
}

func (s *containerTestSuite) TestContainer_IsPrivileged_Privileged() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(s.d.State(), args, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	s.Req.True(c.IsPrivileged(), "This container should be privileged.")
	s.Req.Nil(c.Delete(true, true), "Failed to delete the container.")
}

func (s *containerTestSuite) TestContainer_AddRoutedNicValidation() {
	eth0 := deviceConfig.Device{
		"name": "eth0", "type": "nic", "ipv4.gateway": "none",
		"ipv6.gateway": "none", "nictype": "routed", "parent": "unknownbr0",
	}

	eth1 := deviceConfig.Device{
		"name": "eth1", "type": "nic", "ipv4.gateway": "none",
		"ipv6.gateway": "none", "nictype": "routed", "parent": "unknownbr0",
	}

	eth2 := deviceConfig.Device{"name": "eth2", "type": "nic", "nictype": "bridged", "parent": "unknownbr0"}

	var testProfiles []api.Profile

	err := s.d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		testProfiles, err = tx.GetProfiles(ctx, "default", []string{"default"})

		return err
	})
	s.Req.Nil(err)

	args := db.InstanceArgs{
		Type:     instancetype.Container,
		Profiles: testProfiles,
		Devices: deviceConfig.Devices{
			"eth0": eth0,
		},
		Name: "testFoo",
	}

	c, op, _, err := instance.CreateInternal(s.d.State(), args, nil, true, true, false)
	s.Req.NoError(err)
	op.Done(nil)
	err = c.Update(db.InstanceArgs{
		Type:     instancetype.Container,
		Profiles: testProfiles,
		Config:   c.LocalConfig(),
		Devices: deviceConfig.Devices{
			"eth0": eth0,
			"eth1": eth1,
		},
		Name: "testFoo",
	}, true)
	s.Req.NoError(err, errors.New("Adding multiple routed with gateway mode ['none'] should succeed. "))

	eth0["ipv6.gateway"] = "auto"
	eth1["ipv6.gateway"] = ""
	err = c.Update(db.InstanceArgs{
		Type:     instancetype.Container,
		Profiles: testProfiles,
		Config:   c.LocalConfig(),
		Devices: deviceConfig.Devices{
			"eth0": eth0,
			"eth1": eth1,
		},
		Name: "testFoo",
	}, true)
	s.Req.Error(err,
		errors.New("Adding multiple routed nic devices with any gateway mmode ['auto',''] should throw error. "))

	err = c.Update(db.InstanceArgs{
		Type:     instancetype.Container,
		Profiles: testProfiles,
		Config:   c.LocalConfig(),
		Devices: deviceConfig.Devices{
			"eth0": eth0,
			"eth2": eth2,
		},
		Name: "testFoo",
	}, true)
	s.Req.NoError(err,
		errors.New("Adding multiple nic devices with unicque nictype ['routed'] should throw error. "))
}

func (s *containerTestSuite) TestContainer_IsPrivileged_Unprivileged() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "false"},
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(s.d.State(), args, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	s.Req.False(c.IsPrivileged(), "This container should be unprivileged.")
	s.Req.Nil(c.Delete(true, true), "Failed to delete the container.")
}

func (s *containerTestSuite) TestContainer_Rename() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(s.d.State(), args, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true, true) }()

	s.Req.Nil(c.Rename("testFoo2", true), "Failed to rename the container.")
	s.Req.Equal(internalUtil.VarPath("containers", "testFoo2"), c.Path())
}

func (s *containerTestSuite) TestContainer_findIdmap_isolated() {
	c1, op, _, err := instance.CreateInternal(s.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	}, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c1.Delete(true, true) }()

	c2, op, _, err := instance.CreateInternal(s.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-2",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	}, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c2.Delete(true, true) }()

	map1, err := c1.(instance.Container).NextIdmap()
	s.Req.Nil(err)
	map2, err := c2.(instance.Container).NextIdmap()
	s.Req.Nil(err)

	host := s.d.os.IdmapSet.Entries[0]

	for i := range 2 {
		s.Req.Equal(host.HostID+65536, map1.Entries[i].HostID, "hostids don't match %d", i)
		s.Req.Equal(int64(0), map1.Entries[i].NSID, "nsid nonzero")
		s.Req.Equal(int64(65536), map1.Entries[i].MapRange, "incorrect maprange")
	}

	for i := range 2 {
		s.Req.Equal(host.HostID+65536*2, map2.Entries[i].HostID, "hostids don't match")
		s.Req.Equal(int64(0), map2.Entries[i].NSID, "nsid nonzero")
		s.Req.Equal(int64(65536), map2.Entries[i].MapRange, "incorrect maprange")
	}
}

func (s *containerTestSuite) TestContainer_findIdmap_mixed() {
	c1, op, _, err := instance.CreateInternal(s.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "false",
		},
	}, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c1.Delete(true, true) }()

	c2, op, _, err := instance.CreateInternal(s.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-2",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	}, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c2.Delete(true, true) }()

	map1, err := c1.(instance.Container).NextIdmap()
	s.Req.Nil(err)
	map2, err := c2.(instance.Container).NextIdmap()
	s.Req.Nil(err)

	host := s.d.os.IdmapSet.Entries[0]

	for i := range 2 {
		s.Req.Equal(host.HostID, map1.Entries[i].HostID, "hostids don't match %d", i)
		s.Req.Equal(int64(0), map1.Entries[i].NSID, "nsid nonzero")
		s.Req.Equal(host.MapRange, map1.Entries[i].MapRange, "incorrect maprange")
	}

	for i := range 2 {
		s.Req.Equal(host.HostID+65536, map2.Entries[i].HostID, "hostids don't match")
		s.Req.Equal(int64(0), map2.Entries[i].NSID, "nsid nonzero")
		s.Req.Equal(int64(65536), map2.Entries[i].MapRange, "incorrect maprange")
	}
}

func (s *containerTestSuite) TestContainer_findIdmap_raw() {
	c1, op, _, err := instance.CreateInternal(s.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "false",
			"raw.idmap":               "both 1000 1000",
		},
	}, nil, true, true, false)
	s.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c1.Delete(true, true) }()

	map1, err := c1.(instance.Container).NextIdmap()
	s.Req.Nil(err)

	host := s.d.os.IdmapSet.Entries[0]

	for _, i := range []int{0, 3} {
		s.Req.Equal(host.HostID, map1.Entries[i].HostID, "hostids don't match")
		s.Req.Equal(int64(0), map1.Entries[i].NSID, "nsid nonzero")
		s.Req.Equal(int64(1000), map1.Entries[i].MapRange, "incorrect maprange")
	}

	s.Req.Equal(int64(1000), map1.Entries[1].HostID, "hostids don't match")
	s.Req.Equal(int64(1000), map1.Entries[1].NSID, "invalid nsid")
	s.Req.Equal(int64(1), map1.Entries[1].MapRange, "incorrect maprange")

	for _, i := range []int{2, 4} {
		s.Req.Equal(host.HostID+1001, map1.Entries[i].HostID, "hostids don't match")
		s.Req.Equal(int64(1001), map1.Entries[i].NSID, "invalid nsid")
		s.Req.Equal(host.MapRange-1000-1, map1.Entries[i].MapRange, "incorrect maprange")
	}
}

func (s *containerTestSuite) TestContainer_findIdmap_maxed() {
	maps := []*idmap.Set{}
	instances := []instance.Instance{}

	for i := range 7 {
		c, op, _, err := instance.CreateInternal(s.d.State(), db.InstanceArgs{
			Type: instancetype.Container,
			Name: fmt.Sprintf("isol-%d", i),
			Config: map[string]string{
				"security.idmap.isolated": "true",
			},
		}, nil, true, true, false)

		instances = append(instances, c)

		/* we should fail if there are no ids left */
		if i == 6 {
			s.Req.NotNil(err)
			return
		}

		s.Req.Nil(err)

		op.Done(nil)

		m, err := c.(instance.Container).NextIdmap()
		s.Req.Nil(err)

		maps = append(maps, m)
	}

	defer func() {
		for _, c := range instances {
			_ = c.Delete(true, true)
		}
	}()

	for i, m1 := range maps {
		for j, m2 := range maps {
			if m1 == m2 {
				continue
			}

			for _, e := range m2.Entries {
				s.Req.False(m1.HostIDsIntersect(e), "%d and %d's idmaps intersect %v %v", i, j, m1, m2)
			}
		}
	}
}

func TestContainerTestSuite(t *testing.T) {
	suite.Run(t, &containerTestSuite{})
}
