package config

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Exercise valid values.
func TestKey_validate(t *testing.T) {
	for _, c := range validateCases {
		t.Run(c.value, func(t *testing.T) {
			assert.NoError(t, c.node.validate(c.value))
		})
	}
}

// Test cases for TestKey_validate.
var validateCases = []struct {
	node  Key
	value string
}{
	{Key{}, "hello"},
	{Key{Type: Bool}, "yes"},
	{Key{Type: Bool}, "0"},
	{Key{Type: Int64}, "666"},
	{Key{Type: Int64}, "666"},
	{Key{Type: Bool}, ""},
	{Key{Validator: isNotEmptyString, Default: "foo"}, ""},
}

// Validator that returns an error if the value is not the empty string.
func isNotEmptyString(value string) error {
	if value == "" {
		return fmt.Errorf("empty value not valid")
	}

	return nil
}

// Exercise all possible validation errors.
func TestKey_validateError(t *testing.T) {
	for _, c := range validateErrorCases {
		t.Run(c.message, func(t *testing.T) {
			err := c.node.validate(c.value)
			assert.EqualError(t, err, c.message)
		})
	}
}

// Test cases for TestKey_validateError.
var validateErrorCases = []struct {
	node    Key
	value   string
	message string
}{
	{Key{Type: Int64}, "1.2", "invalid integer"},
	{Key{Type: Bool}, "yyy", "invalid boolean"},
	{Key{Validator: func(string) error { return fmt.Errorf("ugh") }}, "", "ugh"},
	{Key{Deprecated: "don't use this"}, "foo", "deprecated: don't use this"},
}

// If a value has an expected kind code, a panic is thrown.
func TestKey_UnexpectedKind(t *testing.T) {
	value := Key{Type: 999}
	f := func() { _ = value.validate("foo") }
	assert.PanicsWithValue(t, "unexpected value type: 999", f)
}
