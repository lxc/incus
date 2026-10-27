package main

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lxc/incus/v7/shared/api"
)

func controlChannelGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]

	return strings.Count(string(buf), "(*migrationFields).controlChannel.func1")
}

// TestMigrationControlChannelNoLeak checks the goroutine exits when nothing reads the channel.
func TestMigrationControlChannelNoLeak(t *testing.T) {
	before := controlChannelGoroutines()

	for range 10 {
		conn := newMigrationConn("secret", nil, nil)
		conn.disconnected = true

		c := &migrationFields{conns: map[string]*migrationConn{api.SecretNameControl: conn}}
		_ = c.controlChannel()
	}

	deadline := time.Now().Add(5 * time.Second)
	for controlChannelGoroutines() > before {
		if time.Now().After(deadline) {
			t.Fatalf("%d controlChannel goroutines still running, want %d", controlChannelGoroutines(), before)
		}

		time.Sleep(10 * time.Millisecond)
	}
}
