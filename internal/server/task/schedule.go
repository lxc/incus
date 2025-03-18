package task

import (
	"fmt"
	"time"
)

// Schedule captures the signature of a schedule function.
//
// It should return the amount of time to wait before triggering the next
// execution of a task function.
//
// If it returns zero, the function does not get run at all.
//
// If it returns a duration greater than zero, the task function gets run once
// immediately and then again after the specified amount of time. At that point
// the Task re-invokes the schedule function and repeats the same logic.
//
// If ErrSkip is returned, the immediate execution of the task function gets
// skipped, and it will only be possibly executed after the returned interval.
//
// If any other error is returned, the task won't execute the function, however
// if the returned interval is greater than zero it will re-try to run the
// schedule function after that amount of time.
type Schedule func() (time.Duration, error)

// ErrSkip is a special error that may be returned by a Schedule function to
// mean to skip a particular execution of the task function, and just wait the
// returned interval before re-evaluating.
var ErrSkip = fmt.Errorf("skip execution of task function")

// Every returns a Schedule that always returns the given time interval.
func Every(interval time.Duration, options ...EveryOption) Schedule {
	every := &every{}
	for _, option := range options {
		option(every)
	}

	first := true
	return func() (time.Duration, error) {
		var err error
		if first && every.skipFirst {
			err = ErrSkip
		}

		first = false
		return interval, err
	}
}

// Daily is a convenience for creating a schedule that runs once a day.
func Daily(options ...EveryOption) Schedule {
	return Every(24*time.Hour, options...)
}

// Hourly is a convenience for creating a schedule that runs once an hour.
func Hourly(options ...EveryOption) Schedule {
	return Every(time.Hour, options...)
}

// SkipFirst is an option for the Every schedule that will make the schedule
// skip the very first invocation of the task function.
var SkipFirst = func(every *every) { every.skipFirst = true }

// EveryOption captures a tweak that can be applied to the Every schedule.
type EveryOption func(*every)

// Captures options for the Every schedule.
type every struct {
	skipFirst bool // If true, return ErrSkip at the very first execution
}
