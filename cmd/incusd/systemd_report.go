package main

import (
	"context"
	"errors"
	"time"

	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/db/warningtype"
	instanceDrivers "github.com/lxc/incus/v7/internal/server/instance/drivers"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	systemdreport "github.com/lxc/incus/v7/internal/server/systemd-report"
)

func (d *Daemon) systemdReportSnapshot(ctx context.Context) (systemdreport.Snapshot, error) {
	snapshot := systemdreport.Snapshot{InstanceTypes: make(map[string]bool, 2)}
	for instType, name := range map[instancetype.Type]string{
		instancetype.Container: "container",
		instancetype.VM:        "virtual-machine",
	} {
		status, ok := instanceDrivers.DriverStatuses()[instType]
		if ok {
			snapshot.InstanceTypes[name] = status.Supported
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	s := d.State()
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.GetLocalNodeName(ctx)
		return err
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(d.shutdownCtx.Err(), context.Canceled) {
			return systemdreport.Snapshot{}, err
		}

		return snapshot, nil
	}

	snapshot.DatabaseReadAvailable = true
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		warnings, err := dbCluster.GetWarnings(ctx, tx.Tx())
		if err != nil {
			return err
		}

		count := 0
		for _, warning := range warnings {
			if warning.Node != s.ServerName || warning.Status == warningtype.StatusResolved {
				continue
			}

			count++
		}

		snapshot.UnresolvedWarnings = count
		return nil
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(d.shutdownCtx.Err(), context.Canceled) {
			return systemdreport.Snapshot{}, err
		}

		return snapshot, nil
	}

	snapshot.WarningsAvailable = true
	return snapshot, nil
}
