package util

import (
	"io"
)

// MaxBytesReader provides a ReadCloser wrapper which returns an error when reading past a set limit.
// This is based on http.MaxBytesReader but adapted for use outside of http.
func MaxBytesReader(r io.Reader, n int64) io.Reader {
	if n < 0 { // Treat negative limits as equivalent to 0.
		n = 0
	}

	return &maxBytesReader{r: r, i: n, n: n}
}

// MaxBytesError is returned by [MaxBytesReader] when its read limit is exceeded.
type MaxBytesError struct {
	Limit int64
}

func (e *MaxBytesError) Error() string {
	return "input data too large"
}

type maxBytesReader struct {
	r   io.Reader // underlying reader
	i   int64     // max bytes initially, for MaxBytesError
	n   int64     // max bytes remaining
	err error     // sticky error
}

func (l *maxBytesReader) Read(p []byte) (n int, err error) {
	if l.err != nil {
		return 0, l.err
	}

	if len(p) == 0 {
		return 0, nil
	}

	// If they asked for a 32KB byte read but only 5 bytes are
	// remaining, no need to read 32KB. 6 bytes will answer the
	// question of the whether we hit the limit or go past it.
	// 0 < len(p) < 2^63
	if int64(len(p))-1 > l.n {
		p = p[:l.n+1]
	}

	n, err = l.r.Read(p)
	if int64(n) <= l.n {
		l.n -= int64(n)
		l.err = err

		return n, err
	}

	n = int(l.n)
	l.n = 0

	l.err = &MaxBytesError{l.i}
	return n, l.err
}
