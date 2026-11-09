package drivers

import (
	"context"
	"sync"
	"time"

	"github.com/lxc/incus/v7/internal/server/instance"
	storagePools "github.com/lxc/incus/v7/internal/server/storage"
	storageDrivers "github.com/lxc/incus/v7/internal/server/storage/drivers"
	"github.com/lxc/incus/v7/shared/logger"
)

// storageLock tracks a pending switch of an instance's storage locks to exclusive.
type storageLock struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// storageLocks tracks the pending storage lock switches by instance ID.
var (
	storageLocksMu sync.Mutex
	storageLocks   = map[int]*storageLock{}
)

// stopStorageLock stops any pending switch of the instance's storage locks and waits for it to finish.
func (d *common) stopStorageLock() {
	storageLocksMu.Lock()
	lock := storageLocks[d.id]
	delete(storageLocks, d.id)
	storageLocksMu.Unlock()

	if lock != nil {
		lock.cancel()
		<-lock.done
	}
}

// lockStorageExclusive switches the instance's storage locks back to exclusive once no other member uses them.
func (d *common) lockStorageExclusive(inst instance.Instance, pool storagePools.Pool) {
	d.stopStorageLock()

	ctx, cancel := context.WithCancel(context.Background())
	lock := &storageLock{cancel: cancel, done: make(chan struct{})}

	storageLocksMu.Lock()
	storageLocks[d.id] = lock
	storageLocksMu.Unlock()

	go func() {
		defer close(lock.done)
		defer func() {
			storageLocksMu.Lock()
			if storageLocks[d.id] == lock {
				delete(storageLocks, d.id)
			}

			storageLocksMu.Unlock()
			cancel()
		}()

		var err error

		for range 30 {
			if ctx.Err() != nil || !inst.IsRunning() {
				return
			}

			err = pool.SetInstanceAccessMode(inst, storageDrivers.VolumeAccessModeExclusive, nil)
			if err == nil {
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
		}

		d.logger.Warn("Failed switching storage locks to exclusive", logger.Ctx{"err": err})
	}()
}
