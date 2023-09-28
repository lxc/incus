package io

import (
	"bytes"
	"io"
)

func WriteAll(w io.Writer, data []byte) error {
	buf := bytes.NewBuffer(data)

	toWrite := int64(buf.Len())
	for {
		n, err := io.Copy(w, buf)
		if err != nil {
			return err
		}

		toWrite -= n
		if toWrite <= 0 {
			return nil
		}
	}
}
