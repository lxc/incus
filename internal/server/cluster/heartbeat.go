package cluster

import (
	"context"
	"time"

	"github.com/cowsql/go-cowsql/cluster"
	"github.com/cowsql/go-cowsql/cluster/heartbeat"

	"github.com/lxc/incus/v7/internal/server/task"
)

// APIHeartbeat contains data sent to nodes in heartbeat.
type APIHeartbeat = heartbeat.APIHeartbeat

// HeartbeatTask returns a task function that performs leader-initiated heartbeat
// checks against all cluster members in the cluster.
//
// It will update the heartbeat timestamp column of the nodes table
// accordingly, and also notify them of the current list of database nodes.
func HeartbeatTask(gateway cluster.Gateway) (task.Func, task.Schedule) {
	// Since the database APIs are blocking we need to wrap the core logic
	// and run it in a goroutine, so we can abort as soon as the context expires.
	heartbeatWrapper := func(ctx context.Context) {
		if gateway.HeartbeatCancelFunc() == nil {
			ch := make(chan struct{})
			go func() {
				gateway.Heartbeat(ctx, heartbeat.HeartbeatNormal)
				close(ch)
			}()
			select {
			case <-ch:
			case <-ctx.Done():
			}
		}
	}

	schedule := func() (time.Duration, error) {
		return task.Every(gateway.HeartbeatInterval())()
	}

	return heartbeatWrapper, schedule
}
