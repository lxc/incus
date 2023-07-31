package config

import (
	"fmt"
	"sort"
)

// Error generated when trying to set a certain config key to certain value.
type Error struct {
	Name   string // The name of the key this error is associated with.
	Value  any    // The value that the key was tried to be set to.
	Reason string // Human-readable reason of the error.
}

// Error implements the error interface.
func (e Error) Error() string {
	message := fmt.Sprintf("cannot set '%s'", e.Name)
	if e.Value != nil {
		message += fmt.Sprintf(" to '%v'", e.Value)
	}

	return message + fmt.Sprintf(": %s", e.Reason)
}

// ErrorList is a list of configuration Errors occurred during Load() or
// Map.Change().
type ErrorList []*Error

// ErrorList implements the error interface.
func (l ErrorList) Error() string {
	switch len(l) {
	case 0:
		return "no errors"
	case 1:
		return l[0].Error()
	}

	return fmt.Sprintf("%s (and %d more errors)", l[0], len(l)-1)
}

// ErrorList implements the sort Interface.
func (l ErrorList) Len() int           { return len(l) }
func (l ErrorList) Swap(i, j int)      { l[i], l[j] = l[j], l[i] }
func (l ErrorList) Less(i, j int) bool { return l[i].Name < l[j].Name }

// Sort sorts an ErrorList. *Error entries are sorted by key name.
func (l ErrorList) sort() { sort.Sort(l) }

// Add adds an Error with given key name, value and reason.
func (l *ErrorList) add(name string, value any, reason string) {
	*l = append(*l, &Error{name, value, reason})
}
