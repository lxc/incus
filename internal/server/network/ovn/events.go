package ovn

import (
	ovsdbModel "github.com/ovn-org/libovsdb/model"
)

// EventHandler represents an OVN database event handler.
type EventHandler struct {
	// Tables contains the list of OVN database tables to watch for events.
	Tables []string

	// Hook is the function being called on a matching event.
	Hook func(action string, table string, oldObject ovsdbModel.Model, newObject ovsdbModel.Model)
}
