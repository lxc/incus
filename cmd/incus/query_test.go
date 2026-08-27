package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryParseHeaders(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected http.Header
		wantErr  string
	}{
		{
			name:     "none",
			input:    nil,
			expected: http.Header{},
		},
		{
			name:     "simple",
			input:    []string{"X-Incus-mode: 0644"},
			expected: http.Header{"X-Incus-Mode": []string{"0644"}},
		},
		{
			name:     "value containing a colon",
			input:    []string{"X-Test: a:b"},
			expected: http.Header{"X-Test": []string{"a:b"}},
		},
		{
			name:     "surrounding whitespace is trimmed",
			input:    []string{"  X-Test  :   value  "},
			expected: http.Header{"X-Test": []string{"value"}},
		},
		{
			name:     "no space after the colon",
			input:    []string{"X-Test:value"},
			expected: http.Header{"X-Test": []string{"value"}},
		},
		{
			name:     "repeated name appends",
			input:    []string{"X-Test: a", "X-Test: b"},
			expected: http.Header{"X-Test": []string{"a", "b"}},
		},
		{
			name:     "empty value marks for removal",
			input:    []string{"Content-Type:"},
			expected: http.Header{"Content-Type": []string{}},
		},
		{
			name:     "a later value undoes a removal",
			input:    []string{"Content-Type:", "Content-Type: application/json"},
			expected: http.Header{"Content-Type": []string{"application/json"}},
		},
		{
			name:     "a removal undoes earlier values",
			input:    []string{"Content-Type: application/json", "Content-Type:"},
			expected: http.Header{"Content-Type": []string{}},
		},
		{
			name:    "missing colon",
			input:   []string{"bogus"},
			wantErr: `Bad header, expecting "name: value": "bogus"`,
		},
		{
			name:    "empty name",
			input:   []string{": value"},
			wantErr: `Bad header, empty name: ": value"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := cmdQuery{flagHeaders: tt.input}

			headers, err := c.parseHeaders()
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, headers)
		})
	}
}
