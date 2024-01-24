//go:build linux && cgo && !agent

package sys

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// NewTestOS returns a new OS instance initialized with test values.
func NewTestOS(t *testing.T) (*OS, func()) {
	dir, err := os.MkdirTemp("", "incus-sys-os-test-")
	require.NoError(t, err)
	require.NoError(t, SetupTestCerts(dir))

	cleanup := func() {
		require.NoError(t, os.RemoveAll(dir))
	}

	os := &OS{
		// FIXME: setting mock mode can be avoided once daemon tasks
		// are fixed to exit gracefully. See daemon.go.
		MockMode: true,

		VarDir:   dir,
		CacheDir: filepath.Join(dir, "cache"),
		LogDir:   filepath.Join(dir, "log"),
		RunDir:   filepath.Join(dir, "run"),
	}

	_, err = os.Init()
	require.NoError(t, err)

	return os, cleanup
}

// SetupTestCerts populates the given test directory with server certificates.
//
// Since generating certificates is CPU intensive, they will be simply
// symlink'ed from the test/deps/ directory.
//
//	FIXME: this function is exported because some tests use it
//		directly. Eventually we should rework those tests to use NewTestOS
//		instead.
func SetupTestCerts(dir string) error {
	_, filename, _, _ := runtime.Caller(0)
	deps := filepath.Join(filepath.Dir(filename), "..", "..", "..", "test", "deps")
	for _, f := range []string{"server.crt", "server.key"} {
		err := os.Symlink(filepath.Join(deps, f), filepath.Join(dir, f))
		if err != nil {
			return err
		}
	}
	return nil
}
