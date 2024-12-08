package marshal

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"go.starlark.net/starlark"
)

// starlarkObject wraps a starlark.Dict and is used to provide custom object types to the Starlark scriptlets.
// This implements the starlark.HasAttrs interface.
type starlarkObject struct {
	d        *starlark.Dict
	typeName string
}

func (s *starlarkObject) Type() string {
	return s.typeName
}

func (s *starlarkObject) String() string {
	return s.d.String()
}

func (s *starlarkObject) Freeze() {
}

func (s *starlarkObject) Hash() (uint32, error) {
	return 0, fmt.Errorf("Unhashable type %s", s.Type())
}

func (s *starlarkObject) Truth() starlark.Bool {
	return starlark.True
}

func (s *starlarkObject) AttrNames() []string {
	keys := s.d.Keys()
	keyNames := make([]string, 0, len(keys))
	for _, k := range keys {
		keyNames = append(keyNames, k.String())
	}

	return keyNames
}

func (s *starlarkObject) Attr(name string) (starlark.Value, error) {
	field, found, err := s.d.Get(starlark.String(name))
	if err != nil {
		return nil, err
	}

	if !found {
		return nil, fmt.Errorf("Invalid field %q", name)
	}

	return field, nil
}

// StarlarkMarshal converts input to a starlark Value.
// It only includes exported struct fields, and uses the "json" tag for field names.
func StarlarkMarshal(input any) (starlark.Value, error) {
	return starlarkMarshal(input, nil)
}

// starlarkMarshal converts input to a starlark Value.
// It only includes exported struct fields, and uses the "json" tag for field names.
// Takes optional parent Starlark dictionary which will be used to set fields from anonymous (embedded) structs
// in to the parent struct.
func starlarkMarshal(input any, parent *starlark.Dict) (starlark.Value, error) {
	if input == nil {
		return starlark.None, nil
	}

	sv, ok := input.(starlark.Value)
	if ok {
		return sv, nil
	}

	var err error

	v := reflect.ValueOf(input)

	switch v.Type().Kind() {
	case reflect.String:
		sv = starlark.String(v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		sv = starlark.MakeInt(int(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		sv = starlark.MakeUint(uint(v.Uint()))
	case reflect.Float32, reflect.Float64:
		sv = starlark.Float(v.Float())
	case reflect.Bool:
		sv = starlark.Bool(v.Bool())
	case reflect.Array, reflect.Slice:
		vlen := v.Len()
		listElems := make([]starlark.Value, 0, vlen)

		for i := 0; i < vlen; i++ {
			lv, err := StarlarkMarshal(v.Index(i).Interface())
			if err != nil {
				return nil, err
			}

			listElems = append(listElems, lv)
		}

		sv = starlark.NewList(listElems)
	case reflect.Map:
		mKeys := v.MapKeys()
		d := starlark.NewDict(len(mKeys))

		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("Only string keys are supported, found %s", v.Type().Key().Kind())
		}

		sort.Slice(mKeys, func(i, j int) bool {
			return mKeys[i].String() < mKeys[j].String()
		})

		for _, k := range mKeys {
			mv := v.MapIndex(k)
			dv, err := StarlarkMarshal(mv.Interface())
			if err != nil {
				return nil, err
			}

			err = d.SetKey(starlark.String(k.String()), dv)
			if err != nil {
				return nil, fmt.Errorf("Failed setting map key %q to %v: %w", k.String(), dv, err)
			}
		}

		sv = d
	case reflect.Struct:
		fieldCount := v.Type().NumField()

		d := parent
		if d == nil {
			d = starlark.NewDict(fieldCount)
		}

		for i := 0; i < fieldCount; i++ {
			field := v.Type().Field(i)
			fieldValue := v.Field(i)

			if !field.IsExported() {
				continue
			}

			if field.Anonymous && fieldValue.Kind() == reflect.Struct {
				// If anonymous struct field's value is another struct then pass the the current
				// starlark dictionary to starlarkMarshal so its fields will be set on the parent.
				_, err = starlarkMarshal(fieldValue.Interface(), d)
				if err != nil {
					return nil, err
				}
			} else {
				dv, err := StarlarkMarshal(fieldValue.Interface())
				if err != nil {
					return nil, err
				}

				key, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if key == "" {
					key = field.Name
				}

				err = d.SetKey(starlark.String(key), dv)
				if err != nil {
					return nil, fmt.Errorf("Failed setting struct field %q to %v: %w", key, dv, err)
				}
			}
		}

		// Only convert the top-level struct to a Starlark object.
		if parent == nil {
			ss := starlarkObject{
				d:        d,
				typeName: v.Type().Name(),
			}

			sv = &ss
		} else {
			sv = d
		}

	case reflect.Pointer:
		if v.IsZero() {
			sv = starlark.None
		} else {
			sv, err = StarlarkMarshal(v.Elem().Interface())
			if err != nil {
				return nil, err
			}
		}
	}

	if sv == nil {
		return nil, fmt.Errorf("Unrecognised type %v for value %+v", v.Type(), v.Interface())
	}

	return sv, nil
}

// StarlarkUnmarshal converts a Starlark value into a Go value.
// Only NoneType, Bool, Int, Float, String, List and Dict are supported.
func StarlarkUnmarshal(input starlark.Value) (any, error) {
	switch v := input.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(v), nil
	case starlark.Int:
		var result, _ = v.Int64()
		return result, nil
	case starlark.Float:
		return float64(v), nil
	case starlark.String:
		return string(v), nil
	case *starlark.List:
		length := v.Len()
		result := make([]any, length)

		// Iterate over the Starlark List
		for i := 0; i < length; i++ {
			value, err := StarlarkUnmarshal(v.Index(i))
			if err != nil {
				return nil, err
			}

			result[i] = value
		}

		return result, nil
	case *starlark.Dict:
		result := make(map[string]any)

		// Iterate over the Starlark Dict
		for _, kv := range v.Items() {
			dictKey, dictValue := kv[0], kv[1]

			key, ok := starlark.AsString(dictKey)
			if !ok {
				return nil, fmt.Errorf("Only string keys are supported, found %s", dictKey.Type())
			}

			value, err := StarlarkUnmarshal(dictValue)
			if err != nil {
				return nil, err
			}

			result[key] = value
		}

		return result, nil
	default:
		return nil, fmt.Errorf("Unsupported type: %T", v)
	}
}
