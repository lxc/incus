package ovsdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	ovsmodel "github.com/lxc/incus/v7/internal/server/network/ovs/schema/ovs"
)

func fenceTestServer(t *testing.T) (func() ovsdbClient.Client, ovsdb.UUID) {
	t.Helper()
	server, err := exec.LookPath("ovsdb-server")
	if err != nil {
		t.Skip("ovsdb-server is required for real backend fence tests")
	}

	tool, err := exec.LookPath("ovsdb-tool")
	if err != nil {
		t.Skip("ovsdb-tool is required for real backend fence tests")
	}

	dir, err := os.MkdirTemp("", "ovsdb-fence-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	schema, err := json.Marshal(ovsmodel.Schema())
	require.NoError(t, err)
	schemaPath := filepath.Join(dir, "schema")
	require.NoError(t, os.WriteFile(schemaPath, schema, 0o600))
	dbPath := filepath.Join(dir, "db")
	output, err := exec.Command(tool, "create", dbPath, schemaPath).CombinedOutput()
	require.NoError(t, err, string(output))

	socket := filepath.Join(dir, "socket")
	var log bytes.Buffer
	cmd := exec.Command(server, dbPath, "--remote=punix:"+socket, "--unixctl="+filepath.Join(dir, "control"), "--pidfile="+filepath.Join(dir, "pid"), "--no-chdir")
	cmd.Stdout = &log
	cmd.Stderr = &log
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Log(log.String())
		}
	})
	require.Eventually(t, func() bool {
		_, err := os.Stat(socket)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	connect := func() ovsdbClient.Client {
		model, err := ovsmodel.FullDatabaseModel()
		require.NoError(t, err)
		logger := logr.Discard()
		client, err := ovsdbClient.NewOVSDBClient(model, ovsdbClient.WithEndpoint("unix:"+socket), ovsdbClient.WithLogger(&logger))
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, client.Connect(ctx))
		t.Cleanup(client.Close)
		return client
	}

	client := connect()
	ops := []ovsdb.Operation{{Op: ovsdb.OperationInsert, Table: "Open_vSwitch", Row: ovsdb.Row{"external_ids": ovsdb.OvsMap{GoMap: map[any]any{}}}}}
	reply, err := client.Transact(context.Background(), ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(reply, ops)
	require.NoError(t, err)
	return connect, reply[0].UUID
}

func fenceTestRoot(t *testing.T, client ovsdbClient.Client) ovsdb.Row {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ops := []ovsdb.Operation{{Op: ovsdb.OperationSelect, Table: "Open_vSwitch", Columns: []string{"_uuid", "external_ids", "next_cfg", "cur_cfg", "bridges"}}}
	reply, err := client.Transact(ctx, ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(reply, ops)
	require.NoError(t, err)
	require.Len(t, reply[0].Rows, 1)
	return reply[0].Rows[0]
}

func TestFencedClientResults(t *testing.T) {
	connect, root := fenceTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := NewFencedClient(ctx, connect(), "Open_vSwitch", "results")
	require.NoError(t, err)
	ops := []ovsdb.Operation{
		{Op: ovsdb.OperationInsert, Table: "Bridge", UUIDName: "new_bridge", Row: ovsdb.Row{"name": "fence-test"}},
		{Op: ovsdb.OperationMutate, Table: "Open_vSwitch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: root}}, Mutations: []ovsdb.Mutation{{Column: "bridges", Mutator: ovsdb.MutateOperationInsert, Value: ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "new_bridge"}}}}}},
	}

	reply, err := client.Transact(ctx, ops...)
	require.NoError(t, err)
	require.Len(t, reply, 2)
	require.NotEmpty(t, reply[0].UUID.GoUUID)
	require.Equal(t, 1, reply[1].Count)
	row := fenceTestRoot(t, connect())
	require.Equal(t, reply[0].UUID, row["bridges"])
	_, err = client.Transact(ctx, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: root}}, Row: ovsdb.Row{"next_cfg": "invalid-integer"}})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrFenced)
	require.Equal(t, row["external_ids"], fenceTestRoot(t, connect())["external_ids"])
}

// delayedFenceClient models transport buffering after the complete transaction has been constructed.
type delayedFenceClient struct {
	ovsdbClient.Client
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *delayedFenceClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	if c.entered != nil {
		c.once.Do(func() {
			close(c.entered)
			<-c.release
		})
	}

	return c.Client.Transact(ctx, operations...)
}

