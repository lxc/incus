//go:build linux

package linux

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMountNoSymlinkFollow checks that a symlink planted under a nosymfollow mount can't redirect a write outside of it (GHSA-579w-c4rw-c8q3).
func TestMountNoSymlinkFollow(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Mounting requires root")
	}

	work := t.TempDir()
	tree := filepath.Join(work, "tree")
	victim := filepath.Join(work, "victim")

	require.NoError(t, os.Mkdir(tree, 0o755))
	require.NoError(t, os.WriteFile(victim, []byte("KEEP"), 0o644))
	require.NoError(t, os.Symlink(victim, filepath.Join(tree, "escape")))

	unmount, err := MountNoSymlinkFollow(tree)
	require.NoError(t, err)

	defer func() { _ = unmount() }()

	f, err := os.OpenFile(filepath.Join(tree, "escape"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err == nil {
		_ = f.Close()
		t.Fatal("Write through a planted symlink was followed under a nosymfollow mount")
	}

	got, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "KEEP", string(got))
}
