package operations

import (
	"fmt"
	"reflect"
	"strconv"

	"github.com/lxc/incus/v6/shared/units"
)

func parseMetadata(metadata any) (map[string]any, error) {
	newMetadata := make(map[string]any)
	s := reflect.ValueOf(metadata)
	if !s.IsValid() {
		return nil, nil
	}

	if s.Kind() == reflect.Map {
		for _, k := range s.MapKeys() {
			if k.Kind() != reflect.String {
				return nil, fmt.Errorf("Invalid metadata provided (key isn't a string)")
			}

			newMetadata[k.String()] = s.MapIndex(k).Interface()
		}
	} else if s.Kind() == reflect.Ptr && !s.Elem().IsValid() {
		return nil, nil
	} else {
		return nil, fmt.Errorf("Invalid metadata provided (type isn't a map)")
	}

	return newMetadata, nil
}

// SetProgressMetadata updates an operation metadata map with the provided progress data.
func SetProgressMetadata(metadata map[string]any, stage, displayPrefix string, percent, processed, speed int64) {
	progress := make(map[string]string)
	// stage, percent, speed sent for API callers.
	progress["stage"] = stage
	if processed > 0 {
		progress["processed"] = strconv.FormatInt(processed, 10)
	}

	if percent > 0 {
		progress["percent"] = strconv.FormatInt(percent, 10)
	}

	progress["speed"] = strconv.FormatInt(speed, 10)
	metadata["progress"] = progress

	// <stage>_progress with formatted text.
	if percent > 0 {
		metadata[stage+"_progress"] = fmt.Sprintf("%s: %d%% (%s/s)", displayPrefix, percent, units.GetByteSizeString(speed, 2))
	} else if processed > 0 {
		metadata[stage+"_progress"] = fmt.Sprintf("%s: %s (%s/s)", displayPrefix, units.GetByteSizeString(processed, 2), units.GetByteSizeString(speed, 2))
	} else {
		metadata[stage+"_progress"] = fmt.Sprintf("%s: %s/s", displayPrefix, units.GetByteSizeString(speed, 2))
	}
}
