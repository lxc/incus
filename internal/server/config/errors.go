package config

import (
	"fmt"
	"sort"
	"strings"
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
type ErrorList struct {
	errors []Error
}

// ErrorList implements the error interface.
func (l *ErrorList) Error() string {
	errorCount := l.Len()
	if errorCount == 0 {
		return "no errors"
	}

	errorMessage := strings.Builder{}

	firstError := l.errors[0].Error()
	errorMessage.WriteString(firstError)

	if errorCount > 1 {
		errorMessage.WriteString(fmt.Sprintf(" (and %d more errors)", errorCount-1))
	}

	return errorMessage.String()
}

// Len returns the amount of errors contained in the list. This is needed to implement the sort Interface.
func (l *ErrorList) Len() int { return len(l.errors) }

// Swap swaps two errors at two indices. This is needed to implement the sort Interface.
func (l *ErrorList) Swap(i, j int) { l.errors[i], l.errors[j] = l.errors[j], l.errors[i] }

// Less defines an ordering of errors inside the error list. This is needed to implement the sort Interface.
func (l *ErrorList) Less(i, j int) bool { return l.errors[i].Name < l.errors[j].Name }

// Sort sorts an ErrorList. *Error entries are sorted by key name.
func (l *ErrorList) sort() { sort.Sort(l) }

// Add adds an Error with given key name, value and reason.
func (l *ErrorList) add(name string, value any, reason string) {
	l.errors = append(l.errors, Error{name, value, reason})
}
