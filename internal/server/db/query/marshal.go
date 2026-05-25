package query

import (
	"errors"
)

// Marshaler is the interface implemented by types that can marshal themselves into a database value.
type Marshaler interface {
	MarshalDB() (string, error)
}

// Unmarshaler is the interface implemented by types that can unmarshal a database value into themselves.
type Unmarshaler interface {
	UnmarshalDB(string) error
}

// Marshal converts the given value into its database string representation using the Marshaler interface.
func Marshal(v any) (string, error) {
	marshaller, ok := v.(Marshaler)
	if !ok {
		return "", errors.New("Cannot marshal data, type does not implement DBMarshaler")
	}

	return marshaller.MarshalDB()
}

// Unmarshal populates the given value from its database string representation using the Unmarshaler interface.
func Unmarshal(data string, v any) error {
	if v == nil {
		return errors.New("Cannot unmarshal data into nil value")
	}

	unmarshaler, ok := v.(Unmarshaler)
	if !ok {
		return errors.New("Cannot marshal data, type does not implement DBUnmarshaler")
	}

	return unmarshaler.UnmarshalDB(data)
}
