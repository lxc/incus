package schema

import (
	"fmt"
)

// ErrGracefulAbort is a special error that can be returned by a Check function
// to force Schema.Ensure to abort gracefully.
//
// Every change performed so by the Check will be committed, although
// ErrGracefulAbort will be returned.
var ErrGracefulAbort = fmt.Errorf("schema check gracefully aborted")
