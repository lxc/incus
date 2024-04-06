package main

import (
	"context"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/internal/server/task"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/logger"
)

// This task function expires logs when executed. It's started by the Daemon
// and will run once every 24h.
func expireLogsTask(state *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		opRun := func(op *operations.Operation) error {
			return expireLogs(ctx, state)
		}

		op, err := operations.OperationCreate(state, "", operations.OperationClassTask, operationtype.LogsExpire, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed creating log files expiry operation", logger.Ctx{"err": err})
			return
		}

		logger.Info("Expiring log files")
		err = op.Start()
		if err != nil {
			logger.Error("Failed starting log files expiry operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed expiring log files", logger.Ctx{"err": err})
			return
		}

		logger.Info("Done expiring log files")
	}

	return f, task.Daily()
}

func expireLogs(ctx context.Context, state *state.State) error {
	// List the instances.
	instances, err := instance.LoadNodeAll(state, instancetype.Any)
	if err != nil {
		return err
	}

	// List the directory.
	entries, err := os.ReadDir(state.OS.LogDir)
	if err != nil {
		return err
	}

	// Build the expected names.
	names := []string{}
	for _, inst := range instances {
		names = append(names, project.Instance(inst.Project().Name, inst.Name()))
	}

	newestFile := func(path string, dir os.FileInfo) time.Time {
		newest := dir.ModTime()

		entries, err := os.ReadDir(path)
		if err != nil {
			return newest
		}

		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				continue
			}

			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}

		return newest
	}

	for _, entry := range entries {
		// At each iteration we check if we got cancelled in the meantime.
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// We only care about instance directories.
		if !entry.IsDir() {
			continue
		}

		// Skip if we are unable to read the file info, e.g. the file might
		// be deleted.
		fi, err := entry.Info()
		if err != nil {
			continue
		}

		// Check if the instance still exists.
		if slices.Contains(names, fi.Name()) {
			instDirEntries, err := os.ReadDir(internalUtil.LogPath(fi.Name()))
			if err != nil {
				return err
			}

			for _, instDirEntry := range instDirEntries {
				path := internalUtil.LogPath(fi.Name(), instDirEntry.Name())

				instInfo, err := instDirEntry.Info()
				if err != nil {
					continue
				}

				// Deal with directories (snapshots).
				if instInfo.IsDir() {
					newest := newestFile(path, instInfo)
					if time.Since(newest).Hours() >= 48 {
						err := os.RemoveAll(path)
						if err != nil {
							return err
						}
					}

					continue
				}

				// Only remove old log files (keep other files, such as conf, pid, monitor etc).
				if strings.HasSuffix(instInfo.Name(), ".log") || strings.HasSuffix(instInfo.Name(), ".log.old") {
					// Remove any log file which wasn't modified in the past 48 hours.
					if time.Since(instInfo.ModTime()).Hours() >= 48 {
						err := os.Remove(path)
						if err != nil {
							return err
						}
					}
				}
			}
		} else {
			// Empty directory if unchanged in the past 24 hours.
			path := internalUtil.LogPath(fi.Name())
			newest := newestFile(path, fi)
			if time.Since(newest).Hours() >= 24 {
				err := os.RemoveAll(path)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}
