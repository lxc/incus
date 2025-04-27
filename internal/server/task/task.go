package task

import (
	"context"
	"errors"
	"time"
)

// Task executes a certain function periodically, according to a certain
// schedule.
type Task struct {
	f        Func          // Function to execute.
	schedule Schedule      // Decides if and when to execute f.
	reset    chan struct{} // Resets the schedule and starts over.
}

// Reset the state of the task as if it had just been started.
//
// This is handy if the schedule logic has changed, since the schedule function
// will be invoked immediately to determine whether and when to run the task
// function again.
func (t *Task) Reset() {
	t.reset <- struct{}{}
}

// Execute the our task function according to our schedule, until the given
// context gets cancelled.
func (t *Task) loop(ctx context.Context) {
	// Kick off the task immediately (as long as the schedule is
	// greater than zero, see below).
	delay := immediately

	for {
		var timer <-chan time.Time

		schedule, err := t.schedule()
		switch {
		case errors.Is(err, ErrSkip):
			// Reset the delay to be exactly the schedule, so we
			// rule out the case where it's set to immediately
			// because it's the first iteration or we got reset.
			delay = schedule
			fallthrough // Fall to case nil, to apply normal non-error logic
		case err == nil:
			// If the schedule is greater than zero, setup a timer
			// that will expire after 'delay' seconds (or after the
			// schedule in case of ErrSkip, to avoid triggering
			// immediately), otherwise setup a timer that will
			// never expire (hence the task function won't ever be
			// run, unless Reset() is called and schedule() starts
			// returning values greater than zero).
			if schedule > 0 {
				timer = time.After(delay)
			} else {
				timer = make(chan time.Time)
			}

		default:
			// If the schedule is not greater than zero, abort the
			// task and return immediately. Otherwise set up the
			// timer to retry after that amount of time.
			if schedule <= 0 {
				return
			}

			timer = time.After(schedule)
		}

		select {
		case <-timer:
			if err == nil {
				// Execute the task function synchronously. Consumers
				// are responsible for implementing proper cancellation
				// of the task function itself using the tomb's context.
				start := time.Now()
				t.f(ctx)
				duration := time.Since(start)

				delay = schedule - duration
				if delay < 0 {
					delay = immediately
				}

			} else {
				// Don't execute the task function, and set the
				// delay to run it immediately whenever the
				// schedule function returns a nil error.
				delay = immediately
			}

		case <-ctx.Done():
			return

		case <-t.reset:
			delay = immediately
		}
	}
}

const immediately = 0 * time.Second
