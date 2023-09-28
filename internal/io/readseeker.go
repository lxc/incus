package io

import (
	"io"
)

type readSeeker struct {
	io.Reader
	io.Seeker
}

// NewReadSeeker combines provided io.Reader and io.Seeker into a new io.ReadSeeker.
func NewReadSeeker(reader io.Reader, seeker io.Seeker) io.ReadSeeker {
	return &readSeeker{Reader: reader, Seeker: seeker}
}

func (r *readSeeker) Read(p []byte) (n int, err error) {
	return r.Reader.Read(p)
}

func (r *readSeeker) Seek(offset int64, whence int) (int64, error) {
	return r.Seeker.Seek(offset, whence)
}
