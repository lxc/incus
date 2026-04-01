package drivers

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/subprocess"
)

// This is a global ZFS cache for the system, used to limit the number
// of expensive requests to ZFS especially during bulk requests like a full
// instance list.
type zfsCacheEntry struct {
	Expiry time.Time
	Value  string
}

var (
	zfsCache               map[string]map[string]zfsCacheEntry
	zfsCacheMu             sync.Mutex
	zfsCachePrefillQueue   []string
	zfsCachePrefillRunning bool
	zfsCachePrefillMu      sync.RWMutex
	zfsCacheProperties     = []string{"used", "referenced"}
)

func (d *zfs) prefillCachedProperties(dataset string) {
	// Define a function to quickly check if a dataset is already cached.
	isCached := func(dataset string) bool {
		record, ok := zfsCache[dataset]
		if !ok {
			return false
		}

		now := time.Now()
		for _, propName := range zfsCacheProperties {
			prop, ok := record[propName]
			if !ok || prop.Expiry.Before(now) {
				return false
			}
		}

		return true
	}

	// Get the lock.
	zfsCacheMu.Lock()

	// Check if we already have a valid cache for the dataset.
	if isCached(dataset) {
		zfsCacheMu.Unlock()
		return
	}

	// Add the request to the queue.
	if !slices.Contains(zfsCachePrefillQueue, dataset) {
		zfsCachePrefillQueue = append(zfsCachePrefillQueue, dataset)
	}

	// Check if a filler is already running.
	// If not, make a copy of the queue and reset it.
	var runPrefill bool

	if !zfsCachePrefillRunning {
		zfsCachePrefillRunning = true
		zfsCachePrefillMu.Lock()
		defer func() {
			zfsCacheMu.Lock()

			zfsCachePrefillMu.Unlock()
			zfsCachePrefillRunning = false

			zfsCacheMu.Unlock()
		}()

		runPrefill = true
	}

	// Release the lock.
	zfsCacheMu.Unlock()

	// Check if we're done.
	if !runPrefill {
		// Wait for current run.
		zfsCachePrefillMu.RLock()
		zfsCachePrefillMu.RUnlock() //nolint:staticcheck

		// Check that we made it.
		zfsCacheMu.Lock()
		inQueue := slices.Contains(zfsCachePrefillQueue, dataset)
		zfsCacheMu.Unlock()

		if inQueue {
			// We didn't make it, re-trigger.
			d.prefillCachedProperties(dataset)
			return
		}
	}

	// Allow for requests to accumulate.
	time.Sleep(100 * time.Millisecond)

	// Copy and clear the queue.
	zfsCacheMu.Lock()

	queue := []string{}
	for _, entry := range zfsCachePrefillQueue {
		if isCached(entry) {
			continue
		}

		queue = append(queue, entry)
	}

	zfsCachePrefillQueue = []string{}

	zfsCacheMu.Unlock()

	// Check that we have something to do.
	if len(queue) == 0 {
		return
	}

	// Run the filler.
	properties := strings.Join(append([]string{"name"}, zfsCacheProperties...), ",")
	args := []string{"list", "-H", "-p", "-o", properties, "-r", "-t", "filesystem,volume,snapshot"}
	args = append(args, queue...)

	out, err := subprocess.RunCommand("zfs", args...)
	if err != nil {
		d.logger.Warn("Couldn't cache ZFS properties", logger.Ctx{"err": err})

		return
	}

	// Update the cache.
	zfsCacheMu.Lock()
	defer zfsCacheMu.Unlock()

	expiry := time.Now().Add(15 * time.Second)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}

		record, ok := zfsCache[fields[0]]
		if !ok {
			record = map[string]zfsCacheEntry{}
		}

		for i, value := range fields[1:] {
			key := zfsCacheProperties[i]
			record[key] = zfsCacheEntry{Expiry: expiry, Value: value}
		}

		zfsCache[fields[0]] = record
	}
}

func (d *zfs) getCachedProperty(dataset string, key string) (string, bool) {
	// Check if this is a cached property.
	if !slices.Contains(zfsCacheProperties, key) {
		return "", false
	}

	// Update cache if needed.
	parentDataset := strings.Split(dataset, "@")[0]
	d.prefillCachedProperties(parentDataset)

	// Get the value.
	zfsCacheMu.Lock()
	defer zfsCacheMu.Unlock()

	record, ok := zfsCache[dataset]
	if !ok {
		return "", false
	}

	value, ok := record[key]
	if !ok {
		return "", false
	}

	if value.Expiry.Before(time.Now()) {
		return "", false
	}

	return value.Value, true
}
