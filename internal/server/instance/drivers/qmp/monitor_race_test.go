package qmp

import (
	"sync"
	"testing"
)

// TestRunJSONConcurrentDisconnect reproduces a nil-pointer dereference
// in RunJSON when a concurrent Disconnect nils the QMP socket state
// while RunJSON is accessing m.qmp.log.
//
// The race: Disconnect() calls m.qmp.disconnect() which closes the
// underlying unix socket. A concurrent RunJSON checks m.disconnected
// (still false), then dereferences m.qmp.log. If m.qmp's internal
// state is partially torn down (e.g., log closed/nilled by disconnect),
// or if the Monitor is reused from the global map in a state where
// qmp was never fully initialized, the dereference panics.
//
// Run with: go test -race -count=100 -run TestRunJSONConcurrentDisconnect
func TestRunJSONConcurrentDisconnect(t *testing.T) {
	// Create a minimal Monitor with a nil qmp to simulate the
	// post-disconnect state. This is the simplest reproduction:
	// m.qmp is nil but m.disconnected hasn't been set yet.
	m := &Monitor{
		qmp:          nil, // simulates torn-down state
		disconnected: false,
		chDisconnect: make(chan struct{}, 1),
		eventMap:     make(map[string]chan Event),
	}

	// RunJSON should not panic when m.qmp is nil.
	// It should return ErrMonitorDisconnect or similar.
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("RunJSON panicked with nil m.qmp: %v", r)
				}
			}()

			// This will panic on current code because RunJSON
			// accesses m.qmp.log without checking m.qmp != nil.
			_ = m.RunJSON([]byte(`{"execute":"quit"}`), nil, true, 1)
		}()
	}

	wg.Wait()
}

// TestRunJSONAfterDisconnect verifies RunJSON is safe to call after
// Disconnect has been called (m.disconnected = true).
func TestRunJSONAfterDisconnect(t *testing.T) {
	m := &Monitor{
		qmp:          nil,
		disconnected: true,
		chDisconnect: make(chan struct{}, 1),
		eventMap:     make(map[string]chan Event),
	}

	err := m.RunJSON([]byte(`{"execute":"quit"}`), nil, true, 1)
	if err != ErrMonitorDisconnect {
		t.Errorf("expected ErrMonitorDisconnect, got %v", err)
	}
}
