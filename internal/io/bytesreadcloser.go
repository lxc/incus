package io

import (
	"bytes"
)

// BytesReadCloser is a basic in-memory reader with a closer interface.
type BytesReadCloser struct {
	Buf *bytes.Buffer
}

// Read just returns the buffer.
func (r BytesReadCloser) Read(b []byte) (n int, err error) {
	return r.Buf.Read(b)
}

// Close is a no-op.
func (r BytesReadCloser) Close() error {
	return nil
}
