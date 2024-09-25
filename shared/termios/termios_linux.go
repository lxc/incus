//go:build linux

package termios

import (
	"golang.org/x/sys/unix"
)

const ioctlReadTermios = unix.TCGETS
const ioctlWriteTermios = unix.TCSETS

// State contains the state of a terminal.
type State struct {
	Termios unix.Termios
}

// IsTerminal returns true if the given file descriptor is a terminal.
func IsTerminal(fd int) bool {
	_, err := GetState(fd)
	return err == nil
}

// GetState returns the current state of a terminal which may be useful to restore the terminal after a signal.
func GetState(fd int) (*State, error) {
	termios, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return nil, err
	}

	state := State{}
	state.Termios = *termios

	return &state, nil
}

// GetSize returns the dimensions of the given terminal.
func GetSize(fd int) (int, int, error) {
	winsize, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return -1, -1, err
	}

	return int(winsize.Col), int(winsize.Row), nil
}

// MakeRaw put the terminal connected to the given file descriptor into raw mode and returns the previous state of the terminal so that it can be restored.
func MakeRaw(fd int) (*State, error) {
	oldState, err := GetState(fd)
	if err != nil {
		return nil, err
	}

	newState := *oldState

	// This attempts to replicate the behaviour documented for cfmakeraw in the termios(3) manpage.
	newState.Termios.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	newState.Termios.Oflag &^= unix.OPOST
	newState.Termios.Cflag &^= unix.CSIZE | unix.PARENB
	newState.Termios.Cflag |= unix.CS8
	newState.Termios.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	newState.Termios.Cc[unix.VMIN] = 1
	newState.Termios.Cc[unix.VTIME] = 0

	err = Restore(fd, &newState)
	if err != nil {
		return nil, err
	}

	return oldState, nil
}

// Restore restores the terminal connected to the given file descriptor to a previous state.
func Restore(fd int, state *State) error {
	err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &state.Termios)
	if err != nil {
		return err
	}

	return nil
}
