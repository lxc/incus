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
	"github.com/lxc/incus/v7/internal/server/systemd_report"
)

func (d *Daemon) startSystemdReportServer() error {
	d.systemdReportMu.Lock()
	defer d.systemdReportMu.Unlock()

	if d.shutdownCtx.Err() != nil {
		return errors.New("Incus is shutting down")
	}

	if d.systemdReportServer != nil {
		return nil
	}

	server, err := systemd_report.Start(systemd_report.SocketPath, d.systemdReportSnapshot)
	if err != nil {
		return err
	}

	d.systemdReportServer = server
	return nil
}

func (d *Daemon) stopSystemdReportServer() error {
	d.systemdReportMu.Lock()
	defer d.systemdReportMu.Unlock()

	if d.systemdReportServer == nil {
		return nil
	}

	server := d.systemdReportServer
	d.systemdReportServer = nil
	return server.Close()
}

func (d *Daemon) systemdReportSnapshot(ctx context.Context) (systemd_report.Snapshot, error) {
	snapshot := systemd_report.Snapshot{InstanceTypes: make(map[string]bool, 2)}
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
			return systemd_report.Snapshot{}, err
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
			return systemd_report.Snapshot{}, err
		}

		return snapshot, nil
	}

	snapshot.WarningsAvailable = true
	return snapshot, nil
}
