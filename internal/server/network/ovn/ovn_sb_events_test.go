package ovn

import (
	"sync/atomic"
	"testing"
	"time"

	ovsdbModel "github.com/ovn-kubernetes/libovsdb/model"
)

func TestOVNSBHandlerRemovalDrainsAndGates(t *testing.T) {
	const name = "test-drain"
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	err := AddOVNSBHandler(name, EventHandler{
		Tables: []string{"Port_Binding"},
		Hook: func(string, string, ovsdbModel.Model, ovsdbModel.Model) {
			calls.Add(1)
			close(entered)
			<-release
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	sbEventHandlersMu.Lock()
	oldHandler := sbEventHandlers[name]
	sbEventHandlersMu.Unlock()

	dispatchOVNSBEvent("add", "Port_Binding", nil, nil)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Hook did not start")
	}

	removed := make(chan error, 1)
	go func() { removed <- RemoveOVNSBHandler(name) }()

	select {
	case err := <-removed:
		t.Fatalf("Removal returned before the hook finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-removed:
		if err != nil {
			t.Fatal(err)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("Removal did not finish after the hook returned")
	}

	// A dispatched goroutine retaining the old handler must not resurrect local state.
	oldHandler.run("add", "Port_Binding", nil, nil)
	dispatchOVNSBEvent("add", "Port_Binding", nil, nil)
	if calls.Load() != 1 {
		t.Fatalf("Removed handler was called %d times", calls.Load())
	}
}

func TestOVNSBHandlerReplacementGatesOldHooks(t *testing.T) {
	const name = "test-replace"
	entered := make(chan struct{})
	release := make(chan struct{})
	var oldCalls atomic.Int64
	err := AddOVNSBHandler(name, EventHandler{Hook: func(string, string, ovsdbModel.Model, ovsdbModel.Model) {
		oldCalls.Add(1)
		close(entered)
		<-release
	}})
	if err != nil {
		t.Fatal(err)
	}

	sbEventHandlersMu.Lock()
	oldHandler := sbEventHandlers[name]
	sbEventHandlersMu.Unlock()
	go oldHandler.run("add", "Port_Binding", nil, nil)
	<-entered

	var newCalls atomic.Int64
	replaced := make(chan error, 1)
	go func() {
		replaced <- AddOVNSBHandler(name, EventHandler{Hook: func(string, string, ovsdbModel.Model, ovsdbModel.Model) { newCalls.Add(1) }})
	}()

	select {
	case err := <-replaced:
		t.Fatalf("Replacement returned before the old hook drained: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-replaced:
		if err != nil {
			t.Fatal(err)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("Replacement did not finish")
	}

	oldHandler.run("add", "Port_Binding", nil, nil)
	sbEventHandlersMu.Lock()
	newHandler := sbEventHandlers[name]
	sbEventHandlersMu.Unlock()
	newHandler.run("add", "Port_Binding", nil, nil)
	if oldCalls.Load() != 1 || newCalls.Load() != 1 {
		t.Fatalf("Old/new calls after replacement: %d/%d", oldCalls.Load(), newCalls.Load())
	}

	err = RemoveOVNSBHandler(name)
	if err != nil {
		t.Fatal(err)
	}
}
