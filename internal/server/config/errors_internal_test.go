package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Errors can be sorted by key name, and the global error message mentions the
// first of them.
func TestErrorList_Error(t *testing.T) {
	errors := ErrorList{}
	errors.add("foo", "xxx", "boom")
	errors.add("bar", "yyy", "ugh")
	errors.sort()
	assert.EqualError(t, errors, "cannot set 'bar' to 'yyy': ugh (and 1 more errors)")
}
