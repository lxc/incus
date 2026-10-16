package incus

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveUnixSocket(t *testing.T) {
	tmpDir := t.TempDir()

	// Set up dirs for active vs stale socket test.
	staleDir := t.TempDir()
	staleSock := filepath.Join(staleDir, "unix.socket")
	l, err := net.Listen("unix", staleSock)
	assert.NoError(t, err)
	_ = l.Close()

	activeDir := t.TempDir()
	activeSock := filepath.Join(activeDir, "unix.socket")
	activeListener, err := net.Listen("unix", activeSock)
	assert.NoError(t, err)
	defer func() { _ = activeListener.Close() }()

	// Dirs for fallback when nothing exists.
	fallbackDir1 := t.TempDir()
	fallbackDir2 := t.TempDir()

	// Dirs for incus-user (unix.socket not writable, unix.socket.user writable).
	userDir := t.TempDir()
	userSock := filepath.Join(userDir, "unix.socket.user")
	err = os.WriteFile(userSock, nil, 0o600)
	assert.NoError(t, err)

	origDefaultSocketDirs := defaultSocketDirs
	defer func() { defaultSocketDirs = origDefaultSocketDirs }()

	tests := []struct {
		name         string
		path         string
		envSocket    string
		envDir       string
		socketDirs   []string
		expectedPath string
		expectedProj string
		skipOS       []string
	}{
		{
			name:         "env override",
			path:         "",
			envSocket:    "/custom/override.sock",
			expectedPath: "/custom/override.sock",
		},
		{
			name:         "custom path",
			path:         "/tmp/custom.sock",
			expectedPath: "/tmp/custom.sock",
		},
		{
			name:         "incus dir",
			path:         "",
			envDir:       tmpDir,
			expectedPath: filepath.Join(tmpDir, "unix.socket"),
		},
		{
			name:         "active listener preferred over stale socket",
			path:         "",
			socketDirs:   []string{staleDir, activeDir},
			expectedPath: activeSock,
		},
		{
			name:         "default fallback when no socket exists",
			path:         "",
			socketDirs:   []string{fallbackDir1, fallbackDir2},
			expectedPath: filepath.Join(fallbackDir2, "unix.socket"),
		},
		{
			name:         "incus-user socket and project",
			path:         "",
			socketDirs:   []string{userDir},
			expectedPath: userSock,
			expectedProj: fmt.Sprintf("user-%d", os.Geteuid()),
			skipOS:       []string{"windows"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, osName := range tc.skipOS {
				if runtime.GOOS == osName {
					t.Skipf("Test not supported on %s", osName)
				}
			}

			t.Setenv("INCUS_SOCKET", tc.envSocket)
			t.Setenv("INCUS_DIR", tc.envDir)

			if len(tc.socketDirs) > 0 {
				defaultSocketDirs = tc.socketDirs
			} else {
				defaultSocketDirs = origDefaultSocketDirs
			}

			path, project := ResolveUnixSocket(tc.path)
			assert.Equal(t, tc.expectedPath, path)
			assert.Equal(t, tc.expectedProj, project)
		})
	}
}
