package task_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/v6/internal/server/task"
)

// The given task is executed immediately by the scheduler.
func TestTask_ExecuteImmediately(t *testing.T) {
	f, wait := newFunc(t, 1)
	defer startTask(t, f, task.Every(time.Second))()
	wait(100 * time.Millisecond)
}

// The given task is executed again after the specified time interval has
// elapsed.
func TestTask_ExecutePeriodically(t *testing.T) {
	f, wait := newFunc(t, 2)
	defer startTask(t, f, task.Every(250*time.Millisecond))()
	wait(100 * time.Millisecond)
	wait(400 * time.Millisecond)
}

// If the scheduler is reset, the task is re-executed immediately and then
// again after the interval.
func TestTask_Reset(t *testing.T) {
	f, wait := newFunc(t, 3)
	stop, reset := task.Start(context.Background(), f, task.Every(250*time.Millisecond))
	defer func() { _ = stop(time.Second) }()

	wait(50 * time.Millisecond)  // First execution, immediately
	reset()                      // Trigger a reset
	wait(50 * time.Millisecond)  // Second execution, immediately after reset
	wait(400 * time.Millisecond) // Third execution, after the timeout
}

// If the interval is zero, the task function is never run.
func TestTask_ZeroInterval(t *testing.T) {
	f, _ := newFunc(t, 0)
	defer startTask(t, f, task.Every(0*time.Millisecond))()

	// Sleep a little bit to prove that the task function does not get run.
	time.Sleep(100 * time.Millisecond)
}

// If the schedule returns an error, the task is aborted.
func TestTask_ScheduleError(t *testing.T) {
	schedule := func() (time.Duration, error) {
		return 0, fmt.Errorf("boom")
	}

	f, _ := newFunc(t, 0)
	defer startTask(t, f, schedule)()

	// Sleep a little bit to prove that the task function does not get run.
	time.Sleep(100 * time.Millisecond)
}

// If the schedule returns an error, but its interval is positive, the task will
// try again to invoke the schedule function after that interval.
func TestTask_ScheduleTemporaryError(t *testing.T) {
	errored := false
	schedule := func() (time.Duration, error) {
		if !errored {
			errored = true
			return time.Millisecond, fmt.Errorf("boom")
		}

		return time.Second, nil
	}

	f, wait := newFunc(t, 1)
	defer startTask(t, f, schedule)()

	// The task gets executed since the schedule error is temporary and gets
	// resolved.
	wait(50 * time.Millisecond)
}

// If SkipFirst is passed, the given task is only executed at the second round.
func TestTask_SkipFirst(t *testing.T) {
	i := 0
	f := func(context.Context) {
		i++
	}

	defer startTask(t, f, task.Every(250*time.Millisecond, task.SkipFirst))()
	time.Sleep(400 * time.Millisecond)
	assert.Equal(t, 1, i) // The function got executed only once, not twice.
}

// Create a new task function that sends a notification to a channel every time
// it's run.
//
// Return the task function, along with a "wait" function which will block
// until one notification is received through such channel, or fails the test
// if no notification is received within the given timeout.
//
// The n parameter can be used to limit the number of times the task function
// is allowed run: when that number is reached the task function will trigger a
// test failure (zero means that the task function will make the test fail as
// soon as it is invoked).
func newFunc(t *testing.T, n int) (task.Func, func(time.Duration)) {
	i := 0
	notifications := make(chan struct{})
	f := func(context.Context) {
		if i == n {
			t.Errorf("task was supposed to be called at most %d times", n)
		}

		notifications <- struct{}{}
		i++
	}

	wait := func(timeout time.Duration) {
		select {
		case <-notifications:
		case <-time.After(timeout):
			t.Errorf("no notification received in %s", timeout)
		}
	}
	return f, wait
}

// Convenience around task.Start which also makes sure that the stop function
// of the task actually terminates.
func startTask(t *testing.T, f task.Func, schedule task.Schedule) func() {
	stop, _ := task.Start(context.Background(), f, schedule)
	return func() {
		assert.NoError(t, stop(time.Second))
	}
}
