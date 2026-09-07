package drivers

import (
	"sync"
)

func init() {
	truenasCache = map[string]map[string]map[string]truenasCacheEntry{}
	truenasCachePrefillQueue = map[string][]string{}
	truenasCachePrefillRunning = map[string]bool{}
	truenasCachePrefillMu = map[string]*sync.RWMutex{}
}
