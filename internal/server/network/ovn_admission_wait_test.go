//go:build linux && cgo && !agent

package network

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cowsql/go-cowsql/driver"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/internal/server/sys"
	"github.com/lxc/incus/v7/shared/api"
)

func newOVNWaitTestNetwork(t *testing.T) (*ovn, *atomic.Int32) {
	t.Helper()
	dir, store, serverCleanup := db.NewTestCowsqlServer(t)
	serverCleanup = sync.OnceFunc(serverCleanup)
	t.Cleanup(serverCleanup)
	members, err := store.Get(context.Background())
	require.NoError(t, err)
	require.Len(t, members, 1)
	address := members[0].Address
	dial := func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", address)
	}

	cluster, err := db.OpenCluster(context.Background(), "test.db", store, "1", dir, 5*time.Second, driver.WithDialFunc(dial))
	require.NoError(t, err)
	t.Logf("owned private native fixture started: dir=%q socket=%q", dir, address)
	t.Cleanup(func() {
		clusterErr := cluster.Close()
		serverCleanup()
		require.NoError(t, clusterErr)
		_, err := os.Stat(dir)
		require.True(t, errors.Is(err, os.ErrNotExist), "owned server directory remains: %v", err)
		conn, err := net.DialTimeout("unix", address, time.Second)
		if conn != nil {
			_ = conn.Close()
		}

		require.Error(t, err, "owned native socket still accepts connections after server.Close")
		t.Logf("owned private native fixture closed: cluster.Close, server.Close, removed directory, socket dial=%v", err)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	calls := &atomic.Int32{}
	s := &state.State{ShutdownCtx: ctx, DB: &db.DB{Cluster: cluster}, OS: &sys.OS{}}
	s.OVN = func() (*networkOVN.NB, *networkOVN.SB, error) {
		calls.Add(1)
		return &networkOVN.NB{}, &networkOVN.SB{}, nil
	}

	var id int64
	err = cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		id, err = tx.CreateNetwork(ctx, api.ProjectDefaultName, "c015-wait", "", db.NetworkTypeOVN, map[string]string{"user.phase": "before"})
		if err != nil {
			return err
		}

		return tx.NetworkNodeCreated(id)
	})
	require.NoError(t, err)
	loaded, err := LoadByName(s, api.ProjectDefaultName, "c015-wait")
	require.NoError(t, err)
	n, ok := loaded.(*ovn)
	require.True(t, ok)
	require.Equal(t, id, n.ID())
	ovnLocalStates.Lock()
	previous, existed := ovnLocalStates.members[id]
	ovnLocalStates.Unlock()
	pn := ProjectNetwork{ProjectName: n.project, NetworkName: n.name}
	unavailableNetworksMu.Lock()
	_, unavailable := unavailableNetworks[pn]
	unavailableNetworksMu.Unlock()
	t.Cleanup(func() {
		ovnLocalStates.Lock()
		if existed {
			ovnLocalStates.members[id] = previous
		} else {
			delete(ovnLocalStates.members, id)
		}

		ovnLocalStates.Unlock()
		unavailableNetworksMu.Lock()
		if unavailable {
			unavailableNetworks[pn] = struct{}{}
		} else {
			delete(unavailableNetworks, pn)
		}

		unavailableNetworksMu.Unlock()
		t.Log("restored exact prior process-local readiness and availability entries")
	})
	n.setLocalState(ovnLocalState{})
	n.setUnavailable()
	calls.Store(0)
	return n, calls
}

