package ovn

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	ovnICNB "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-ic-nb"
)

type transactionTestClient struct {
	ovsdbClient.Client
	results        []ovsdb.OperationResult
	err            error
	disconnected   bool
	unknownSchema  bool
	calls          int
	awaitReconnect bool

	// replyAtDeadline completes the transaction as the caller's deadline expires.
	replyAtDeadline bool
}

func (c *transactionTestClient) Connected() bool {
	return !c.disconnected
}

func (c *transactionTestClient) Schema() ovsdb.DatabaseSchema {
	if c.unknownSchema {
		return ovsdb.DatabaseSchema{}
	}

	return ovnICNB.Schema()
}

func (c *transactionTestClient) Transact(ctx context.Context, _ ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	c.calls++
	if c.replyAtDeadline {
		<-ctx.Done()
		return c.results, nil
	}

	if c.awaitReconnect {
		<-ctx.Done()
		return nil, fmt.Errorf("%w: while awaiting reconnection", ctx.Err())
	}

	return c.results, c.err
}

func TestICNBUncertainTransactionPreservesOriginalError(t *testing.T) {
	transport := errors.New("transport closed after dispatch")
	for _, original := range []error{transport, context.DeadlineExceeded, context.Canceled, ovsdbClient.ErrNotConnected} {
		client := &timeoutClient{
			Client:          &transactionTestClient{err: original},
			name:            "interconnect northbound",
			uncertainWrites: true,
		}

		_, err := client.Transact(context.Background(), ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Transit_Switch"})
		require.ErrorIs(t, err, original)
		require.True(t, IsUncertainTransaction(fmt.Errorf("Failed deleting peer: %w", err)))
	}
}

func TestICNBNotDispatchedTransactionsAreNotUncertain(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		client    transactionTestClient
		operation ovsdb.Operation
	}{
		{name: "disconnected", client: transactionTestClient{disconnected: true}, operation: ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Transit_Switch"}},
		{name: "unknown schema", client: transactionTestClient{unknownSchema: true}, operation: ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Transit_Switch"}},
		{name: "invalid table", operation: ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "NoSuchTable"}},
		{name: "invalid column", operation: ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Transit_Switch", Row: ovsdb.Row{"no_such_column": "bad"}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			raw := &testCase.client
			client := &timeoutClient{Client: raw, name: "interconnect northbound", uncertainWrites: true}
			_, err := client.Transact(context.Background(), testCase.operation)
			require.Error(t, err)
			require.False(t, IsUncertainTransaction(err))
			require.Zero(t, raw.calls)
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	raw := &transactionTestClient{awaitReconnect: true}
	client := &timeoutClient{Client: raw, name: "interconnect northbound", uncertainWrites: true}
	_, err := client.Transact(ctx, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Transit_Switch"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, IsUncertainTransaction(err))
	require.Equal(t, 1, raw.calls)
}

func TestICNBAcknowledgedTransactionHasKnownOutcome(t *testing.T) {
	for _, response := range [][]ovsdb.OperationResult{{{}}, {{Error: "constraint violation", Details: "rejected transaction"}}} {
		client := &timeoutClient{
			Client:          &transactionTestClient{results: response},
			name:            "interconnect northbound",
			uncertainWrites: true,
		}

		results, err := client.Transact(context.Background(), ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Transit_Switch"})
		require.NoError(t, err)
		require.Equal(t, response, results)
		require.False(t, IsUncertainTransaction(err))
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	client := &timeoutClient{
		Client:          &transactionTestClient{results: []ovsdb.OperationResult{{}}},
		name:            "interconnect northbound",
		uncertainWrites: true,
	}

	_, err := client.Transact(ctx, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Transit_Switch"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, IsUncertainTransaction(err))
}

func TestICNBMalformedReplyRetainsUncertainty(t *testing.T) {
	for _, response := range [][]ovsdb.OperationResult{nil, {{}, {}}, {{}, {}, {}}} {
		client := &timeoutClient{
			Client:          &transactionTestClient{results: response},
			name:            "interconnect northbound",
			uncertainWrites: true,
		}

		_, err := client.Transact(context.Background(), ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Transit_Switch"})
		require.Error(t, err)
		require.True(t, IsUncertainTransaction(err))
	}
}

func TestICNBCommitStageErrorRetainsUncertainty(t *testing.T) {
	client := &timeoutClient{
		Client:          &transactionTestClient{results: []ovsdb.OperationResult{{}, {Error: "I/O error", Details: "modeled commit acknowledgment lost"}}},
		name:            "interconnect northbound",
		uncertainWrites: true,
	}

	_, err := client.Transact(context.Background(), ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Transit_Switch"})
	require.ErrorContains(t, err, "modeled commit acknowledgment lost")
	require.True(t, IsUncertainTransaction(err))
}

func TestICNBReplyAtDeadlineIsCommittedOutcome(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		client := &timeoutClient{
			Client: &transactionTestClient{results: []ovsdb.OperationResult{{}}, replyAtDeadline: true},
			name:   "interconnect northbound", uncertainWrites: uncertain,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		results, err := client.Transact(ctx, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "Transit_Switch"})
		cancel()
		require.NoError(t, err, "a complete reply must not be reported as a failure and reverted")
		require.Equal(t, []ovsdb.OperationResult{{}}, results)
		require.False(t, client.unreachable)
	}
}
