package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestInstanceLimiterTurns(t *testing.T) {
	limiter := newInstanceLimiter(1)
	limiter.acquire("big")

	granted := make(chan string)
	queue := func(project string, count int) {
		for range count {
			go func() {
				limiter.acquire(project)
				granted <- project
			}()
		}

		assert.Eventually(t, func() bool {
			limiter.mu.Lock()
			defer limiter.mu.Unlock()

			return len(limiter.waiters[project]) == count
		}, 5*time.Second, time.Millisecond)
	}

	queue("big", 3)
	queue("small", 2)

	// Projects alternate no matter how many actions they have queued.
	for _, project := range []string{"big", "small", "big", "small", "big"} {
		limiter.release()
		assert.Equal(t, project, <-granted)
	}

	// With nothing waiting, the slot is free again.
	limiter.release()
	limiter.acquire("other")
}
