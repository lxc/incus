package drivers

import (
	"context"
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

		resourceDefinitions, err := linstor.Client.ResourceDefinitions.GetAll(context.TODO(), linstorClient.RDGetAllRequest{})
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

// getCachedVolumeUsage returns the allocated size of a volume in KiB from the cache.
func (d *linstor) getCachedVolumeUsage(vol Volume) (int64, bool) {
	resourceDefinitionName, ok := d.getCachedResourceDefinitionName(vol)
	if !ok {
		return 0, false
	}

	fill := func(names []string) (map[string]map[string]string, error) {
		linstor, err := d.state.Linstor()
		if err != nil {
			return nil, err
		}

		resources, err := linstor.Client.Resources.GetResourceView(context.TODO(), &linstorClient.ListOpts{
			Resource: names,
		})
		if err != nil {
			return nil, err
		}

		results := map[string]map[string]string{}
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

	cache := getPropertyCache("linstor/usage", 100*time.Millisecond, 15*time.Second)

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
