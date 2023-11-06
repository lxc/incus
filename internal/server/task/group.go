package task

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Group of tasks sharing the same lifecycle.
//
// All tasks in a group will be started and stopped at the same time.
type Group struct {
	cancel  func()
	wg      sync.WaitGroup
	tasks   []Task
	running map[int]bool
	mu      sync.Mutex
}

// Add a new task to the group, returning its index.
func (g *Group) Add(f Func, schedule Schedule) *Task {
	g.mu.Lock()
	defer g.mu.Unlock()

	i := len(g.tasks)
	g.tasks = append(g.tasks, Task{
		f:        f,
		schedule: schedule,
		reset:    make(chan struct{}, 16), // Buffered to not block senders
	})

	return &g.tasks[i]
}

// Start all the tasks in the group.
func (g *Group) Start(ctx context.Context) {
	// Lock access to the g.running and g.tasks map for the entirety of this function so that
	// concurrent calls to Start() or Add(0) don't race. This ensures all tasks in this group
	// are started based on a consistent snapshot of g.running and g.tasks.
	g.mu.Lock()
	defer g.mu.Unlock()

	ctx, g.cancel = context.WithCancel(ctx)
	g.wg.Add(len(g.tasks))

	if g.running == nil {
		g.running = make(map[int]bool)
	}

	for i := range g.tasks {
		if g.running[i] {
			continue
		}

		g.running[i] = true
		task := g.tasks[i] // Local variable for the closure below.

		go func(i int) {
			task.loop(ctx)

			// Ensure running map is updated before wait group Done() is called.
			g.mu.Lock()
			defer g.mu.Unlock()

			if g.running != nil {
				g.running[i] = false
				g.wg.Done()
			}
		}(i)
	}
}

// Stop all tasks in the group.
//
// This works by sending a cancellation signal to all tasks of the
// group and waiting for them to terminate.
//
// If a task is idle (i.e. not executing its task function) it will terminate
// immediately.
//
// If a task is busy executing its task function, the cancellation signal will
// propagate through the context passed to it, and the task will block waiting
// for the function to terminate.
//
// In case the given timeout expires before all tasks complete, this method
// exits immediately and returns an error, otherwise it returns nil.
func (g *Group) Stop(timeout time.Duration) error {
	if g.cancel == nil {
		// We were not even started
		return nil
	}

	g.cancel()

	graceful := make(chan struct{}, 1)
	go func() {
		g.wg.Wait()
		close(graceful)
	}()

	// Wait for graceful termination, but abort if the context expires.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case <-ctx.Done():
		g.mu.Lock()
		defer g.mu.Unlock()

		running := []string{}
		for i, value := range g.running {
			if value {
				running = append(running, strconv.Itoa(i))
			}
		}

		return fmt.Errorf("Task(s) still running: IDs %v", running)
	case <-graceful:
		return nil
	}
}
