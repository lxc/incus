package drivers

import (
	"errors"
	"maps"
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
		results := map[string]map[string]string{}

		list := func(args []string, names []string) error {
			args = append([]string{"list", "-H", "-p", "-o", properties}, args...)
			args = append(args, names...)

			out, err := subprocess.RunCommand("zfs", args...)
			if err != nil {
				// Datasets deleted since being queued are reported on stderr while the others are still listed.
				var runErr subprocess.RunError
				if !errors.As(err, &runErr) || !zfsOnlyMissingDatasets(runErr.StdErr().String()) {
					return err
				}

				out = runErr.StdOut().String()
			}

			maps.Copy(results, parsePropertyList(out, zfsCacheProperties))
			return nil
		}

		// Datasets are listed on their own, snapshots are listed for the whole parent dataset at once.
		plain := []string{}
		parents := []string{}
		for _, dataset := range datasets {
			parent, _, isSnapshot := strings.Cut(dataset, "@")
			if !isSnapshot {
				plain = append(plain, dataset)
			} else if !slices.Contains(parents, parent) {
				parents = append(parents, parent)
			}
		}

		if len(plain) > 0 {
			err := list([]string{"-t", "filesystem,volume"}, plain)
			if err != nil {
				return nil, err
			}
		}

		if len(parents) > 0 {
			err := list([]string{"-d", "1", "-t", "snapshot"}, parents)
			if err != nil {
				return nil, err
			}
		}

		return results, nil
	}

	// A snapshot lookup fills all snapshots of its parent, avoiding a listing per snapshot.
	cache := getPropertyCache("zfs", 100*time.Millisecond, 15*time.Second)
	cache.prefill(dataset, fill)

	return cache.lookup(dataset, key)
}
