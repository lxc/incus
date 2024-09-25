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

				// Load our existing operation.
				if ok {
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
			}, nil
		}

		// An existing operation is ongoing, lets wait for that to finish and then try
		// to get exlusive access to create a new operation again.
		locksMutex.Unlock()

		select {
		case <-waitCh:
			continue
		case <-ctx.Done():
			return nil, fmt.Errorf("Failed to obtain lock %q: %w", lockName, ctx.Err())
		}
	}
}
