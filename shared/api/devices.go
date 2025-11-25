package api

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// DevicesMap type is used to hold incus devices configurations. In contrast to
// plain map[string]map[string]string it provides unmarshal methods for JSON and
// YAML, which gracefully handle numbers and bools.
//
// swagger:model
// swagger:type object
//
// Example: {"eth0":{"network":"incusbr0","type":"nic"}
type DevicesMap map[string]map[string]string

// Backwards compatibility tests.
var (
	_ map[string]map[string]string = DevicesMap{}
	_ DevicesMap                   = map[string]map[string]string{}
)

// UnmarshalJSON implements json.Unmarshaler interface.
func (m *DevicesMap) UnmarshalJSON(data []byte) error {
	var raw map[string]any

	err := json.Unmarshal(data, &raw)
	if err != nil {
		return fmt.Errorf("JSON data not valid for DevicesMap: %w", err)
	}

	return m.fromMapStringAny(raw)
}

// UnmarshalYAML implements yaml.Unmarshaler interface.
func (m *DevicesMap) UnmarshalYAML(unmarshal func(any) error) error {
	var raw map[string]any
	err := unmarshal(&raw)
	if err != nil {
		return fmt.Errorf("YAML data not valid for DevicesMap: %w", err)
	}

	return m.fromMapStringAny(raw)
}

func (m *DevicesMap) fromMapStringAny(raw map[string]any) error {
	if raw == nil {
		return nil
	}

	result := *m
	if result == nil {
		result = make(DevicesMap, len(raw))
	}

	for k, v := range raw {
		switch val := v.(type) {
		case map[string]any:
			res, err := fromMapStringAnyInner(val, result[k])
			if err != nil {
				return fmt.Errorf("inner type for %q: %w", k, err)
			}

			result[k] = res

		case map[any]any:
			mapStr := make(map[string]any, len(val))
			for key, value := range val {
				strKey, ok := key.(string)
				if !ok {
					return fmt.Errorf(`inner key "%v" is not a string`, key)
				}

				mapStr[strKey] = value
			}

			res, err := fromMapStringAnyInner(mapStr, result[k])
			if err != nil {
				return fmt.Errorf("inner type for %q: %w", k, err)
			}

			result[k] = res

		default:
			return fmt.Errorf("type %T is not supported in %T", v, m)
		}
	}

	*m = result

	return nil
}

func fromMapStringAnyInner(raw map[string]any, dest map[string]string) (map[string]string, error) {
	if dest == nil {
		dest = make(map[string]string, len(raw))
	}

	for k, v := range raw {
		switch val := v.(type) {
		case string:
			dest[k] = val

		case int:
			dest[k] = strconv.FormatInt(int64(val), 10)

		case uint64:
			dest[k] = strconv.FormatUint(val, 10)

		case float64:
			if val == float64(int64(val)) {
				dest[k] = strconv.FormatInt(int64(val), 10)
			} else {
				dest[k] = strconv.FormatFloat(val, 'g', -1, 64)
			}

		case bool:
			dest[k] = strconv.FormatBool(val)

		case nil:
			dest[k] = ""

		default:
			return nil, fmt.Errorf("type %T is not supported as inner type for %T", v, DevicesMap{})
		}
	}

	return dest, nil
}
