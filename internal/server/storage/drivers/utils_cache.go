package drivers

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/v7/shared/logger"
)

// propertyCache caches the result of expensive property lookups against a storage backend.
// Concurrent lookups are accumulated for a short delay and then fetched in a single batch,
// limiting the number of backend requests during bulk operations like a full instance list.
type propertyCache struct {
	name   string
	delay  time.Duration
	expiry time.Duration

	mu             sync.Mutex
	records        map[string]propertyCacheRecord
	prefillQueue   []string
	prefillRunning bool
	prefillMu      sync.RWMutex
}

type propertyCacheRecord struct {
	expiry time.Time
	values map[string]string
}

// propertyCacheFiller fetches the properties of the given keys, returning a map of properties for each key.
type propertyCacheFiller func(keys []string) (map[string]map[string]string, error)

var (
	propertyCaches   = map[string]*propertyCache{}
	propertyCachesMu sync.Mutex
)

// getPropertyCache returns the named cache, creating it on first use.
func getPropertyCache(name string, delay time.Duration, expiry time.Duration) *propertyCache {
	propertyCachesMu.Lock()
	defer propertyCachesMu.Unlock()

	cache, ok := propertyCaches[name]
	if !ok {
		cache = &propertyCache{
			name:    name,
			delay:   delay,
			expiry:  expiry,
			records: map[string]propertyCacheRecord{},
		}

		propertyCaches[name] = cache
	}

	return cache
}

// isCached returns whether the key has a valid record, the lock must be held.
func (c *propertyCache) isCached(key string) bool {
	record, ok := c.records[key]
	if !ok {
		return false
	}

	return record.expiry.After(time.Now())
}

// prefill ensures the key is cached, batching concurrent requests into a single filler call.
func (c *propertyCache) prefill(key string, fill propertyCacheFiller) {
	c.mu.Lock()

	if c.isCached(key) {
		c.mu.Unlock()
		return
	}

	if !slices.Contains(c.prefillQueue, key) {
		c.prefillQueue = append(c.prefillQueue, key)
	}

	// Become the filler if none is running.
	var runPrefill bool

	if !c.prefillRunning {
		c.prefillRunning = true
		c.prefillMu.Lock()
		defer func() {
			c.mu.Lock()

			c.prefillMu.Unlock()
			c.prefillRunning = false

			c.mu.Unlock()
		}()

		runPrefill = true
	}

	c.mu.Unlock()

	if !runPrefill {
		// Wait for the running filler to complete, it may or may not have picked us up from the queue.
		c.prefillMu.RLock()
		c.prefillMu.RUnlock() //nolint:staticcheck

		c.mu.Lock()
		inQueue := slices.Contains(c.prefillQueue, key)
		c.mu.Unlock()

		if inQueue {
			c.prefill(key, fill)
		}

		return
	}

	// Allow for requests to accumulate.
	time.Sleep(c.delay)

	// Copy and clear the queue.
	c.mu.Lock()

	queue := []string{}
	for _, entry := range c.prefillQueue {
		if c.isCached(entry) {
			continue
		}

		queue = append(queue, entry)
	}

	c.prefillQueue = []string{}

	c.mu.Unlock()

	if len(queue) == 0 {
		return
	}

	results, err := fill(queue)
	if err != nil {
		logger.Warn("Failed to fill property cache", logger.Ctx{"cache": c.name, "err": err})
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	expiry := time.Now().Add(c.expiry)
	for key, values := range results {
		c.records[key] = propertyCacheRecord{expiry: expiry, values: values}
	}
}

// lookup returns the cached value of a key's property.
func (c *propertyCache) lookup(key string, property string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isCached(key) {
		return "", false
	}

	value, ok := c.records[key].values[property]
	return value, ok
}

// get returns the value of a key's property, prefilling the cache when needed.
func (c *propertyCache) get(key string, property string, fill propertyCacheFiller) (string, bool) {
	c.prefill(key, fill)

	return c.lookup(key, property)
}

// parsePropertyList parses whitespace separated "name value..." lines into cache records.
func parsePropertyList(out string, properties []string) map[string]map[string]string {
	results := map[string]map[string]string{}

	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != len(properties)+1 {
			continue
		}

		values := map[string]string{}
		for i, value := range fields[1:] {
			values[properties[i]] = value
		}

		results[fields[0]] = values
	}

	return results
}
