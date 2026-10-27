package filter

import (
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/lxc/incus/v7/shared/api"
)

// DotPrefixMatch finds the shortest unambiguous identifier for a given namespace.
func DotPrefixMatch(short string, full string) bool {
	fullMembs := strings.Split(full, ".")
	shortMembs := strings.Split(short, ".")

	if len(fullMembs) != len(shortMembs) {
		return false
	}

	if !strings.HasPrefix(fullMembs[0], shortMembs[0]) {
		return false
	}

	for i := 1; i < len(fullMembs); i++ {
		if fullMembs[i] != shortMembs[i] {
			return false
		}
	}

	return true
}

// ValueOf returns the value of the given field.
func ValueOf(obj any, field string) any {
	value := reflect.ValueOf(obj)
	typ := value.Type()
	parts := strings.Split(field, ".")

	key := parts[0]
	rest := strings.Join(parts[1:], ".")

	if value.Kind() == reflect.Map {
		switch reflect.TypeOf(obj).Elem().Kind() {
		case reflect.String:
			m := map[string]string{}
			switch mm := value.Interface().(type) {
			case map[string]string:
				m = mm
			case api.ConfigMap:
				m = mm
			}

			// Prefer an exact key match.
			v, ok := m[field]
			if ok {
				return v
			}

			// Fall back to prefix matching, in sorted order for determinism.
			for _, k := range slices.Sorted(maps.Keys(m)) {
				if DotPrefixMatch(field, k) {
					return m[k]
				}
			}

			return m[field]

		case reflect.Map:
			for _, entry := range value.MapKeys() {
				if entry.Interface() != key {
					continue
				}

				m := value.MapIndex(entry)
				return ValueOf(m.Interface(), rest)
			}

			return nil

		default:
			return nil
		}
	}

	for i := range value.NumField() {
		fieldValue := value.Field(i)
		fieldType := typ.Field(i)
		yaml := fieldType.Tag.Get("yaml")

		if yaml == ",inline" {
			v := ValueOf(fieldValue.Interface(), field)
			if v != nil {
				return v
			}
		}

		yamlKey, _, _ := strings.Cut(yaml, ",")
		if yamlKey == key {
			v := fieldValue.Interface()
			if len(parts) == 1 {
				return v
			}

			return ValueOf(v, rest)
		}
	}

	return nil
}
