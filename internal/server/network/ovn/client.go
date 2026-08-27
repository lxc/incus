package ovn

import (
	"context"
	"time"

	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	ovsdbModel "github.com/ovn-kubernetes/libovsdb/model"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// timeoutClient wraps a libovsdb client so operations can't wait forever for a reconnection.
type timeoutClient struct {
	ovsdbClient.Client
}

// timeoutContext applies a default deadline to the context if it doesn't have one.
func timeoutContext(ctx context.Context) (context.Context, context.CancelFunc) {
	_, ok := ctx.Deadline()
	if ok {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, 30*time.Second)
}

// Get retrieves a record from the cache, waiting a bounded time for a consistent cache.
func (c *timeoutClient) Get(ctx context.Context, m ovsdbModel.Model) error {
	ctx, cancel := timeoutContext(ctx)
	defer cancel()

	return c.Client.Get(ctx, m)
}

// List retrieves records from the cache, waiting a bounded time for a consistent cache.
func (c *timeoutClient) List(ctx context.Context, result any) error {
	ctx, cancel := timeoutContext(ctx)
	defer cancel()

	return c.Client.List(ctx, result)
}

// Transact runs the transaction, waiting a bounded time for a connection.
func (c *timeoutClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	ctx, cancel := timeoutContext(ctx)
	defer cancel()

	return c.Client.Transact(ctx, operations...)
}
