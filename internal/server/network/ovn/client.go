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

	backendDB "github.com/lxc/incus/v7/internal/server/network/ovsdb"
)

// timeoutClient wraps a libovsdb client so operations can't wait forever for a reconnection.
type timeoutClient struct {
	ovsdbClient.Client

	name            string
	uncertainWrites bool

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

	if c.uncertainWrites {
		err := backendDB.ValidateTransaction(ctx, c.Client, operations...)
		if err != nil {
			return nil, c.checkErr(ctx, err)
		}
	}

	resp, err := c.Client.Transact(ctx, operations...)
	if c.uncertainWrites && err == nil && (len(resp) < len(operations) || len(resp) > len(operations)+1 || (len(resp) == len(operations)+1 && resp[len(operations)].Error == "")) {
		err = errors.New("Unexpected OVN interconnect transaction result count")
	}

	if c.uncertainWrites && err == nil && len(resp) == len(operations)+1 {
		operationErrors, resultErr := ovsdb.CheckOperationResults(resp, operations)
		for _, operationErr := range operationErrors {
			resultErr = errors.Join(resultErr, operationErr)
		}

		err = resultErr
	}

	if err == nil {
		// A complete validated reply is authoritative even if the deadline expired meanwhile;
		// reporting it as a failure would make callers revert a committed write.
		c.mu.Lock()
		c.unreachable = false
		c.mu.Unlock()
		return resp, nil
	}

	uncertain := c.uncertainWrites && !backendDB.IsAwaitingReconnect(ctx, err)
	err = c.checkErr(ctx, err)
	if uncertain {
		return resp, &uncertainTransactionError{err: err}
	}

	return resp, err
}

type uncertainTransactionError struct {
	err error
}

func (e *uncertainTransactionError) Error() string {
	return e.err.Error()
}

func (e *uncertainTransactionError) Unwrap() error {
	return e.err
}

// IsUncertainTransaction identifies a dispatched interconnect write whose outcome was not acknowledged.
func IsUncertainTransaction(err error) bool {
	var uncertain *uncertainTransactionError
	return errors.As(err, &uncertain)
}
