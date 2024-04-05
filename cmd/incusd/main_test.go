package main

import (
	"context"
	"fmt"
	"os"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/sys"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/idmap"
)

func mockStartDaemon() (*Daemon, error) {
	d := defaultDaemon()
	d.os.MockMode = true

	// Setup test certificates. We re-use the ones already on disk under
	// the test/ directory, to avoid generating new ones, which is
	// expensive.
	err := sys.SetupTestCerts(internalUtil.VarPath())
	if err != nil {
		return nil, err
	}

	err = d.Init()
	if err != nil {
		return nil, err
	}

	d.os.IdmapSet = &idmap.Set{Entries: []idmap.Entry{
		{IsUID: true, HostID: 100000, NSID: 0, MapRange: 500000},
		{IsGID: true, HostID: 100000, NSID: 0, MapRange: 500000},
	}}

	return d, nil
}

type daemonTestSuite struct {
	suite.Suite
	d      *Daemon
	Req    *require.Assertions
	tmpdir string
}

const daemonTestSuiteDefaultStoragePool string = "testrunPool"

func (suite *daemonTestSuite) SetupTest() {
	tmpdir, err := os.MkdirTemp("", "incus_testrun_")
	if err != nil {
		suite.T().Errorf("failed to create temp dir: %v", err)
	}

	suite.tmpdir = tmpdir

	err = os.Setenv("INCUS_DIR", suite.tmpdir)
	if err != nil {
		suite.T().Errorf("failed to set INCUS_DIR: %v", err)
	}

	suite.d, err = mockStartDaemon()
	if err != nil {
		suite.T().Errorf("failed to start daemon: %v", err)
	}

	// Create default storage pool. Make sure that we don't pass a nil to
	// the next function.
	poolConfig := map[string]string{}

	// Create the database entry for the storage pool.
	poolDescription := fmt.Sprintf("%s storage pool", daemonTestSuiteDefaultStoragePool)
	_, err = dbStoragePoolCreateAndUpdateCache(context.Background(), suite.d.State(), daemonTestSuiteDefaultStoragePool, poolDescription, "mock", poolConfig)
	if err != nil {
		suite.T().Errorf("failed to create default storage pool: %v", err)
	}

	rootDev := map[string]string{}
	rootDev["path"] = "/"
	rootDev["pool"] = daemonTestSuiteDefaultStoragePool
	device := cluster.Device{
		Name:   "root",
		Type:   cluster.TypeDisk,
		Config: rootDev,
	}

	err = suite.d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		profile, err := cluster.GetProfile(ctx, tx.Tx(), "default", "default")
		if err != nil {
			return err
		}

		return cluster.UpdateProfileDevices(ctx, tx.Tx(), int64(profile.ID), map[string]cluster.Device{"root": device})
	})
	if err != nil {
		suite.T().Errorf("failed to update default profile: %v", err)
	}

	suite.Req = require.New(suite.T())
}

func (suite *daemonTestSuite) TearDownTest() {
	err := suite.d.Stop(context.Background(), unix.SIGQUIT)
	if err != nil {
		suite.T().Errorf("failed to stop daemon: %v", err)
	}

	err = os.RemoveAll(suite.tmpdir)
	if err != nil {
		suite.T().Errorf("failed to remove temp dir: %v", err)
	}
}