func TestFencedClientDelayedWholeMapWrite(t *testing.T) {
	connect, root := fenceTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	delayed := &delayedFenceClient{Client: connect()}
	old, err := NewFencedClient(ctx, delayed, "Open_vSwitch", "same-member")
	require.NoError(t, err)
	staleIDs := fenceTestRoot(t, connect())["external_ids"]
	delayed.entered = make(chan struct{})
	delayed.release = make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(delayed.release) }) })
	done := make(chan error, 1)
	go func() {
		_, err := old.Transact(ctx, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: root}}, Row: ovsdb.Row{"external_ids": staleIDs, "next_cfg": 99}})
		done <- err
	}()
	select {
	case <-delayed.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	_, err = NewFencedClient(ctx, connect(), "Open_vSwitch", "same-member")
	require.NoError(t, err)
	current := fenceTestRoot(t, connect())
	release.Do(func() { close(delayed.release) })
	select {
	case err = <-done:
		require.ErrorIs(t, err, ErrFenced)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	after := fenceTestRoot(t, connect())
	require.Equal(t, current["external_ids"], after["external_ids"])
	require.Equal(t, current["next_cfg"], after["next_cfg"])
}

func TestFencedClientDeadlineFencesBeforeRelease(t *testing.T) {
	connect, root := fenceTestServer(t)
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer setupCancel()
	client, err := NewFencedClient(setupCtx, connect(), "Open_vSwitch", "deadline")
	require.NoError(t, err)
	initialIDs := fenceTestRoot(t, connect())["external_ids"]
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	where := []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: root}}
	serverTimeout := 5000
	done := make(chan error, 1)
	go func() {
		_, err := client.Transact(ctx,
			ovsdb.Operation{Op: ovsdb.OperationWait, Table: "Open_vSwitch", Where: where, Columns: []string{"cur_cfg"}, Until: "==", Rows: []ovsdb.Row{{"cur_cfg": 1}}, Timeout: &serverTimeout},
			ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: where, Row: ovsdb.Row{"next_cfg": 7}})
		done <- err
	}()
	select {
	case err = <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-setupCtx.Done():
		t.Fatal(setupCtx.Err())
	}

	require.NotEqual(t, initialIDs, fenceTestRoot(t, connect())["external_ids"])

	other := connect()
	ops := []ovsdb.Operation{{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: where, Row: ovsdb.Row{"cur_cfg": 1}}}
	reply, err := other.Transact(setupCtx, ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(reply, ops)
	require.NoError(t, err)
	require.EqualValues(t, 0, fenceTestRoot(t, other)["next_cfg"])
}

// admissionFenceClient models disconnect observation and the library's pre-send reconnect timeout.
type admissionFenceClient struct {
	ovsdbClient.Client
	disconnected   bool
	awaitReconnect bool
	calls          int
}

func (c *admissionFenceClient) Connected() bool {
	return !c.disconnected && c.Client.Connected()
}

func (c *admissionFenceClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	c.calls++
	if c.awaitReconnect {
		c.awaitReconnect = false
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: while awaiting reconnection", ctx.Err())
		case <-time.After(100 * time.Millisecond):
			return nil, errors.New("Caller deadline was not passed to raw reconnect wait")
		}
	}

	return c.Client.Transact(ctx, operations...)
}

func TestFencedClientNotDispatchedTransactions(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		disconnected   bool
		awaitReconnect bool
		operation      ovsdb.Operation
	}{
		{name: "disconnected observation", disconnected: true, operation: ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Row: ovsdb.Row{"next_cfg": 99}}},
		{name: "invalid column", operation: ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Row: ovsdb.Row{"missing_column": 99}}},
		{name: "reconnect wait before send", awaitReconnect: true, operation: ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Row: ovsdb.Row{"next_cfg": 99}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			connect, _ := fenceTestServer(t)
			setupCtx, cancelSetup := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelSetup()
			raw := &admissionFenceClient{Client: connect()}
			client, err := NewFencedClient(setupCtx, raw, "Open_vSwitch", "not-sent")
			require.NoError(t, err)
			before := fenceTestRoot(t, connect())
			calls := raw.calls
			raw.disconnected = testCase.disconnected
			raw.awaitReconnect = testCase.awaitReconnect
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_, err = client.Transact(ctx, testCase.operation)
			require.Error(t, err)
			if testCase.awaitReconnect {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Equal(t, calls+1, raw.calls)
			} else {
				require.Equal(t, calls, raw.calls)
			}

			after := fenceTestRoot(t, connect())
			require.Equal(t, before["external_ids"], after["external_ids"])
			require.Equal(t, before["next_cfg"], after["next_cfg"])
		})
	}
}

