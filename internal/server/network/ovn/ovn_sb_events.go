package ovn

import (
	"sync"
)

var (
	sbEventHandlers   map[string]EventHandler
	sbEventHandlersMu sync.Mutex
)

// AddOVNSBHandler registers a new event handler with the OVN Southbound database.
func AddOVNSBHandler(name string, handler EventHandler) error {
	sbEventHandlersMu.Lock()
	defer sbEventHandlersMu.Unlock()

	if sbEventHandlers == nil {
		sbEventHandlers = map[string]EventHandler{}
	}

	sbEventHandlers[name] = handler

	return nil
}

// RemoveOVNSBHandler removes a currently registered event handler.
func RemoveOVNSBHandler(name string) error {
	sbEventHandlersMu.Lock()
	defer sbEventHandlersMu.Unlock()

	if sbEventHandlers == nil {
		return nil
	}

	delete(sbEventHandlers, name)

	return nil
}
