package main

import (
	"context"
	"time"

	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/internal/server/task"
	"github.com/lxc/incus/v6/shared/logger"
)

func autoRemoveExpiredTokens(ctx context.Context, s *state.State) {
	expiredTokenOps := make([]*operations.Operation, 0)

	for _, op := range operations.Clone() {
		// Only consider token operations
		if op.Type() != operationtype.ClusterJoinToken && op.Type() != operationtype.CertificateAddToken {
			continue
		}

		// Instead of cancelling the operation here, we add it to a list of expired token operations.
		// This allows us to only show log messages if there are expired tokens.
		expiry, ok := op.Metadata()["expiresAt"].(time.Time)
		if ok && time.Now().After(expiry) {
			expiredTokenOps = append(expiredTokenOps, op)
		}
	}

	if len(expiredTokenOps) == 0 {
		return
	}

	opRun := func(op *operations.Operation) error {
		for _, op := range expiredTokenOps {
			_, err := op.Cancel()
			if err != nil {
				logger.Debug("Failed removing expired token", logger.Ctx{"err": err, "id": op.ID()})
			}
		}

		return nil
	}

	op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.RemoveExpiredTokens, nil, nil, opRun, nil, nil, nil)
	if err != nil {
		logger.Error("Failed creating remove expired tokens operation", logger.Ctx{"err": err})
		return
	}

	logger.Info("Removing expired tokens")

	err = op.Start()
	if err != nil {
		logger.Error("Failed starting remove expired tokens operation", logger.Ctx{"err": err})
		return
	}

	err = op.Wait(ctx)
	if err != nil {
		logger.Error("Failed removing expired tokens", logger.Ctx{"err": err})
		return
	}

	logger.Debug("Done removing expired tokens")
}

func autoRemoveExpiredTokensTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		autoRemoveExpiredTokens(ctx, d.State())
	}

	return f, task.Every(time.Minute)
}
