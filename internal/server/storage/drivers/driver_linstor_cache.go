package drivers

import (
	"slices"
	"strconv"
	"time"

	linstorClient "github.com/LINBIT/golinstor/client"
)

// getCachedResourceDefinitionName returns the resource definition name of a volume from the cache.
func (d *linstor) getCachedResourceDefinitionName(vol Volume) (string, bool) {
	fill := func(_ []string) (map[string]map[string]string, error) {
		linstor, err := d.state.Linstor()
		if err != nil {
			return nil, err
		}

		ctx, cancel := d.queryContext()
		defer cancel()

		resourceDefinitions, err := linstor.Client.ResourceDefinitions.GetAll(ctx, linstorClient.RDGetAllRequest{})
		if err != nil {
			return nil, err
		}

		results := map[string]map[string]string{}
		duplicates := []string{}
		for _, rd := range resourceDefinitions {
			key := rd.Props[LinstorAuxName] + "|" + rd.Props[LinstorAuxType] + "|" + rd.ResourceGroupName

			_, ok := results[key]
			if ok {
				duplicates = append(duplicates, key)
			}

			results[key] = map[string]string{"name": rd.Name}
		}

		// Leave ambiguous volumes to the direct lookup.
		for _, key := range duplicates {
			delete(results, key)
		}

		return results, nil
	}

	key := d.config[LinstorVolumePrefixConfigKey] + vol.name + "|" + string(vol.volType) + "|" + d.config[LinstorResourceGroupNameConfigKey]

	cache := getPropertyCache("linstor/definitions", 100*time.Millisecond, 15*time.Second)
	return cache.get(key, "name", fill)
}

// getCachedVolumeUsage returns the allocated size in KiB of a volume on the named resource definition from the cache.
func (d *linstor) getCachedVolumeUsage(resourceDefinitionName string, vol Volume) (int64, bool) {
	fill := func(names []string) (map[string]map[string]string, error) {
		linstor, err := d.state.Linstor()
		if err != nil {
			return nil, err
		}

		ctx, cancel := d.queryContext()
		defer cancel()

		// Any resource listing makes the controller poll the satellites, so fetch everything in one go.
		resources, err := linstor.Client.Resources.GetResourceView(ctx, &linstorClient.ListOpts{})
		if err != nil {
			return nil, err
		}

		// Record the requested resources without a diskful replica so they aren't looked up again.
		results := map[string]map[string]string{}
		for _, name := range names {
			results[name] = map[string]string{}
		}

		for _, r := range resources {
			// Only diskful resources carry allocation information.
			if slices.Contains(r.Flags, "DISKLESS") {
				continue
			}

			values := map[string]string{"count": strconv.Itoa(len(r.Volumes))}
			for i, volume := range r.Volumes {
				values[strconv.Itoa(i)] = strconv.FormatInt(volume.AllocatedSizeKib, 10)
			}

			results[r.Name] = values
		}

		return results, nil
	}

	cache := getPropertyCache("linstor/usage", 100*time.Millisecond, 30*time.Second)

	count, ok := cache.get(resourceDefinitionName, "count", fill)
	if !ok {
		return 0, false
	}

	volumeCount, err := strconv.Atoi(count)
	if err != nil {
		return 0, false
	}

	value, ok := cache.lookup(resourceDefinitionName, strconv.Itoa(d.getVolumeIndex(vol, volumeCount)))
	if !ok {
		return 0, false
	}

	usage, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}

	return usage, true
}
