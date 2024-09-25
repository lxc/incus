package util

import (
	"bytes"
	"encoding/gob"
)

// DeepCopy copies src to dest by using encoding/gob so its not that fast.
func DeepCopy(src, dest any) error {
	buff := new(bytes.Buffer)
	enc := gob.NewEncoder(buff)
	dec := gob.NewDecoder(buff)
	err := enc.Encode(src)
	if err != nil {
		return err
	}

	err = dec.Decode(dest)
	if err != nil {
		return err
	}

	return nil
}