func TestFencedClientAdmissionDeadlinePreservesOwner(t *testing.T) {
	connect, root := fenceTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	delayed := &delayedFenceClient{Client: connect()}
	client, err := NewFencedClient(ctx, delayed, "Open_vSwitch", "admission")
	require.NoError(t, err)
	delayed.entered = make(chan struct{})
	delayed.release = make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(delayed.release) }) })
	done := make(chan error, 1)
	go func() {
		_, err := client.Transact(ctx, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: root}}, Row: ovsdb.Row{"next_cfg": 7}})
		done <- err
	}()
	select {
	case <-delayed.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancelWait()
	_, err = client.Transact(waitCtx, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Row: ovsdb.Row{"next_cfg": 99}})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case err = <-done:
		t.Fatalf("Waiting caller released the held original transaction: %v", err)
	default:
	}

	release.Do(func() { close(delayed.release) })
	select {
	case err = <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	require.EqualValues(t, 7, fenceTestRoot(t, connect())["next_cfg"])
}

// conflictingFenceClient changes real external IDs between each constructor snapshot and CAS.
type conflictingFenceClient struct {
	ovsdbClient.Client
	root            ovsdb.UUID
	attempts        int
	permissionError bool
}

func (c *conflictingFenceClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	if len(operations) == 2 && operations[1].Op == ovsdb.OperationMutate {
		c.attempts++
		if c.permissionError {
			return []ovsdb.OperationResult{{}, {Error: "permission error", Details: "test role cannot modify root"}}, nil
		}

		ops := []ovsdb.Operation{{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: c.root}}, Row: ovsdb.Row{"external_ids": ovsdb.OvsMap{GoMap: map[any]any{"test-conflict": strconv.Itoa(c.attempts)}}}}}
		reply, err := c.Client.Transact(ctx, ops...)
		if err != nil {
			return nil, err
		}

		err = transactionResultError(reply, ops)
		if err != nil {
			return nil, err
		}
	}

	return c.Client.Transact(ctx, operations...)
}

func TestFencedClientConstructorRetryDiagnostics(t *testing.T) {
	connect, root := fenceTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	raw := &conflictingFenceClient{Client: connect(), root: root}
	_, err := NewFencedClient(ctx, raw, "Open_vSwitch", "constructor-conflict")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "timed out")
	require.GreaterOrEqual(t, raw.attempts, 1)
	require.LessOrEqual(t, raw.attempts, 5)

	permissionCtx, cancelPermission := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPermission()
	permission := &conflictingFenceClient{Client: connect(), root: root, permissionError: true}
	_, err = NewFencedClient(permissionCtx, permission, "Open_vSwitch", "constructor-permission")
	require.ErrorContains(t, err, "test role cannot modify root")
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, permission.attempts)
}

var errFenceTestLostReply = errors.New("test transport lost the backend reply")

// lostReplyFenceClient returns before a real server-side waiting transaction finishes.
type lostReplyFenceClient struct {
	ovsdbClient.Client
	loseNext       bool
	malformed      bool
	transportError error
	commitError    bool
	timedOut       chan struct{}
	release        chan struct{}
}

func (c *lostReplyFenceClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	if !c.loseNext {
		return c.Client.Transact(ctx, operations...)
	}

	c.loseNext = false
	deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	reply, err := c.Client.Transact(deadline, operations...)
	if errors.Is(err, context.DeadlineExceeded) {
		close(c.timedOut)
		<-c.release
		if c.malformed {
			return []ovsdb.OperationResult{}, nil
		}

		if c.commitError {
			result := make([]ovsdb.OperationResult, len(operations)+1)
			result[len(operations)] = ovsdb.OperationResult{Error: "I/O error", Details: "modeled commit acknowledgment lost"}
			return result, nil
		}

		if c.transportError != nil {
			return nil, c.transportError
		}

		return nil, errFenceTestLostReply
	}

	return reply, err
}

