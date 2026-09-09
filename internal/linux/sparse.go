package linux

import (
	"bytes"
	"io"
	"os"
)

// sparseBlockSize is the granularity at which zero-filled data is turned into holes.
const sparseBlockSize = 4096

var sparseZeroBlock = make([]byte, sparseBlockSize)

// SparseFileWrapper wraps os.File to create sparse files.
type SparseFileWrapper struct {
	w *os.File
}

// NewSparseFileWrapper returns a SparseFileWrapper for the provided file.
func NewSparseFileWrapper(w *os.File) *SparseFileWrapper {
	return &SparseFileWrapper{w: w}
}

// Write performs the write but leaves holes in place of zero-filled blocks.
func (sfw *SparseFileWrapper) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		zero := sparseIsZero(p[written:])
		end := sparseRunEnd(p, written, zero)

		if zero {
			_, err := sfw.w.Seek(int64(end-written), io.SeekCurrent)
			if err != nil {
				return written, err
			}

			written = end
			continue
		}

		n, err := sfw.w.Write(p[written:end])
		written += n
		if err != nil {
			return written, err
		}
	}

	return written, nil
}

// sparseIsZero checks whether the first block of p is zero-filled.
func sparseIsZero(p []byte) bool {
	size := min(len(p), sparseBlockSize)

	return bytes.Equal(p[:size], sparseZeroBlock[:size])
}

// sparseRunEnd returns the end of the run of blocks starting at offset that match zero.
func sparseRunEnd(p []byte, offset int, zero bool) int {
	end := offset
	for end < len(p) && sparseIsZero(p[end:]) == zero {
		end += sparseBlockSize
	}

	return min(end, len(p))
}
