package drivers

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/v6/shared/logger"
)

// This is a per-pool TrueNAS cache, used to limit the number
// of expensive requests to TrueNAS especially during bulk requests like a full
// instance list.
type truenasCacheEntry struct {
	Expiry time.Time
	Value  string
}

var (
	truenasCache               map[string]map[string]map[string]truenasCacheEntry
	truenasCacheMu             sync.Mutex
	truenasCachePrefillQueue   map[string][]string
	truenasCachePrefillRunning map[string]bool
	truenasCachePrefillMu      map[string]*sync.RWMutex
	truenasCacheProperties     = []string{"used", "referenced"}
)

// truenasCacheEnsurePool initializes the per-pool cache maps if needed.
func truenasCacheEnsurePool(pool string) {
	_, ok := truenasCache[pool]
	if !ok {
		truenasCache[pool] = map[string]map[string]truenasCacheEntry{}
		truenasCachePrefillQueue[pool] = []string{}
		truenasCachePrefillMu[pool] = &sync.RWMutex{}
	}
}

func (d *truenas) prefillCachedProperties(dataset string) {
	// Define a function to quickly check if a dataset is already cached.
	isCached := func(dataset string) bool {
		record, ok := truenasCache[d.name][dataset]
		if !ok {
			return false
		}

		now := time.Now()
		for _, propName := range truenasCacheProperties {
			prop, ok := record[propName]
			if !ok || prop.Expiry.Before(now) {
				return false
			}
		}

		return true
	}

	// Get the lock.
	truenasCacheMu.Lock()

	// Ensure the pool exists in the cache.
	truenasCacheEnsurePool(d.name)

	// Check if we already have a valid cache for the dataset.
	if isCached(dataset) {
		truenasCacheMu.Unlock()
		return
	}

	// Add the request to the queue.
	if !slices.Contains(truenasCachePrefillQueue[d.name], dataset) {
		truenasCachePrefillQueue[d.name] = append(truenasCachePrefillQueue[d.name], dataset)
	}

	// Check if a filler is already running.
	// If not, make a copy of the queue and reset it.
	var runPrefill bool

	if !truenasCachePrefillRunning[d.name] {
		truenasCachePrefillRunning[d.name] = true
		truenasCachePrefillMu[d.name].Lock()
		defer func() {
			truenasCacheMu.Lock()

			truenasCachePrefillMu[d.name].Unlock()
			truenasCachePrefillRunning[d.name] = false

			truenasCacheMu.Unlock()
		}()

		runPrefill = true
	}

	// Release the lock.
	truenasCacheMu.Unlock()

	// Check if we're done.
	if !runPrefill {
		// Wait for current run.
		truenasCachePrefillMu[d.name].RLock()
		defer truenasCachePrefillMu[d.name].RUnlock()

		// Check that we made it.
		truenasCacheMu.Lock()
		inQueue := slices.Contains(truenasCachePrefillQueue[d.name], dataset)
		truenasCacheMu.Unlock()

		if inQueue {
			// We didn't make it, re-trigger.
			d.prefillCachedProperties(dataset)
			return
		}
	}

	// Allow for requests to accumulate.
	time.Sleep(200 * time.Millisecond)

	// Copy and clear the queue.
	truenasCacheMu.Lock()

	queue := []string{}
	for _, entry := range truenasCachePrefillQueue[d.name] {
		if isCached(entry) {
			continue
		}

		queue = append(queue, entry)
	}

	truenasCachePrefillQueue[d.name] = []string{}

	truenasCacheMu.Unlock()

	// Check that we have something to do.
	if len(queue) == 0 {
		return
	}

	// Run the filler in batches of 2 datasets (TrueNAS limitation).
	properties := strings.Join(append([]string{"name"}, truenasCacheProperties...), ",")

	for i := 0; i < len(queue); i += 2 {
		batch := queue[i:]
		if len(batch) > 2 {
			batch = batch[:2]
		}

		args := []string{"list", "--no-headers", "--parsable", "-o", properties, "-r", "-t", "filesystem,volume,snapshot"}
		args = append(args, batch...)

		out, err := d.runTool(args...)
		if err != nil {
			d.logger.Warn("Couldn't cache TrueNAS properties", logger.Ctx{"err": err})

			continue
		}

		// Update the cache.
		truenasCacheMu.Lock()

		expiry := time.Now().Add(time.Minute)
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			fields := strings.Fields(line)
			if len(fields) != 3 {
				continue
			}

			record, ok := truenasCache[d.name][fields[0]]
			if !ok {
				record = map[string]truenasCacheEntry{}
			}

			for i, value := range fields[1:] {
				key := truenasCacheProperties[i]
				record[key] = truenasCacheEntry{Expiry: expiry, Value: value}
			}

			truenasCache[d.name][fields[0]] = record
		}

		truenasCacheMu.Unlock()
	}
}

func (d *truenas) getCachedProperty(dataset string, key string) (string, bool) {
	// Check if this is a cached property.
	if !slices.Contains(truenasCacheProperties, key) {
		return "", false
	}

	// Update cache if needed.
	parentDataset := strings.Split(dataset, "@")[0]
	d.prefillCachedProperties(parentDataset)

	// Get the value.
	truenasCacheMu.Lock()
	defer truenasCacheMu.Unlock()

	record, ok := truenasCache[d.name][dataset]
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
