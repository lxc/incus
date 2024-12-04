package query

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Rican7/retry/jitter"
	"github.com/cowsql/go-cowsql/driver"
	"github.com/mattn/go-sqlite3"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

const maxRetries = 250

// Retry wraps a function that interacts with the database, and retries it in
// case a transient error is hit.
//
// This should by typically used to wrap transactions.
func Retry(ctx context.Context, f func(ctx context.Context) error) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		err = f(ctx)
		if err == nil {
			// The function succeeded, we're done here.
			break
		}

		if errors.Is(err, context.Canceled) {
			// The function was canceled, don't retry.
			break
		}

		// No point in re-trying or logging a no-row or not found error.
		if errors.Is(err, sql.ErrNoRows) || api.StatusErrorCheck(err, http.StatusNotFound) {
			break
		}

		// Process actual errors.
		if !IsRetriableError(err) {
			logger.Debug("Database error", logger.Ctx{"err": err})
			break
		}

		if i == maxRetries {
			logger.Warn("Database error, giving up", logger.Ctx{"attempt": i, "err": err})
			break
		}

		logger.Debug("Database error, retrying", logger.Ctx{"attempt": i, "err": err})
		time.Sleep(jitter.Deviation(nil, 0.8)(100 * time.Millisecond))
	}

	return err
}

// IsRetriableError returns true if the given error might be transient and the
// interaction can be safely retried.
func IsRetriableError(err error) bool {
	var dErr *driver.Error

	if errors.As(err, &dErr) && dErr.Code == driver.ErrBusy {
		return true
	}

	if errors.Is(err, sqlite3.ErrLocked) || errors.Is(err, sqlite3.ErrBusy) {
		return true
	}

	// Unwrap errors one at a time.
	for ; err != nil; err = errors.Unwrap(err) {
		if strings.Contains(err.Error(), "database is locked") {
			return true
		}

		if strings.Contains(err.Error(), "cannot start a transaction within a transaction") {
			return true
		}

		if strings.Contains(err.Error(), "bad connection") {
			return true
		}

		if strings.Contains(err.Error(), "checkpoint in progress") {
			return true
		}
	}

	return false
}
