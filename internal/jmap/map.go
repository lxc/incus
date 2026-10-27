package jmap

import (
	"fmt"
)

// Map represents a simple JSON map.
type Map map[string]any

// GetString retrieves a value from the map as a string.
func (m Map) GetString(key string) (string, error) {
	val, ok := m[key]
	if !ok {
		return "", fmt.Errorf("Response was missing `%s`", key)
	}

	strVal, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("`%s` was not a string", key)
	}

	return strVal, nil
}

// GetMap retrieves a value from the map as a map.
func (m Map) GetMap(key string) (Map, error) {
	val, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("Response was missing `%s`", key)
	}

	mapVal, ok := val.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("`%s` was not a map, got %T", key, m[key])
	}

	return mapVal, nil
}

// GetInt retrieves a value from the map as an int.
func (m Map) GetInt(key string) (int, error) {
	val, ok := m[key]
	if !ok {
		return -1, fmt.Errorf("Response was missing `%s`", key)
	}

	floatVal, ok := val.(float64)
	if !ok {
		return -1, fmt.Errorf("`%s` was not an int", key)
	}

	return int(floatVal), nil
}

// GetBool retrieves a value from the map as a bool.
func (m Map) GetBool(key string) (bool, error) {
	val, ok := m[key]
	if !ok {
		return false, fmt.Errorf("Response was missing `%s`", key)
	}

	boolVal, ok := val.(bool)
	if !ok {
		return false, fmt.Errorf("`%s` was not a bool", key)
	}

	return boolVal, nil
}
