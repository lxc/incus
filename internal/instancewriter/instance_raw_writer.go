package instancewriter

import (
	"io"
	"os"
)

// InstanceRawWriter provides an InstanceWriter implementation that copies the contents of a file to another.
type InstanceRawWriter struct {
	rawWriter *os.File
}

// NewInstanceRawWriter returns an InstanceRawWriter for the provided target file.
func NewInstanceRawWriter(writer *os.File) *InstanceRawWriter {
	return &InstanceRawWriter{rawWriter: writer}
}

// ResetHardLinkMap is a no-op.
func (crw *InstanceRawWriter) ResetHardLinkMap() {}

// WriteFile is a no-op.
func (crw *InstanceRawWriter) WriteFile(name string, srcPath string, fi os.FileInfo, ignoreGrowth bool) error {
	return nil
}

// WriteFileFromReader streams a file into the target file.
func (crw *InstanceRawWriter) WriteFileFromReader(src io.Reader, fi os.FileInfo) error {
	_, err := io.CopyN(crw.rawWriter, src, fi.Size())
	return err
}
