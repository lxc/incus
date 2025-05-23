package schema

import (
	"errors"
)

// ErrGracefulAbort is a special error that can be returned by a Check function
// to force Schema.Ensure to abort gracefully.
//
// Every change performed so by the Check will be committed, although
// ErrGracefulAbort will be returned.
var ErrGracefulAbort = errors.New("schema check gracefully aborted")
