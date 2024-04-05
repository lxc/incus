package main

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
)

func (suite *containerTestSuite) TestSnapshotScheduling() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "hal9000",
	}

	c, op, _, err := instance.CreateInternal(suite.d.State(), args, true, true)
	suite.Req.Nil(err)
	suite.Equal(true, snapshotIsScheduledNow("* * * * *",
		int64(c.ID())),
		"snapshot.schedule config '* * * * *' should have matched now")
	suite.Equal(true, snapshotIsScheduledNow("@daily,"+
		"@hourly,"+
		"@midnight,"+
		"@weekly,"+
		"@monthly,"+
		"@annually,"+
		"@yearly,"+
		" * * * * *",
		int64(c.ID())),
		"snapshot.schedule config '* * * * *' should have matched now")
	op.Done(nil)
}

func TestSnapshotCommon(t *testing.T) {
	suite.Run(t, new(containerTestSuite))
}
