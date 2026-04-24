package qmp

import (
	"sync"
	"testing"
)


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
