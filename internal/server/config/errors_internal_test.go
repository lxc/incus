package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ErrorList_Error_NoErrors(t *testing.T) {
	// arrange
	errors := &ErrorList{}

	// assert
	assert.EqualError(t, errors, "no errors")
}

func Test_ErrorList_Error_OneError(t *testing.T) {
	// arrange
	errors := &ErrorList{}
	errors.add("qux", "zzz", "bean")

	// assert
	assert.EqualError(t, errors, "cannot set 'qux' to 'zzz': bean")
}

func Test_ErrorList_Error_TwoErrorsSorted(t *testing.T) {
	// arrange
	errors := &ErrorList{}
	errors.add("foo", "xxx", "boom")
	errors.add("bar", "yyy", "ugh")

	// act
	errors.sort()

	// assert
	assert.EqualError(t, errors, "cannot set 'bar' to 'yyy': ugh (and 1 more errors)")
}

func Test_ErrorList_Error_TwoErrorsUnsorted(t *testing.T) {
	// arrange
	errors := &ErrorList{}
	errors.add("foo", "xxx", "boom")
	errors.add("bar", "yyy", "ugh")

	// assert
	assert.EqualError(t, errors, "cannot set 'foo' to 'xxx': boom (and 1 more errors)")
}

func Test_ErrorList_Error_MoreThanTwoErrorsUnsorted(t *testing.T) {
	// arrange
	errors := &ErrorList{}
	errors.add("foo", "xxx", "boom")
	errors.add("bar", "yyy", "ugh")
	errors.add("qux", "zzz", "bean")

	// assert
	assert.EqualError(t, errors, "cannot set 'foo' to 'xxx': boom (and 2 more errors)")
}