func TestFencedClientUncertainTransport(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		superseded     bool
		malformed      bool
		transportError error
		commitError    bool
	}{
		{name: "same daemon"},
		{name: "superseded daemon", superseded: true},
		{name: "malformed reply", malformed: true},
		{name: "post-send not connected", transportError: ovsdbClient.ErrNotConnected},
		{name: "commit-stage error", commitError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			connect, root := fenceTestServer(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			transport := &lostReplyFenceClient{Client: connect(), malformed: testCase.malformed, commitError: testCase.commitError, transportError: testCase.transportError, timedOut: make(chan struct{}), release: make(chan struct{})}
			client, err := NewFencedClient(ctx, transport, "Open_vSwitch", "uncertain")
			require.NoError(t, err)
			initialIDs := fenceTestRoot(t, connect())["external_ids"]
			where := []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: root}}
			serverTimeout := 5000
			transport.loseNext = true
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(transport.release) }) })
			done := make(chan error, 1)
			go func() {
				_, err := client.Transact(ctx,
					ovsdb.Operation{Op: ovsdb.OperationWait, Table: "Open_vSwitch", Where: where, Columns: []string{"cur_cfg"}, Until: "==", Rows: []ovsdb.Row{{"cur_cfg": 1}}, Timeout: &serverTimeout},
					ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: where, Row: ovsdb.Row{"next_cfg": 99}})
				done <- err
			}()
			select {
			case <-transport.timedOut:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}

			var replacement *FencedClient
			if testCase.superseded {
				replacement, err = NewFencedClient(ctx, connect(), "Open_vSwitch", "uncertain")
				require.NoError(t, err)
			}

			release.Do(func() { close(transport.release) })
			select {
			case err = <-done:
				require.Error(t, err)
				if testCase.commitError {
					require.ErrorContains(t, err, "modeled commit acknowledgment lost")
				} else if !testCase.malformed {
					expected := testCase.transportError
					if expected == nil {
						expected = errFenceTestLostReply
					}

					require.ErrorIs(t, err, expected)
				}

				if testCase.superseded {
					require.ErrorIs(t, err, ErrFenced)
				}

			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}

			other := connect()
			require.NotEqual(t, initialIDs, fenceTestRoot(t, other)["external_ids"])
			ops := []ovsdb.Operation{{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: where, Row: ovsdb.Row{"cur_cfg": 1}}}
			reply, err := other.Transact(ctx, ops...)
			require.NoError(t, err)
			_, err = ovsdb.CheckOperationResults(reply, ops)
			require.NoError(t, err)
			require.EqualValues(t, 0, fenceTestRoot(t, other)["next_cfg"])

			if replacement != nil {
				_, err = replacement.Transact(ctx, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: where, Row: ovsdb.Row{"next_cfg": 8}})
				require.NoError(t, err)
			}
		})
	}
}

// redirectedFenceClient changes only transaction routing and observes replies from the replacement server.
type redirectedFenceClient struct {
	ovsdbClient.Client
	mu             sync.Mutex
	other          ovsdbClient.Client
	otherRoot      ovsdb.UUID
	redirected     bool
	otherSnapshots int
	repeatedRoot   chan ovsdb.UUID
}

func (c *redirectedFenceClient) redirect(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.redirected = enabled
}

func (c *redirectedFenceClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	c.mu.Lock()
	redirected := c.redirected
	client := c.Client
	if redirected {
		client = c.other
	}

	c.mu.Unlock()
	reply, err := client.Transact(ctx, operations...)
	if redirected && err == nil && len(operations) == 1 && operations[0].Op == ovsdb.OperationSelect && len(reply) == 1 && len(reply[0].Rows) == 1 {
		root, ok := reply[0].Rows[0]["_uuid"].(ovsdb.UUID)
		if ok && root == c.otherRoot {
			c.mu.Lock()
			c.otherSnapshots++
			if c.otherSnapshots == 2 {
				c.repeatedRoot <- root
			}

			c.mu.Unlock()
		}
	}

	return reply, err
}

