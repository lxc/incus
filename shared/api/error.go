package api

import (
	"errors"
	"fmt"
	"net/http"
)

// StatusErrorf returns a new StatusError containing the specified status and message.
func StatusErrorf(status int, format string, a ...any) StatusError {
	var msg string
	if len(a) > 0 {
		msg = fmt.Sprintf(format, a...)
	} else {
		msg = format
	}

	return StatusError{
		status: status,
		msg:    msg,
	}
}

// StatusError error type that contains an HTTP status code and message.
type StatusError struct {
	status int
	msg    string
}

// Error returns the error message or the http.StatusText() of the status code if message is empty.
func (e StatusError) Error() string {
	if e.msg != "" {
		return e.msg
	}

	return http.StatusText(e.status)
}

// Status returns the HTTP status code.
func (e StatusError) Status() int {
	return e.status
}

// StatusErrorMatch checks if err was caused by StatusError. Can optionally also check whether the StatusError's
// status code matches one of the supplied status codes in matchStatus.
// Returns the matched StatusError status code and true if match criteria are met, otherwise false.
func StatusErrorMatch(err error, matchStatusCodes ...int) (int, bool) {
	var statusErr StatusError

	if errors.As(err, &statusErr) {
		statusCode := statusErr.Status()

		if len(matchStatusCodes) <= 0 {
			return statusCode, true
		}

		for _, s := range matchStatusCodes {
			if statusCode == s {
				return statusCode, true
			}
		}
	}

	return -1, false
}

// StatusErrorCheck returns whether or not err was caused by a StatusError and if it matches one of the
// optional status codes.
func StatusErrorCheck(err error, matchStatusCodes ...int) bool {
	_, found := StatusErrorMatch(err, matchStatusCodes...)
	return found
}
