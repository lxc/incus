package ovsdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"golang.org/x/sync/semaphore"

	"github.com/lxc/incus/v7/shared/logger"
)

// ErrFenced means another daemon has superseded this client's backend generation.
var ErrFenced = errors.New("OVN backend client has been superseded")

// FencedClient keeps uncertain transactions reserved until a backend write fence acknowledges them.
type FencedClient struct {
	ovsdbClient.Client
	mu       *semaphore.Weighted
	table    string
	key      string
	epoch    string
	rootUUID string
	fenced   bool
}

// NewFencedClient installs a member generation before any lifecycle work or receipt release.
func NewFencedClient(ctx context.Context, client ovsdbClient.Client, table string, owner string) (*FencedClient, error) {
	c := &FencedClient{Client: client, mu: semaphore.NewWeighted(1), table: table, key: "incus:ovn-lifecycle:" + owner}
	epoch, err := c.advance(ctx, "")
	if err != nil {
		return nil, err
	}

	c.epoch = epoch
	return c, nil
}

// RootUUID returns the backend root whose generation was acknowledged.
func (c *FencedClient) RootUUID() string {
	_ = c.mu.Acquire(context.Background(), 1)
	defer c.mu.Release(1)

	return c.rootUUID
}

func (c *FencedClient) snapshot(ctx context.Context) (ovsdb.UUID, ovsdb.OvsMap, error) {
	ops := []ovsdb.Operation{{Op: ovsdb.OperationSelect, Table: c.table, Where: []ovsdb.Condition{}, Columns: []string{"_uuid", "external_ids"}}}
	reply, err := c.Client.Transact(ctx, ops...)
	if err != nil {
		return ovsdb.UUID{}, ovsdb.OvsMap{}, err
	}

	err = transactionResultError(reply, ops)
	if err != nil {
		return ovsdb.UUID{}, ovsdb.OvsMap{}, err
	}

	if len(reply) != 1 || len(reply[0].Rows) != 1 {
		return ovsdb.UUID{}, ovsdb.OvsMap{}, fmt.Errorf("Expected one %s root for OVN fencing", c.table)
	}

	id, ok := reply[0].Rows[0]["_uuid"].(ovsdb.UUID)
	if !ok {
		return ovsdb.UUID{}, ovsdb.OvsMap{}, fmt.Errorf("Invalid %s root UUID", c.table)
	}

	ids, ok := reply[0].Rows[0]["external_ids"].(ovsdb.OvsMap)
	if !ok {
		return ovsdb.UUID{}, ovsdb.OvsMap{}, fmt.Errorf("Invalid %s external IDs", c.table)
	}

	return id, ids, nil
}

// advance uses a compare-and-swap so a delayed fence cannot reinstate an obsolete generation.
func (c *FencedClient) advance(ctx context.Context, expected string) (string, error) {
	next := uuid.NewString()
	var lastErr error
	retry := func(err error) error {
		if lastErr == nil || (!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)) {
			lastErr = err
		}

		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), lastErr)
		case <-timer.C:
			return nil
		}
	}

	for {
		err := ctx.Err()
		if err != nil {
			return "", errors.Join(err, lastErr)
		}

		attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
		id, ids, err := c.snapshot(attempt)
		if err != nil {
			cancel()
			err = retry(err)
			if err != nil {
				return "", err
			}

			continue
		}

		if expected != "" && id.GoUUID != c.rootUUID {
			cancel()
			return "", fmt.Errorf("OVN %s database identity changed while a transaction is unresolved", c.table)
		}

		current, ok := ids.GoMap[c.key].(string)
		if !ok && ids.GoMap[c.key] != nil {
			cancel()
			return "", fmt.Errorf("Invalid %s backend generation", c.table)
		}

		foreign := expected != "" && current != expected && current != next
		zero := 0
		ops := []ovsdb.Operation{{Op: ovsdb.OperationWait, Table: c.table, Timeout: &zero, Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id}}, Columns: []string{"_uuid", "external_ids"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": id, "external_ids": ids}}}}
		mutations := []ovsdb.Mutation{}
		if !foreign {
			mutations = append(mutations,
				ovsdb.Mutation{Column: "external_ids", Mutator: ovsdb.MutateOperationDelete, Value: ovsdb.OvsSet{GoSet: []any{c.key}}},
				ovsdb.Mutation{Column: "external_ids", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsMap{GoMap: map[any]any{c.key: next}}})
		}

		// A changing write makes the observed epoch authoritative even on a clustered backend.
		probeKey := c.key + ":barrier"
		mutations = append(mutations,
			ovsdb.Mutation{Column: "external_ids", Mutator: ovsdb.MutateOperationDelete, Value: ovsdb.OvsSet{GoSet: []any{probeKey}}},
			ovsdb.Mutation{Column: "external_ids", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsMap{GoMap: map[any]any{probeKey: uuid.NewString()}}})
		ops = append(ops, ovsdb.Operation{Op: ovsdb.OperationMutate, Table: c.table, Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id}}, Mutations: mutations})
		reply, err := c.Client.Transact(attempt, ops...)
		if err == nil {
			err = transactionResultError(reply, ops)
			var conflict *ovsdb.TimedOut
			if err != nil && !errors.As(err, &conflict) {
				cancel()
				return "", err
			}
		}

		cancel()
		if err != nil {
			err = retry(err)
			if err != nil {
				return "", err
			}

			continue
		}

		if len(reply) != 2 || reply[1].Count != 1 {
			return "", fmt.Errorf("OVN backend fence did not update exactly one %s root", c.table)
		}

		if foreign {
			return "", ErrFenced
		}

		c.rootUUID = id.GoUUID
		return next, nil
	}
}