func TestFencedClientUncertainTransportDifferentBackend(t *testing.T) {
	connectA, rootA := fenceTestServer(t)
	connectB, rootB := fenceTestServer(t)
	require.NotEqual(t, rootA, rootB)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	routing := &redirectedFenceClient{
		Client: connectA(), other: connectB(), otherRoot: rootB, repeatedRoot: make(chan ovsdb.UUID, 1),
	}

	transport := &lostReplyFenceClient{Client: routing, timedOut: make(chan struct{}), release: make(chan struct{})}
	client, err := NewFencedClient(ctx, transport, "Open_vSwitch", "backend-replacement")
	require.NoError(t, err)
	require.Equal(t, rootA.GoUUID, client.RootUUID())
	initialA := fenceTestRoot(t, connectA())["external_ids"]
	initialB := fenceTestRoot(t, connectB())["external_ids"]
	whereA := []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: rootA}}
	serverTimeout := 15000
	transport.loseNext = true
	var release sync.Once
	done := make(chan error, 1)
	finished := make(chan struct{})
	t.Cleanup(func() {
		routing.redirect(false)
		release.Do(func() { close(transport.release) })
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("Original backend transaction goroutine did not finish after routing restoration")
		}
	})
	go func() {
		defer close(finished)
		_, err := client.Transact(ctx,
			ovsdb.Operation{Op: ovsdb.OperationWait, Table: "Open_vSwitch", Where: whereA, Columns: []string{"cur_cfg"}, Until: "==", Rows: []ovsdb.Row{{"cur_cfg": 1}}, Timeout: &serverTimeout},
			ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: whereA, Row: ovsdb.Row{"next_cfg": 99}})
		done <- err
	}()
	select {
	case <-transport.timedOut:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	// The real write was sent to A; only its later fence requests now reach B.
	routing.redirect(true)
	release.Do(func() { close(transport.release) })
	select {
	case err = <-done:
		t.Fatalf("Released original backend ownership using a different backend: %v", err)
	case observed := <-routing.repeatedRoot:
		require.Equal(t, rootB, observed)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	require.Equal(t, initialA, fenceTestRoot(t, connectA())["external_ids"])
	require.Equal(t, initialB, fenceTestRoot(t, connectB())["external_ids"])
	select {
	case err = <-done:
		t.Fatalf("Ownership escaped while the original backend remained unfenced: %v", err)
	default:
	}

	routing.redirect(false)
	select {
	case err = <-done:
		require.ErrorIs(t, err, errFenceTestLostReply)
		require.NotErrorIs(t, err, ErrFenced)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	require.Equal(t, rootA.GoUUID, client.RootUUID())
	require.NotEqual(t, initialA, fenceTestRoot(t, connectA())["external_ids"])
	require.Equal(t, initialB, fenceTestRoot(t, connectB())["external_ids"])
	other := connectA()
	ops := []ovsdb.Operation{{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: whereA, Row: ovsdb.Row{"cur_cfg": 1}}}
	reply, err := other.Transact(ctx, ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(reply, ops)
	require.NoError(t, err)
	require.EqualValues(t, 0, fenceTestRoot(t, other)["next_cfg"])
}

// bufferedFenceWriteClient retains one constructed write after its constructor is canceled.
type bufferedFenceWriteClient struct {
	ovsdbClient.Client
	buffered chan []ovsdb.Operation
}

func (c *bufferedFenceWriteClient) Transact(ctx context.Context, operations ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	for _, op := range operations {
		if op.Op == ovsdb.OperationMutate {
			c.buffered <- operations
			<-ctx.Done()
			return nil, ctx.Err()
		}
	}

	return c.Client.Transact(ctx, operations...)
}

func TestFencedClientDelayedConstructorWrite(t *testing.T) {
	connect, root := fenceTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := NewFencedClient(ctx, connect(), "Open_vSwitch", "constructor")
	require.NoError(t, err)
	oldCtx, stopOld := context.WithCancel(ctx)
	defer stopOld()
	buffered := &bufferedFenceWriteClient{Client: connect(), buffered: make(chan []ovsdb.Operation, 1)}
	done := make(chan error, 1)
	go func() {
		_, err := NewFencedClient(oldCtx, buffered, "Open_vSwitch", "constructor")
		done <- err
	}()
	var delayed []ovsdb.Operation
	select {
	case delayed = <-buffered.buffered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	stopOld()
	select {
	case err = <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	replacement, err := NewFencedClient(ctx, connect(), "Open_vSwitch", "constructor")
	require.NoError(t, err)
	current := fenceTestRoot(t, connect())["external_ids"]
	other := connect()
	reply, err := other.Transact(ctx, delayed...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(reply, delayed)
	require.Error(t, err)
	require.Equal(t, current, fenceTestRoot(t, other)["external_ids"])
	_, err = replacement.Transact(ctx, ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: root}}, Row: ovsdb.Row{"next_cfg": 8}})
	require.NoError(t, err)
}
