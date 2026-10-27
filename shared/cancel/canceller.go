package cancel

import (
	"context"
)

// Canceller is a simple wrapper for a cancellable context which makes the associated context.CancelFunc more easily
// accessible.
type Canceller struct {
	context.Context
	Cancel context.CancelFunc
}

// New returns a new canceller with the parent context.
func New(ctx context.Context) *Canceller {
	ctx, cancel := context.WithCancel(ctx)
	return &Canceller{
		Context: ctx,
		Cancel:  cancel,
	}
}
