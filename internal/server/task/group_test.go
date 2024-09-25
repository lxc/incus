package task_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/v6/internal/server/task"
)

func TestGroup_Add(t *testing.T) {
	group := &task.Group{}
	ok := make(chan struct{})
	f := func(context.Context) { close(ok) }
	group.Add(f, task.Every(time.Second))
	group.Start(context.Background())

	assertRecv(t, ok)

	assert.NoError(t, group.Stop(time.Second))
}

func TestGroup_StopUngracefully(t *testing.T) {
	group := &task.Group{}

	// Create a task function that blocks.
	ok := make(chan struct{})
	defer close(ok)
	f := func(context.Context) {
		ok <- struct{}{}
		<-ok
	}

	group.Add(f, task.Every(time.Second))
	group.Start(context.Background())

	assertRecv(t, ok)

	assert.EqualError(t, group.Stop(time.Millisecond), "Task(s) still running: IDs [0]")
}

// Assert that the given channel receives an object within a second.
func assertRecv(t *testing.T, ch chan struct{}) {
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no object received")
	}
}
