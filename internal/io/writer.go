package io

import (
	"bytes"
	"io"

	"github.com/lxc/incus/v6/shared/util"
)

// WriteAll copies content of data to specified writer.
func WriteAll(w io.Writer, data []byte) error {
	buf := bytes.NewBuffer(data)

	toWrite := int64(buf.Len())
	for {
		n, err := util.SafeCopy(w, buf)
		if err != nil {
			return err
		}

		toWrite -= n
		if toWrite <= 0 {
			return nil
		}
	}
}
