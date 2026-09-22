package filter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/v7/internal/filter"
)

func TestRegexpToLike(t *testing.T) {
	cases := map[string]string{
		"foo":         "foo",
		"^foo$":       "foo",
		"^foo":        "foo%",
		"foo$":        "%foo",
		"^foo.*$":     "foo%",
		"foo.*":       "foo%",
		".*foo.*":     "%foo%",
		"^foo.*bar$":  "foo%bar",
		"f.o":         "f_o",
		"^web_1%$":    "web\\_1\\%",
		"":            "",
		"^$":          "",
		"foo\\.bar":   "",
		"(^foo$|^f.)": "",
		"foo|bar":     "",
		"foo+":        "",
		"foo,bar":     "",
		"[a-z]":       "",
		"^foo$bar":    "",
	}

	for value, expected := range cases {
		like, ok := filter.RegexpToLike(value)
		if expected == "" && value != "" && value != "^$" {
			assert.False(t, ok, value)
			continue
		}

		assert.True(t, ok, value)
		assert.Equal(t, expected, like, value)
	}
}
