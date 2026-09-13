package ovn

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	ovsdbModel "github.com/ovn-kubernetes/libovsdb/model"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// timeoutClient wraps a libovsdb client so operations can't wait forever for a reconnection.
type timeoutClient struct {
	ovsdbClient.Client

	name string

	mu          sync.Mutex
	unreachable bool
}

// timeoutContext applies a default deadline to the context if it doesn't have one.
// Once a wait has timed out, later calls only wait briefly until one succeeds again.
func (c *timeoutClient) timeoutContext(ctx context.Context) (context.Context, context.CancelFunc) {
	_, ok := ctx.Deadline()
	if ok {
		return ctx, func() {}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.unreachable {
		return context.WithTimeout(ctx, 5*time.Second)
	}

	return context.WithTimeout(ctx, 30*time.Second)
}

// checkErr records whether the call timed out and wraps the error accordingly.
func (c *timeoutClient) checkErr(ctx context.Context, err error) error {
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)

	c.mu.Lock()
	c.unreachable = timedOut
	c.mu.Unlock()

	if !timedOut {
		return err
	}

	if err == nil {
		err = ctx.Err()
	}

	return fmt.Errorf("OVN %s database unavailable: %w", c.name, err)
}

// Get retrieves a record from the cache, waiting a bounded time for a consistent cache.
func (c *timeoutClient) Get(ctx context.Context, m ovsdbModel.Model) error {
	ctx, cancel := c.timeoutContext(ctx)
	defer cancel()

	return c.checkErr(ctx, c.Client.Get(ctx, m))
}

// List retrieves records from the cache, waiting a bounded time for a consistent cache.
func (c *timeoutClient) List(ctx context.Context, result any) error {
	ctx, cancel := c.timeoutContext(ctx)
	defer cancel()

	return c.checkErr(ctx, c.Client.List(ctx, result))
}

// Transact runs the transaction, waiting a bounded time for a connection.
func (c *timeoutClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	ctx, cancel := c.timeoutContext(ctx)
	defer cancel()

	resp, err := c.Client.Transact(ctx, operations...)

	return resp, c.checkErr(ctx, err)
}
