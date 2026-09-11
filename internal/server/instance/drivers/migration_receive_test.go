//go:build linux && cgo && !agent

package drivers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestMigrationReceiveTransferErrorBeforeCancellation(t *testing.T) {
	want := errors.New("transfer failed")
	transfer := &migrationReceiveTransfer{done: make(chan struct{})}
	group, ctx := errgroup.WithContext(context.Background())
	allowReturn := make(chan struct{})
	defer func() {
		close(allowReturn)
		require.ErrorIs(t, group.Wait(), want)
	}()
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- transfer.wait(ctx) }()
	}

	group.Go(func() (retErr error) {
		defer func() { <-allowReturn }()
		defer func() { transfer.complete(retErr) }()
		return want
	})

	// Both the restore worker and the final response must see errors before errgroup does.
	for range 2 {
		require.ErrorIs(t, <-results, want)
		require.NoError(t, ctx.Err())
	}
}

func TestMigrationReceiveTransferSuccess(t *testing.T) {
	transfer := &migrationReceiveTransfer{done: make(chan struct{})}
	transfer.complete(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, transfer.wait(ctx))
	cancel()
	require.ErrorIs(t, transfer.wait(ctx), context.Canceled)
}
