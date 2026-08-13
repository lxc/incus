package locking

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRWLockAllowsConcurrentReaders(t *testing.T) {
	unlockFirst, err := RLock(t.Context(), t.Name())
	require.NoError(t, err)
	defer unlockFirst()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	unlockSecond, err := RLock(ctx, t.Name())
	require.NoError(t, err)
	unlockSecond()
}

func TestRWLockExcludesReadersAndHonorsCancellation(t *testing.T) {
	unlockWriter, err := RWLock(t.Context(), t.Name())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	unlockReader, err := RLock(ctx, t.Name())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, unlockReader)

	unlockWriter()
	unlockWriter() // Unlock functions are idempotent.

	unlockReader, err = RLock(t.Context(), t.Name())
	require.NoError(t, err)
	unlockReader()
}

func TestRWLockPrioritizesWaitingWriter(t *testing.T) {
	lockName := t.Name()
	unlockFirstReader, err := RLock(t.Context(), lockName)
	require.NoError(t, err)

	writerAcquired := make(chan UnlockFunc, 1)
	go func() {
		unlock, err := RWLock(t.Context(), lockName)
		if err == nil {
			writerAcquired <- unlock
		}
	}()

	require.Eventually(t, func() bool {
		rwLocksMutex.Lock()
		defer rwLocksMutex.Unlock()

		return rwLocks[lockName] != nil && rwLocks[lockName].writersWaiting == 1
	}, time.Second, time.Millisecond)

	readerAcquired := make(chan UnlockFunc, 1)
	go func() {
		unlock, err := RLock(t.Context(), lockName)
		if err == nil {
			readerAcquired <- unlock
		}
	}()

	unlockFirstReader()

	var unlockWriter UnlockFunc
	select {
	case unlockWriter = <-writerAcquired:
	case <-readerAcquired:
		t.Fatal("Reader acquired the lock ahead of a waiting writer")
	case <-time.After(time.Second):
		t.Fatal("Writer did not acquire the lock")
	}

	unlockWriter()

	select {
	case unlockReader := <-readerAcquired:
		unlockReader()
	case <-time.After(time.Second):
		t.Fatal("Reader did not acquire the lock after the writer released it")
	}
}
