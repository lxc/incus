package util

import (
	"io"
)

// SafeCopy behaves like io.Copy but performs the copy through a loop of
// io.CopyN calls using a fixed 4MiB chunk size.
func SafeCopy(dst io.Writer, src io.Reader) (int64, error) {
	const chunkSize = 4 * 1024 * 1024

	var written int64
	for {
		n, err := io.CopyN(dst, src, chunkSize)
		written += n
		if err != nil {
			if err == io.EOF {
				return written, nil
			}

			return written, err
		}
	}
}
