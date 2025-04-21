package util

import (
	"errors"
	"io"

	"gopkg.in/yaml.v3"
)

// YAMLUnmarshalStrict unmarshal YAML but fails on unknown fields.
func YAMLUnmarshalStrict(in io.Reader, out any) (err error) {
	decoder := yaml.NewDecoder(in)

	// Enable strict checking for unknown fields.
	decoder.KnownFields(true)

	err = decoder.Decode(out)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	return nil
}
