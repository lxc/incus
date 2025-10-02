package instancewriter

import (
	"io"
	"os"
)

// InstanceWriter is the instance writer interface.
type InstanceWriter interface {
	ResetHardLinkMap()
	WriteFile(name string, srcPath string, fi os.FileInfo, ignoreGrowth bool) error
	WriteFileFromReader(src io.Reader, fi os.FileInfo) error
}
