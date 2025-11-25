package api_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/shared/api"
)

func TestConfigMap_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string]any
		target api.ConfigMap

		assertErr require.ErrorAssertionFunc
		want      api.ConfigMap
	}{
		{
			name:  "nil",
			input: nil,

			assertErr: require.NoError,
			want:      nil,
		},
		{
			name:  "nil with defaults",
			input: nil,
			target: api.ConfigMap{
				"default_string": "string",
			},

			assertErr: require.NoError,
			want: api.ConfigMap{
				"default_string": "string",
			},
		},
		{
			name:  "empty with defaults",
			input: map[string]any{},
			target: api.ConfigMap{
				"default_string": "string",
			},

			assertErr: require.NoError,
			want: api.ConfigMap{
				"default_string": "string",
			},
		},
		{
			name: "only string",
			input: map[string]any{
				"string":  "string",
				"int":     "5",
				"float64": "5.4",
				"bool":    "true",
				"empty":   "",
				"null":    "null",
			},

			assertErr: require.NoError,
			want: api.ConfigMap{
				"string":  "string",
				"int":     "5",
				"float64": "5.4",
				"bool":    "true",
				"empty":   "",
				"null":    "null",
			},
		},
		{
			name: "mixed",
			input: map[string]any{
				"string":     "string",
				"int":        5,
				"uint":       uint64(math.MaxUint64),
				"float64":    5.4,
				"float64max": math.MaxFloat64,
				"bool":       true,
				"empty":      "",
				"null":       nil,
			},

			assertErr: require.NoError,
			want: api.ConfigMap{
				"string":     "string",
				"int":        "5",
				"uint":       "1.8446744073709552e+19",
				"float64":    "5.4",
				"float64max": "1.7976931348623157e+308",
				"bool":       "true",
				"empty":      "",
				"null":       "",
			},
		},
		{
			name: "mixed with defaults",
			input: map[string]any{
				"string":     "string",
				"int":        5,
				"uint":       uint64(math.MaxUint64),
				"float64":    5.4,
				"float64max": math.MaxFloat64,
				"bool":       true,
				"empty":      "",
				"null":       nil,
			},
			target: api.ConfigMap{
				"default_string": "string",
			},

			assertErr: require.NoError,
			want: api.ConfigMap{
				"string":         "string",
				"int":            "5",
				"uint":           "1.8446744073709552e+19",
				"float64":        "5.4",
				"float64max":     "1.7976931348623157e+308",
				"bool":           "true",
				"empty":          "",
				"null":           "",
				"default_string": "string",
			},
		},

		// errors
		{
			name: "error - unsupported type object",
			input: map[string]any{
				"invalid": map[string]any{ // invalid
					"inner": "value",
				},
			},

			assertErr: require.Error,
			want:      nil,
		},
		{
			name: "error - unsupported type object with defaults",
			input: map[string]any{
				"invalid": map[string]any{ // invalid
					"inner": "value",
				},
			},
			target: api.ConfigMap{
				"default_string": "string",
			},

			assertErr: require.Error,
			want: api.ConfigMap{
				"default_string": "string",
			},
		},
		{
			name: "error - unsupported type array",
			input: map[string]any{
				"invalid": []string{ // invalid
					"inner", "value",
				},
			},

			assertErr: require.Error,
			want:      nil,
		},
		{
			name: "error - unsupported type array with defaults",
			input: map[string]any{
				"invalid": []string{ // invalid
					"inner", "value",
				},
			},
			target: api.ConfigMap{
				"default_string": "string",
			},

			assertErr: require.Error,
			want: api.ConfigMap{
				"default_string": "string",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in, err := json.Marshal(tc.input)
			require.NoError(t, err)

			err = json.Unmarshal(in, &tc.target)
			tc.assertErr(t, err)

			require.Equal(t, tc.want, tc.target)
		})
	}
}

