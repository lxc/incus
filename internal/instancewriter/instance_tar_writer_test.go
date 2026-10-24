//go:build linux && cgo

package instancewriter

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tarEntries packs root with Walk + WriteFile and returns the names and contents in the tarball.
func tarEntries(t *testing.T, root string) map[string]string {
	t.Helper()

	buf := &bytes.Buffer{}
	ctw := NewInstanceTarWriter(buf, nil)

	err := Walk(root, func(srcPath string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		name := filepath.Join("backup", filepath.Clean("/"+srcPath[len(root):]))
		return ctw.WriteFile(name, srcPath, fi, false)
	})
	require.NoError(t, err)
	require.NoError(t, ctw.Close())

	out := map[string]string{}
	tr := tar.NewReader(buf)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}

		require.NoError(t, err)

		content, err := io.ReadAll(tr)
		require.NoError(t, err)
		out[hdr.Name] = string(content)
	}

	return out
}

// TestWriteFileNormal checks that a plain tree is packed with its file contents.
func TestWriteFileNormal(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "file"), []byte("hello"), 0o644))

	entries := tarEntries(t, root)
	assert.Equal(t, "hello", entries["backup/sub/file"])
}

// TestWalkDoesNotEscape checks that a directory swapped for a symlink out of the tree isn't followed.
func TestWalkDoesNotEscape(t *testing.T) {
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o600))

	root := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))

	entries := tarEntries(t, root)

	// The symlink is recorded as a symlink, its target is never read.
	_, hasSecret := entries["backup/link/secret"]
	assert.False(t, hasSecret)
	assert.NotContains(t, entries, "backup/link/secret")
}
