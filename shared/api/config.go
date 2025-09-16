package api

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type ConfigMap map[string]string

// Backwards compatibility tests.
var (
	_ map[string]string = ConfigMap{}
	_ ConfigMap         = map[string]string{}
)

func (m *ConfigMap) UnmarshalJSON(data []byte) error {
	var raw map[string]any

	err := json.Unmarshal(data, &raw)
	if err != nil {
		return fmt.Errorf("JSON data not valid for ConfigMap: %w", err)
	}

	return m.fromMapStringAny(raw)
}

func (m *ConfigMap) UnmarshalYAML(unmarshal func(any) error) error {
	var raw map[string]any
	err := unmarshal(&raw)
	if err != nil {
		return fmt.Errorf("YAML data not valid for ConfigMap: %w", err)
	}

	return m.fromMapStringAny(raw)
}

func (m *ConfigMap) fromMapStringAny(raw map[string]any) error {
	if raw == nil {
		*m = nil
		return nil
	}

	result := make(ConfigMap, len(raw))
	for k, v := range raw {
		switch val := v.(type) {
		case string:
			result[k] = val

		case int:
			result[k] = strconv.FormatInt(int64(val), 10)

		case uint64:
			result[k] = strconv.FormatUint(val, 10)

		case float64:
			if val == float64(int64(val)) {
				result[k] = strconv.FormatInt(int64(val), 10)
			} else {
				result[k] = strconv.FormatFloat(val, 'g', -1, 64)
			}

		case bool:
			result[k] = strconv.FormatBool(val)

		case nil:
			result[k] = ""

		default:
			return fmt.Errorf("type %T is not supported in %T", v, result)

		}
	}

	*m = result

	return nil
}