func queuedOVNAdmission(t *testing.T, n *ovn, cancel context.CancelFunc) <-chan error {
	t.Helper()
	result := make(chan error, 1)
	identity := make(chan string, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		b := make([]byte, 256)
		size := runtime.Stack(b, false)
		identity <- strings.Fields(string(b[:size]))[1]
		result <- EnsureOVNLocal(n)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
			t.Log("actual admission caller joined")
		case <-time.After(5 * time.Second):
			t.Error("actual admission caller was not joined")
		}
	})
	var id string
	select {
	case id = <-identity:
	case <-time.After(5 * time.Second):
		t.Fatal("caller identity was not delivered")
	}

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		b := make([]byte, 1<<20)
		size := runtime.Stack(b, true)
		for _, stack := range strings.Split(string(b[:size]), "\n\n") {
			if strings.HasPrefix(stack, "goroutine "+id+" [select]:") && strings.Contains(stack, "/internal/server/locking.Lock(") && strings.Contains(stack, "/internal/server/network.WaitOVNLifecycle(") && strings.Contains(stack, "/internal/server/network.EnsureOVNLocal(") {
				_, err := strconv.Atoi(id)
				require.NoError(t, err)
				t.Logf("actual ordinary admission caller registered in held lifecycle lock select:\n%s", stack)
				return result
			}
		}

		select {
		case err := <-result:
			t.Fatalf("admission exited before held-lock registration: %v", err)
		case <-deadline.C:
			t.Fatal("actual locking.Lock select registration was not observed")
		default:
			runtime.Gosched()
		}
	}
}

func TestOVNAdmissionWaitBoundary(t *testing.T) {
	for _, mode := range []string{"cancel", "latest-ready", "latest-maintenance", "identity-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			n, calls := newOVNWaitTestNetwork(t)
			ctx, cancel := context.WithCancel(n.state.ShutdownCtx)
			n.state.ShutdownCtx = ctx
			t.Cleanup(cancel)
			unlock, err := LockOVNLifecycle(n.Project(), n.Name())
			require.NoError(t, err)
			t.Cleanup(func() {
				unlock()
				probe, err := LockOVNLifecycle(n.Project(), n.Name())
				require.NoError(t, err)
				probe()
				t.Log("owned lifecycle lock released and positively reacquired/released")
			})
			result := queuedOVNAdmission(t, n, cancel)
			require.Zero(t, calls.Load(), "reload constructed an OVN client before lock release")
			require.Equal(t, "before", n.Config()["user.phase"])
			require.False(t, n.localState().started)
			if mode == "cancel" {
				cancel()
			} else {
				err = n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					err := tx.UpdateNetwork(ctx, n.Project(), n.Name(), "current", map[string]string{"user.phase": "after"})
					if err != nil {
						return err
					}

					if mode == "latest-maintenance" {
						return tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateEvacuated)
					}

					return nil
				})
				require.NoError(t, err)
				n.setLocalState(ovnLocalState{started: true})
				n.setAvailable()
				if mode == "identity-mismatch" {
					n.id++
				}

				require.Zero(t, calls.Load(), "reload reached OVN init while owned lock remained held")
				unlock()
			}

			select {
			case err = <-result:
			case <-time.After(5 * time.Second):
				t.Fatal("ordinary caller did not settle after cancellation or release")
			}

			if mode == "cancel" {
				require.ErrorIs(t, err, context.Canceled)
				probe, heldErr := LockOVNLifecycle(n.Project(), n.Name())
				require.Nil(t, probe)
				require.True(t, api.StatusErrorCheck(heldErr, http.StatusConflict))
				require.Zero(t, calls.Load())
				require.Equal(t, "before", n.Config()["user.phase"])
				require.False(t, n.localState().started)
				t.Logf("canceled queued caller refused before reload/readiness: %v", err)
				return
			}

			require.EqualValues(t, 1, calls.Load(), "exactly one current network reload expected")
			if mode == "identity-mismatch" {
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
				require.ErrorContains(t, err, "has been replaced")
				require.Equal(t, "before", n.Config()["user.phase"])
				t.Logf("stale caller identity strictly refused: %v", err)
				return
			}

			require.Equal(t, "after", n.Config()["user.phase"])
			require.Equal(t, "current", n.description)
			require.Equal(t, api.NetworkStatusCreated, n.rawLocalStatus())
			if mode == "latest-maintenance" {
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
				require.ErrorContains(t, err, "not active")
				t.Logf("reloaded latest config then strictly refused current maintenance state: %v", err)
			} else {
				require.NoError(t, err)
				require.Equal(t, api.NetworkStatusCreated, n.LocalStatus())
				t.Log("ordinary caller succeeded only after release, current config reload and current local readiness")
			}
		})
	}
}
