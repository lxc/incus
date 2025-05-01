package config

import (
	"fmt"
	"sort"
	"strings"
)

// configurationError is generated when trying to set a config key to an erroneous value.
type configurationError struct {
	configKey      string
	erroneousValue any
	reason         string
}

// ConfigurationError implements the error interface.
func (e configurationError) Error() string {
	message := fmt.Sprintf("cannot set '%s'", e.configKey)
	if e.erroneousValue != nil {
		message += fmt.Sprintf(" to '%v'", e.erroneousValue)
	}

	return message + fmt.Sprintf(": %s", e.reason)
}

// ErrorList is a list of configuration Errors occurred during Load() or Map.Change().
type ErrorList struct {
	errors []configurationError
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
func (l *ErrorList) Less(i, j int) bool { return l.errors[i].configKey < l.errors[j].configKey }

// sort sorts an ErrorList. *ConfigurationError entries are sorted by key name.
func (l *ErrorList) sort() { sort.Sort(l) }

// add adds an ConfigurationError with given key name, value and reason.
func (l *ErrorList) add(configKey string, erroneousValue any, errorReason string) {
	l.errors = append(l.errors, configurationError{configKey, erroneousValue, errorReason})
}
