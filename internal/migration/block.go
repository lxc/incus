package migration

import (
	"io"
	"runtime"

	"github.com/pierrec/lz4/v4"
)

// nopWriteCloser wraps a writer whose closing is handled by the caller.
type nopWriteCloser struct {
	io.Writer
}

// Close does nothing.
func (nopWriteCloser) Close() error {
	return nil
}

// NewBlockWriter returns the writer to send a block volume through, which must be closed to complete the stream.
func NewBlockWriter(conn io.Writer, compress bool) (io.WriteCloser, error) {
	if !compress {
		return nopWriteCloser{conn}, nil
	}

	zw := lz4.NewWriter(conn)
	err := zw.Apply(lz4.BlockSizeOption(lz4.Block4Mb), lz4.ConcurrencyOption(min(4, runtime.NumCPU())))
	if err != nil {
		return nil, err
	}

	// Only expose Write and Close as the lz4 writer's ReadFrom can't be called more than once.
	return struct {
		io.Writer
		io.Closer
	}{zw, zw}, nil
}

// NewBlockReader returns the reader to receive a block volume from, decompressing the data if needed.
func NewBlockReader(conn io.ReadCloser, compress bool) io.ReadCloser {
	if !compress {
		return conn
	}

	// Checking for a following frame at the end of the stream consumes the connection's end of stream marker.
	return struct {
		io.Reader
		io.Closer
	}{lz4.NewReader(conn), conn}
}
