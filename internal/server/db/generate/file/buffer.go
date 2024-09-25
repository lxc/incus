package file

import (
	"bytes"
	"fmt"
	"go/format"
)

// Buffer for accumulating source code output.
type Buffer struct {
	buf *bytes.Buffer
}

// Create a new source code text buffer.
func newBuffer() *Buffer {
	return &Buffer{
		buf: bytes.NewBuffer(nil),
	}
}

// L accumulates a single line of source code.
func (b *Buffer) L(format string, a ...any) {
	fmt.Fprintf(b.buf, format, a...)
	b.N()
}

// N accumulates a single new line.
func (b *Buffer) N() {
	fmt.Fprintf(b.buf, "\n")
}

// Returns the source code to add to the target file.
func (b *Buffer) code() ([]byte, error) {
	code, err := format.Source(b.buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("Can't format generated source code: %w", err)
	}

	return code, nil
}
