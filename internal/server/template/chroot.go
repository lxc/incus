package template

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ChrootLoader is a pong2 compatible file loader which restricts all accesses to a directory.
type ChrootLoader struct {
	Path string
}

// Abs resolves a filename relative to the base directory. Absolute paths are allowed.
// When there's no base dir set, the absolute path to the filename
// will be calculated based on either the provided base directory (which
// might be a path of a template which includes another template) or
// the current working directory.
func (l ChrootLoader) Abs(base string, name string) string {
	return filepath.Clean(fmt.Sprintf("%s/%s", l.Path, name))
}

// Get reads the path's content from your local filesystem.
func (l ChrootLoader) Get(path string) (io.Reader, error) {
	// Get the full path
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}

	basePath, err := filepath.EvalSymlinks(l.Path)
	if err != nil {
		return nil, err
	}

	// Validate that we're under the expected prefix
	if !strings.HasPrefix(path, basePath) {
		return nil, errors.New("Attempting to access a file outside the instance")
	}

	// Open and read the file
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(buf), nil
}
