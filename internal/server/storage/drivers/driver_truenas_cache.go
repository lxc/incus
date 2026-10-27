package drivers

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/lxc/incus/v7/shared/logger"
)

// truenasCacheProperties lists the dataset properties served from the cache.
var truenasCacheProperties = []string{"used", "referenced"}

// getCachedProperty returns a dataset property from the per-pool cache, prefilling it when needed.
func (d *truenas) getCachedProperty(dataset string, key string) (string, bool) {
	if !slices.Contains(truenasCacheProperties, key) {
		return "", false
	}

	fill := func(datasets []string) (map[string]map[string]string, error) {
		properties := strings.Join(append([]string{"name"}, truenasCacheProperties...), ",")
		results := map[string]map[string]string{}

		// Query in batches of 2 datasets (TrueNAS limitation).
		for batch := range slices.Chunk(datasets, 2) {
			args := []string{"list", "--no-headers", "--parsable", "-o", properties, "-r", "-t", "filesystem,volume,snapshot"}
			args = append(args, batch...)

			out, err := d.runTool(args...)
			if err != nil {
				d.logger.Warn("Couldn't cache TrueNAS properties", logger.Ctx{"err": err})
				continue
			}

			maps.Copy(results, parsePropertyList(out, truenasCacheProperties))
		}

		return results, nil
	}

	// The cache is filled recursively from the parent dataset, covering its snapshots.
	parentDataset, _, _ := strings.Cut(dataset, "@")

	cache := getPropertyCache("truenas/"+d.name, 200*time.Millisecond, time.Minute)
	cache.prefill(parentDataset, fill)

	return cache.lookup(dataset, key)
}
