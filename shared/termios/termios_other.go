//go:build !linux

package termios

import (
	"golang.org/x/term"
)

// State contains the state of a terminal.
type State term.State

// IsTerminal returns true if the given file descriptor is a terminal.
func IsTerminal(fd int) bool {
	return term.IsTerminal(fd)
}

// GetState returns the current state of a terminal which may be useful to restore the terminal after a signal.
func GetState(fd int) (*State, error) {
	state, err := term.GetState(fd)
	if err != nil {
		return nil, err
	}

	currentState := State(*state)
	return &currentState, nil
}

// GetSize returns the dimensions of the given terminal.
func GetSize(fd int) (int, int, error) {
	return term.GetSize(fd)
}

// MakeRaw put the terminal connected to the given file descriptor into raw mode and returns the previous state of the terminal so that it can be restored.
func MakeRaw(fd int) (*State, error) {
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}

	oldState := State(*state)
	return &oldState, nil
}

// Restore restores the terminal connected to the given file descriptor to a previous state.
func Restore(fd int, state *State) error {
	newState := term.State(*state)

	return term.Restore(fd, &newState)
}
