package drivers

import (
	"slices"
	"sync"
	"testing"
	"time"
)

func TestPropertyCache(t *testing.T) {
	cache := getPropertyCache("test", 50*time.Millisecond, 200*time.Millisecond)

	var mu sync.Mutex
	calls := [][]string{}

	fill := func(keys []string) (map[string]map[string]string, error) {
		mu.Lock()
		defer mu.Unlock()

		calls = append(calls, slices.Clone(keys))

		results := map[string]map[string]string{}
		for _, key := range keys {
			results[key] = map[string]string{"value": key + "-value"}
		}

		return results, nil
	}

	// Concurrent lookups are served by a single filler call.
	keys := []string{"a", "b", "c", "d", "e"}

	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Go(func() {
			value, ok := cache.get(key, "value", fill)
			if !ok || value != key+"-value" {
				t.Errorf("Unexpected value for %q: %q (%v)", key, value, ok)
			}
		})
	}

	wg.Wait()

	if len(calls) != 1 {
		t.Fatalf("Expected a single filler call, got %d", len(calls))
	}

	slices.Sort(calls[0])
	if !slices.Equal(calls[0], keys) {
		t.Fatalf("Unexpected filler keys: %v", calls[0])
	}

	// Cached entries don't trigger the filler.
	_, ok := cache.get("a", "value", fill)
	if !ok || len(calls) != 1 {
		t.Fatalf("Expected cache hit, got %d filler calls", len(calls))
	}

	// Unknown properties and keys are misses.
	_, ok = cache.lookup("a", "missing")
	if ok {
		t.Fatal("Unexpected hit on missing property")
	}

	_, ok = cache.lookup("missing", "value")
	if ok {
		t.Fatal("Unexpected hit on missing key")
	}

	// Expired entries are fetched again.
	time.Sleep(250 * time.Millisecond)

	_, ok = cache.get("a", "value", fill)
	if !ok || len(calls) != 2 {
		t.Fatalf("Expected refill after expiry, got %d filler calls", len(calls))
	}
}
