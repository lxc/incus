package io

import (
	"fmt"
	"io"
)

// QuotaWriter returns an error once a given write quota gets exceeded.
type QuotaWriter struct {
	writer io.Writer
	quota  int64
	n      int64
}

// NewQuotaWriter returns a new QuotaWriter wrapping the given writer.
//
// If the given quota is negative, then no quota is applied.
func NewQuotaWriter(writer io.Writer, quota int64) *QuotaWriter {
	return &QuotaWriter{
		writer: writer,
		quota:  quota,
	}
}

// Write implements the Writer interface.
func (w *QuotaWriter) Write(p []byte) (n int, err error) {
	if w.quota >= 0 {
		w.n += int64(len(p))
		if w.n > w.quota {
			return 0, fmt.Errorf("reached %d bytes, exceeding quota of %d", w.n, w.quota)
		}
	}

	return w.writer.Write(p)
}
