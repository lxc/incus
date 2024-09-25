package query

import (
	"fmt"
)

type Marshaler interface {
	MarshalDB() (string, error)
}

type Unmarshaler interface {
	UnmarshalDB(string) error
}

func Marshal(v any) (string, error) {
	marshaller, ok := v.(Marshaler)
	if !ok {
		return "", fmt.Errorf("Cannot marshal data, type does not implement DBMarshaler")
	}

	return marshaller.MarshalDB()
}

func Unmarshal(data string, v any) error {
	if v == nil {
		return fmt.Errorf("Cannot unmarshal data into nil value")
	}

	unmarshaler, ok := v.(Unmarshaler)
	if !ok {
		return fmt.Errorf("Cannot marshal data, type does not implement DBUnmarshaler")
	}

	return unmarshaler.UnmarshalDB(data)
}
