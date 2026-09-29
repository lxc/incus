package drivers

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/lxc/incus/v7/shared/subprocess"
)

// zfsCacheProperties lists the dataset properties served from the cache.
var zfsCacheProperties = []string{"used", "referenced"}

// zfsOnlyMissingDatasets returns true when every error line reports a dataset that no longer exists.
func zfsOnlyMissingDatasets(stderr string) bool {
	for line := range strings.SplitSeq(strings.TrimSpace(stderr), "\n") {
		if !strings.HasSuffix(line, ": dataset does not exist") {
			return false
		}
	}

	return true
}

// getCachedProperty returns a dataset property from the cache, prefilling it when needed.
func (d *zfs) getCachedProperty(dataset string, key string) (string, bool) {
	if !slices.Contains(zfsCacheProperties, key) {
		return "", false
	}

	fill := func(datasets []string) (map[string]map[string]string, error) {
		properties := strings.Join(append([]string{"name"}, zfsCacheProperties...), ",")
		args := []string{"list", "-H", "-p", "-o", properties, "-r", "-t", "filesystem,volume,snapshot"}
		args = append(args, datasets...)

		out, err := subprocess.RunCommand("zfs", args...)
		if err != nil {
			// Datasets deleted since being queued are reported on stderr while the others are still listed.
			var runErr subprocess.RunError
			if !errors.As(err, &runErr) || !zfsOnlyMissingDatasets(runErr.StdErr().String()) {
				return nil, err
			}

			out = runErr.StdOut().String()
		}

		return parsePropertyList(out, zfsCacheProperties), nil
	}

	// The cache is filled recursively from the parent dataset, covering its snapshots.
	parentDataset, _, _ := strings.Cut(dataset, "@")

	cache := getPropertyCache("zfs", 100*time.Millisecond, 15*time.Second)
	cache.prefill(parentDataset, fill)

	return cache.lookup(dataset, key)
}
