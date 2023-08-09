package task

import (
	"context"
	"time"
)

// Start a single task executing the given function with the given schedule.
//
// This is a convenience around Group and it returns two functions that can be
// used to control the task. The first is a "stop" function trying to terminate
// the task gracefully within the given timeout and the second is a "reset"
// function to reset the task's state. See Group.Stop() and Group.Reset() for
// more details.
func Start(ctx context.Context, f Func, schedule Schedule) (func(time.Duration) error, func()) {
	group := Group{}
	task := group.Add(f, schedule)
	group.Start(ctx)

	stop := group.Stop
	reset := task.Reset

	return stop, reset
}
