package locking

import (
	"context"
	"fmt"
	"sync"
)

// locks is a hashmap that allows functions to check whether the operation they are about to perform
// is already in progress. If it is the channel can be used to wait for the operation to finish. If it is not, the
// function that wants to perform the operation should store its code in the hashmap.
// Note that any access to this map must be done while holding a lock.
var locks = map[string]chan struct{}{}

// locksMutex is used to access locks safely.
var locksMutex sync.Mutex

// UnlockFunc unlocks the lock.
type UnlockFunc func()

// Lock creates a named lock to allow activities that require exclusive access to occur.
// Will block until the lock is established or the context is cancelled.
// On successfully acquiring the lock, it returns an unlock function which needs to be called to unlock the lock.
// If the context is canceled then nil will be returned.
func Lock(ctx context.Context, lockName string) (UnlockFunc, error) {
	for {
		unlock, waitCh := TryLock(lockName)
		if unlock != nil {
			return unlock, nil
		}

		select {
		case <-waitCh:
			continue
		case <-ctx.Done():
			return nil, fmt.Errorf("Failed to obtain lock %q: %w", lockName, ctx.Err())
		}
	}
}

type rwState struct {
	readers        int
	writer         bool
	writersWaiting int
	waitCh         chan struct{}
}

var rwLocks = map[string]*rwState{}
var rwLocksMutex sync.Mutex

func (s *rwState) broadcast() {
	close(s.waitCh)
	s.waitCh = make(chan struct{})
}

func getRWLock(lockName string) *rwState {
	state, ok := rwLocks[lockName]
	if !ok {
		state = &rwState{waitCh: make(chan struct{})}
		rwLocks[lockName] = state
	}

	return state
}

func releaseRWLock(lockName string, state *rwState) {
	current, ok := rwLocks[lockName]
	if ok && current == state && state.readers == 0 && !state.writer && state.writersWaiting == 0 {
		delete(rwLocks, lockName)
	}
}

func rwUnlock(lockName string, state *rwState, release func(*rwState)) UnlockFunc {
	var once sync.Once

	return func() {
		once.Do(func() {
			rwLocksMutex.Lock()
			release(state)
			state.broadcast()
			releaseRWLock(lockName, state)
			rwLocksMutex.Unlock()
		})
	}
}

// RWLock acquires an exclusive named read-write lock.
// Writers are prioritized over new readers so a continuous stream of readers cannot starve a writer.
func RWLock(ctx context.Context, lockName string) (UnlockFunc, error) {
	rwLocksMutex.Lock()

	for {
		state := getRWLock(lockName)
		if state.readers == 0 && !state.writer {
			state.writer = true
			rwLocksMutex.Unlock()

			return rwUnlock(lockName, state, func(state *rwState) { state.writer = false }), nil
		}

		state.writersWaiting++
		waitCh := state.waitCh
		rwLocksMutex.Unlock()

		select {
		case <-waitCh:
		case <-ctx.Done():
			rwLocksMutex.Lock()
			state.writersWaiting--
			state.broadcast()
			releaseRWLock(lockName, state)
			rwLocksMutex.Unlock()

			return nil, fmt.Errorf("Failed to obtain lock %q: %w", lockName, ctx.Err())
		}

		rwLocksMutex.Lock()
		state.writersWaiting--
	}
}

// RLock acquires a shared named read-write lock.
func RLock(ctx context.Context, lockName string) (UnlockFunc, error) {
	rwLocksMutex.Lock()

	for {
		state := getRWLock(lockName)
		if !state.writer && state.writersWaiting == 0 {
			state.readers++
			rwLocksMutex.Unlock()

			return rwUnlock(lockName, state, func(state *rwState) { state.readers-- }), nil
		}

		waitCh := state.waitCh
		rwLocksMutex.Unlock()

		select {
		case <-waitCh:
		case <-ctx.Done():
			return nil, fmt.Errorf("Failed to obtain lock %q: %w", lockName, ctx.Err())
		}

		rwLocksMutex.Lock()
	}
}

// TryLock creates a named lock for activities that require exclusive access.
// It does not block if the lock is already held.
// If the lock is acquired successfully, it returns an unlock function that
// must be called to release the lock.
func TryLock(lockName string) (UnlockFunc, chan struct{}) {
	// Get exclusive access to the map and see if there is already an operation ongoing.
	locksMutex.Lock()
	waitCh, ok := locks[lockName]

	if !ok {
		// No ongoing operation, create a new channel to indicate our new operation.
		waitCh = make(chan struct{})
		locks[lockName] = waitCh
		locksMutex.Unlock()

		// Return a function that will complete the operation.
		return func() {
			// Get exclusive access to the map.
			locksMutex.Lock()
			doneCh, ok := locks[lockName]

			// Load our existing operation, skipping release if the entry
			// now belongs to another operation (repeated unlock call).
			if ok && doneCh == waitCh {
				// Close the channel to indicate to other waiting users
				// they can now try again to create a new operation.
				close(doneCh)

				// Remove our existing operation entry from the map.
				delete(locks, lockName)
			}

			// Release the lock now that the done channel is closed and the
			// map entry has been deleted, this will allow any waiting users
			// to try and get access to the map to create a new operation.
			locksMutex.Unlock()
		}, waitCh
	}

	// An existing operation is ongoing, lets wait for that to finish and then try
	// to get exclusive access to create a new operation again.
	locksMutex.Unlock()

	return nil, waitCh
}
