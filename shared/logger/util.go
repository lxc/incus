package logger

import (
	"errors"
)

// WarnOnError calls the provided function and, if it returns an error, logs
// that error as a warning together with the provided message and optional
// context.
func WarnOnError(f func() error, msg string, ctx ...Ctx) {
	WarnOnErrorExcept(f, nil, msg, ctx...)
}

// WarnOnErrorExcept behaves like WarnOnError but doesn't warn for any error
// matching (via errors.Is) one of the provided ignored errors.
func WarnOnErrorExcept(f func() error, ignore []error, msg string, ctx ...Ctx) {
	err := f()
	if err == nil {
		return
	}

	for _, target := range ignore {
		if errors.Is(err, target) {
			return
		}
	}

	ctx = append(ctx, Ctx{"err": err})
	Warn(msg, ctx...)
}
