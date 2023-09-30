package refcount

import (
	"fmt"
	"sync"
)

var refCounters = map[string]uint{}

// refCounterMutex is used to access refCounters safely.
var refCounterMutex sync.Mutex

// Get returns the current value of the refCounter.
func Get(refCounter string) uint {
	refCounterMutex.Lock()
	defer refCounterMutex.Unlock()

	return refCounters[refCounter]
}

// Increment increases a refCounter by the value. If the ref counter doesn't exist, a new one is created.
// The counter's new value is returned.
func Increment(refCounter string, value uint) uint {
	refCounterMutex.Lock()
	defer refCounterMutex.Unlock()

	v := refCounters[refCounter]
	oldValue := v
	v = v + value

	if v < oldValue {
		panic(fmt.Sprintf("Ref counter %q overflowed from %d to %d", refCounter, oldValue, v))
	}

	refCounters[refCounter] = v

	return v
}

// Decrement decreases a refCounter by the value. If the ref counter doesn't exist, a new one is created.
// The counter's new value is returned. A counter cannot be decreased below zero.
func Decrement(refCounter string, value uint) uint {
	refCounterMutex.Lock()
	defer refCounterMutex.Unlock()

	v := refCounters[refCounter]
	if v < value {
		v = 0
	} else {
		v = v - value
	}

	if v == 0 {
		delete(refCounters, refCounter)
	} else {
		refCounters[refCounter] = v
	}

	return v
}
