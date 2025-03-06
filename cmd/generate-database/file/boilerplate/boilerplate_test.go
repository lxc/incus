package boilerplate

import (
	"testing"
)

func Test(t *testing.T) {
	// Fake the usage of the private variables and functions in the boilerplate.
	_ = mapErr
	_ = defaultMapErr
	_ = marshal
	_ = unmarshal
	_ = marshalJSON
	_ = unmarshalJSON
	_ = selectObjects
	_ = scan
}
