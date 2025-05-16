package ioprogress

import (
	"errors"
	"io"
)

// ProgressReader is a wrapper around ReadCloser which allows for progress tracking.
type ProgressReader struct {
	io.Reader
	io.ReadCloser
	Tracker *ProgressTracker
}

// Read in ProgressReader is the same as io.Read.
func (pt *ProgressReader) Read(p []byte) (int, error) {
	var reader io.Reader
	if pt.ReadCloser != nil {
		reader = pt.ReadCloser
	} else if pt.Reader != nil {
		reader = pt.Reader
	} else {
		return -1, errors.New("ProgressReader is missing a reader")
	}

	// Do normal reader tasks
	n, err := reader.Read(p)

	// Do the actual progress tracking
	if pt.Tracker != nil {
		pt.Tracker.total += int64(n)
		pt.Tracker.update(n)
	}

	return n, err
}
