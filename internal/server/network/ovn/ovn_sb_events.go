package ovn

import (
	"context"
	"fmt"
	"slices"
	"sync"

	ovsdbModel "github.com/ovn-kubernetes/libovsdb/model"

	"github.com/lxc/incus/v7/internal/server/locking"
)

type sbEventHandler struct {
	EventHandler
	mu      sync.Mutex
	stopped bool
}

func (h *sbEventHandler) run(action string, table string, oldObject ovsdbModel.Model, newObject ovsdbModel.Model) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.stopped {
		h.Hook(action, table, oldObject, newObject)
	}
}

func (h *sbEventHandler) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.stopped = true
}

var (
	sbEventHandlers   map[string]*sbEventHandler
	sbEventHandlersMu sync.Mutex
)

// AddOVNSBHandler registers a new event handler after draining any previous handler.
func AddOVNSBHandler(name string, handler EventHandler) error {
	unlock, err := locking.Lock(context.Background(), fmt.Sprintf("ovn.sb.handler.%q", name))
	if err != nil {
		return err
	}

	defer unlock()

	removeOVNSBHandler(name)

	sbEventHandlersMu.Lock()
	defer sbEventHandlersMu.Unlock()

	if sbEventHandlers == nil {
		sbEventHandlers = map[string]*sbEventHandler{}
	}

	sbEventHandlers[name] = &sbEventHandler{EventHandler: handler}

	return nil
}

// RemoveOVNSBHandler gates queued hooks and waits for the executing hook to finish.
func RemoveOVNSBHandler(name string) error {
	unlock, err := locking.Lock(context.Background(), fmt.Sprintf("ovn.sb.handler.%q", name))
	if err != nil {
		return err
	}

	defer unlock()

	removeOVNSBHandler(name)
	return nil
}

func removeOVNSBHandler(name string) {
	sbEventHandlersMu.Lock()
	handler := sbEventHandlers[name]
	delete(sbEventHandlers, name)
	sbEventHandlersMu.Unlock()

	if handler != nil {
		handler.stop()
	}
}

func dispatchOVNSBEvent(action string, table string, oldObject ovsdbModel.Model, newObject ovsdbModel.Model) {
	sbEventHandlersMu.Lock()
	defer sbEventHandlersMu.Unlock()

	for _, handler := range sbEventHandlers {
		if handler.Hook != nil && slices.Contains(handler.Tables, table) {
			go handler.run(action, table, oldObject, newObject)
		}
	}
}
