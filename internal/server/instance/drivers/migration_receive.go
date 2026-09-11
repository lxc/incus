//go:build linux && cgo && !agent

package drivers

import (
	"context"
)

// migrationReceiveTransfer publishes the result before notifying all waiting receivers.
type migrationReceiveTransfer struct {
	done chan struct{}
	err  error
}

func (t *migrationReceiveTransfer) complete(err error) {
	t.err = err
	close(t.done)
}

func (t *migrationReceiveTransfer) wait(ctx context.Context) error {
	<-t.done
	if t.err != nil {
		return t.err
	}

	return ctx.Err()
}