func TestConfigMap_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string]any
		target api.ConfigMap

		assertErr require.ErrorAssertionFunc
		want      api.ConfigMap
	}{
		{
			name:  "nil",
			input: nil,

			assertErr: require.NoError,
			want:      api.ConfigMap{},
		},
		{
			name:  "nil with defaults",
			input: nil,
			target: api.ConfigMap{
				"default_string": "string",
			},

			assertErr: require.NoError,
			want: api.ConfigMap{
				"default_string": "string",
			},
		},
		{
			name:  "empty",
			input: map[string]any{},

			assertErr: require.NoError,
			want:      api.ConfigMap{},
		},
		{
			name:  "empty with defaults",
			input: map[string]any{},
			target: api.ConfigMap{
				"default_string": "string",
			},

			assertErr: require.NoError,
			want: api.ConfigMap{
				"default_string": "string",
			},
		},
		{
			name: "only string",
			input: map[string]any{
				"string":  "string",
				"int":     "5",
				"float64": "5.4",
				"bool":    "true",
				"empty":   "",
				"null":    "null",
			},

			assertErr: require.NoError,
			want: api.ConfigMap{
				"string":  "string",
				"int":     "5",
				"float64": "5.4",
				"bool":    "true",
				"empty":   "",
				"null":    "null",
			},
		},
		{
			name: "mixed",
			input: map[string]any{
				"string":     "string",
				"int":        5,
				"uint":       uint64(math.MaxUint64),
				"float64":    5.4,
				"float64max": math.MaxFloat64,
				"bool":       true,
				"empty":      "",
				"null":       nil,
			},

			assertErr: require.NoError,
			want: api.ConfigMap{
				"string":     "string",
				"int":        "5",
				"uint":       "18446744073709551615",
				"float64":    "5.4",
				"float64max": "1.7976931348623157e+308",
				"bool":       "true",
				"empty":      "",
				"null":       "",
			},
		},
		{
			name: "mixed with defaults",
			input: map[string]any{
				"string":     "string",
				"int":        5,
				"uint":       uint64(math.MaxUint64),
				"float64":    5.4,
				"float64max": math.MaxFloat64,
				"bool":       true,
				"empty":      "",
				"null":       nil,
			},
			target: api.ConfigMap{
				"default_string": "string",
			},

			assertErr: require.NoError,
			want: api.ConfigMap{
				"string":         "string",
				"int":            "5",
				"uint":           "18446744073709551615",
				"float64":        "5.4",
				"float64max":     "1.7976931348623157e+308",
				"bool":           "true",
				"empty":          "",
				"null":           "",
				"default_string": "string",
			},
		},

		// errors
		{
			name: "error - unsupported type object",
			input: map[string]any{
				"invalid": map[string]any{ // invalid
					"inner": "value",
				},
			},

			assertErr: require.Error,
			want:      nil,
		},
		{
			name: "error - unsupported type object with defaults",
			input: map[string]any{
				"invalid": map[string]any{ // invalid
					"inner": "value",
				},
			},
			target: api.ConfigMap{
				"default_string": "string",
			},

			assertErr: require.Error,
			want: api.ConfigMap{
				"default_string": "string",
			},
		},
		{
			name: "error - unsupported type array",
			input: map[string]any{
				"invalid": []string{ // invalid
					"inner", "value",
				},
			},

			assertErr: require.Error,
			want:      nil,
		},
		{
			name: "error - unsupported type array with defaults",
			input: map[string]any{
				"invalid": []string{ // invalid
					"inner", "value",
				},
			},
			target: api.ConfigMap{
				"default_string": "string",
			},

			assertErr: require.Error,
			want: api.ConfigMap{
				"default_string": "string",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in, err := yaml.Marshal(tc.input)
			require.NoError(t, err)

			err = yaml.Unmarshal(in, &tc.target)
			tc.assertErr(t, err)

			require.Equal(t, tc.want, tc.target)
		})
	}
}
