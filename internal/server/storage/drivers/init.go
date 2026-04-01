package drivers

import (
	"sync"
)

func init() {
	zfsCache = map[string]map[string]zfsCacheEntry{}
	zfsCachePrefillQueue = []string{}

	truenasCache = map[string]map[string]map[string]truenasCacheEntry{}
	truenasCachePrefillQueue = map[string][]string{}
	truenasCachePrefillRunning = map[string]bool{}
	truenasCachePrefillMu = map[string]*sync.RWMutex{}
}
