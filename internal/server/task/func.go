package task

import (
	"context"
)

// Func captures the signature of a function executable by a Task.
//
// When the given context is done, the function must gracefully terminate
// whatever logic it's executing.
type Func func(context.Context)