func transactionResultError(reply []ovsdb.OperationResult, ops []ovsdb.Operation) error {
	operationErrors, err := ovsdb.CheckOperationResults(reply, ops)
	for _, operationErr := range operationErrors {
		err = errors.Join(err, operationErr)
	}

	return err
}

// ValidateTransaction checks local state before dispatch, so its errors cannot represent backend effects.
func ValidateTransaction(ctx context.Context, client ovsdbClient.Client, operations ...ovsdb.Operation) error {
	err := ctx.Err()
	if err != nil {
		return err
	}

	if !client.Connected() {
		return ovsdbClient.ErrNotConnected
	}

	schema := client.Schema()
	if schema.Name == "" || len(schema.Tables) == 0 {
		return errors.New("Cannot transact to OVN backend before its schema is known")
	}

	if !schema.ValidateOperations(operations...) {
		return errors.New("OVN transaction failed local schema validation")
	}

	return nil
}

// IsAwaitingReconnect recognizes the pre-dispatch context wrapper in libovsdb v0.8.1.
func IsAwaitingReconnect(ctx context.Context, err error) bool {
	// ErrNotConnected also represents RPC shutdown after dispatch and is not sufficient here.
	cause := ctx.Err()
	return cause != nil && errors.Unwrap(err) == cause && err.Error() == cause.Error()+": while awaiting reconnection"
}

func (c *FencedClient) guard() ovsdb.Operation {
	zero := 0
	return ovsdb.Operation{
		Op: ovsdb.OperationWait, Table: c.table, Timeout: &zero,
		Where:   []ovsdb.Condition{{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: ovsdb.OvsMap{GoMap: map[any]any{c.key: c.epoch}}}},
		Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": ovsdb.UUID{GoUUID: c.rootUUID}}},
	}
}

// Transact never releases uncertain effects on a client deadline or disconnected transport alone.
func (c *FencedClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	err := c.mu.Acquire(ctx, 1)
	if err != nil {
		return nil, err
	}

	defer c.mu.Release(1)

	if c.fenced {
		return nil, ErrFenced
	}

	guard := c.guard()
	ops := make([]ovsdb.Operation, 0, len(operations)+2)
	ops = append(ops, guard)
	ops = append(ops, operations...)
	ops = append(ops, guard)
	err = ValidateTransaction(ctx, c.Client, ops...)
	if err != nil {
		return nil, err
	}

	reply, err := c.Client.Transact(ctx, ops...)
	if IsAwaitingReconnect(ctx, err) {
		return nil, err
	}

	if err == nil && (len(reply) < len(ops) || len(reply) > len(ops)+1 || (len(reply) == len(ops)+1 && reply[len(ops)].Error == "")) {
		err = fmt.Errorf("Unexpected OVN backend transaction result count")
	}

	// A trailing commit error does not prove that an earlier cluster leader discarded the write.
	if err == nil && len(reply) == len(ops)+1 {
		err = transactionResultError(reply, ops)
	}

	if err != nil {
		logger.Warn("Fencing uncertain OVN backend transaction before releasing ownership", logger.Ctx{"table": c.table, "err": err})
		var fenceErr error
		for {
			var epoch string
			epoch, fenceErr = c.advance(context.Background(), c.epoch)
			if errors.Is(fenceErr, ErrFenced) {
				c.fenced = true
				break
			}

			if fenceErr == nil {
				c.epoch = epoch
				break
			}

			logger.Warn("OVN backend fence remains unacknowledged", logger.Ctx{"table": c.table, "err": fenceErr})
			time.Sleep(time.Second)
		}

		return nil, errors.Join(err, ctx.Err(), fenceErr)
	}

	err = transactionResultError(reply, ops)
	if err != nil {
		if reply[0].Error == "timed out" || reply[len(ops)-1].Error == "timed out" {
			c.fenced = true
			err = errors.Join(ErrFenced, err)
		}

		return nil, errors.Join(err, ctx.Err())
	}

	// Preserve caller insert-result indexes after validating both generation predicates. A complete,
	// validated reply is a committed outcome even if the caller's deadline expired meanwhile.
	return reply[1 : len(reply)-1], nil
}
