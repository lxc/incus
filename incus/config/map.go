package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/cyphar/incus/shared"
)

// Map is a structured map of config keys to config values.
//
// Each legal key is declared in a config Schema using a Key object.
type Map struct {
	schema Schema
	values map[string]string // Key/value pairs stored in the map.
}

// Load creates a new configuration Map with the given schema and initial
// values. It is meant to be called with a set of initial values that were set
// at a previous time and persisted to some storage like a database.
//
// If one or more keys fail to be loaded, return an ErrorList describing what
// went wrong. Non-failing keys are still loaded in the returned Map.
func Load(schema Schema, values map[string]string) (Map, error) {
	m := Map{
		schema: schema,
	}

	// Populate the initial values.
	_, err := m.update(values)
	return m, err
}

// Change the values of this configuration Map.
//
// Return a map of key/value pairs that were actually changed. If some keys
// fail to apply, details are included in the returned ErrorList.
func (m *Map) Change(changes map[string]any) (map[string]string, error) {
	values := make(map[string]string, len(m.schema))

	errors := ErrorList{}
	for name, change := range changes {
		key, ok := m.schema[name]

		// When a hidden value is set to "true" in the change set, it
		// means "keep it unchanged", so we replace it with our current
		// value.
		if ok && key.Hidden && change == true {
			change = m.GetRaw(name)
		}

		// A nil object means the empty string.
		if change == nil {
			change = ""
		}

		// Ensure that we were actually passed a string.
		s := reflect.ValueOf(change)
		if s.Kind() != reflect.String {
			errors.add(name, nil, fmt.Sprintf("invalid type %s", s.Kind()))
			continue
		}

		values[name] = change.(string)
	}

	if errors.Len() > 0 {
		return nil, errors
	}

	// Any key not explicitly set, is considered unset.
	for name, key := range m.schema {
		_, ok := values[name]
		if !ok {
			values[name] = key.Default
		}
	}

	names, err := m.update(values)

	changed := map[string]string{}
	for _, name := range names {
		changed[name] = m.GetRaw(name)
	}

	return changed, err
}

// Dump the current configuration held by this Map.
//
// Keys that match their default value will not be included in the dump. Also,
// if a Key has its Hidden attribute set to true, it will be rendered as
// "true", for obfuscating the actual value.
func (m *Map) Dump() map[string]any {
	values := map[string]any{}

	for name, value := range m.values {
		key, ok := m.schema[name]
		if ok {
			// Schema key
			value := m.GetRaw(name)
			if value != key.Default {
				if key.Hidden {
					values[name] = true
				} else {
					values[name] = value
				}
			}
		} else if shared.IsUserConfig(name) {
			// User key, just include it as is
			values[name] = value
		}
	}

	return values
}

// GetRaw returns the value of the given key, which must be of type String.
func (m *Map) GetRaw(name string) string {
	value, ok := m.values[name]
	// User key?
	if shared.IsUserConfig(name) {
		return value
	}
	// Schema key
	key := m.schema.mustGetKey(name)
	if !ok {
		value = key.Default
	}

	return value
}

// GetString returns the value of the given key, which must be of type String.
func (m *Map) GetString(name string) string {
	if !shared.IsUserConfig(name) {
		m.schema.assertKeyType(name, String)
	}

	return m.GetRaw(name)
}

// GetBool returns the value of the given key, which must be of type Bool.
func (m *Map) GetBool(name string) bool {
	m.schema.assertKeyType(name, Bool)
	return shared.IsTrue(m.GetRaw(name))
}

// GetInt64 returns the value of the given key, which must be of type Int64.
func (m *Map) GetInt64(name string) int64 {
	m.schema.assertKeyType(name, Int64)
	n, err := strconv.ParseInt(m.GetRaw(name), 10, 64)
	if err != nil {
		panic(fmt.Sprintf("cannot convert to int64: %v", err))
	}

	return n
}

// Update the current values in the map using the newly provided ones. Return a
// list of key names that were actually changed and an ErrorList with possible
// errors.
func (m *Map) update(values map[string]string) ([]string, error) {
	// Detect if this is the first time we're setting values. This happens
	// when Load is called.
	initial := m.values == nil

	if initial {
		m.values = make(map[string]string, len(values))
	}

	// Update our keys with the values from the given map, and keep track
	// of which keys actually changed their value.
	errors := ErrorList{}
	names := []string{}
	for name, value := range values {
		changed, err := m.set(name, value, initial)
		if err != nil {
			errors.add(name, value, err.Error())
			continue
		}

		if changed {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var err error
	if errors.Len() > 0 {
		errors.sort()
		err = errors
	}

	return names, err
}

// Set or change an individual key. Empty string means delete this value and
// effectively revert it to the default. Return a boolean indicating whether
// the value has changed, and error if something went wrong.
func (m *Map) set(name string, value string, initial bool) (bool, error) {
	// Bypass schema for user.* keys
	if shared.IsUserConfig(name) {
		current, ok := m.values[name]
		if ok && value == current {
			// Value is unchanged
			return false, nil
		}

		if value == "" {
			delete(m.values, name)
		} else {
			m.values[name] = value
		}

		return true, nil
	}

	key, ok := m.schema[name]
	if !ok {
		return false, fmt.Errorf("unknown key")
	}

	err := key.validate(value)
	if err != nil {
		return false, err
	}

	// Normalize boolan values, so the comparison below works fine.
	current := m.GetRaw(name)
	if key.Type == Bool {
		value = normalizeBool(value)
		current = normalizeBool(current)
	}

	// Compare the new value with the current one, and return now if they
	// are equal.
	if value == current {
		return false, nil
	}

	// Trigger the Setter if this is not an initial load and the key's
	// schema has declared it.
	if !initial && key.Setter != nil {
		value, err = key.Setter(value)
		if err != nil {
			return false, err
		}
	}

	if value == "" {
		delete(m.values, name)
	} else {
		m.values[name] = value
	}

	return true, nil
}

// Normalize a boolean value, converting it to the string "true" or "false".
func normalizeBool(value string) string {
	if shared.IsTrue(value) {
		return "true"
	}

	return "false"
}
