package drivers

func init() {
	zfsCache = map[string]map[string]zfsCacheEntry{}
	zfsCachePrefillQueue = []string{}
}
