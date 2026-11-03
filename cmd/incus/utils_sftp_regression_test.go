//go:build !windows

package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
)

// fakeFileInfo is a minimal os.FileInfo for the hostile SFTP server.
type fakeFileInfo struct {
	name string
	mode os.FileMode
	size int64
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (f fakeFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeFileInfo) Sys() any           { return nil }

// fixedListerAt serves a fixed set of entries.
type fixedListerAt []os.FileInfo

func (l fixedListerAt) ListAt(dst []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}

	n := copy(dst, l[offset:])
	if int(offset)+n >= len(l) {
		return n, io.EOF
	}

	return n, nil
}

// hostileSFTP mimics a compromised incus-agent, listing one name twice and reporting it as a symlink then as a file.
type hostileSFTP struct {
	linkTarget string
	lstatCalls int
}

func (h *hostileSFTP) Fileread(_ *sftp.Request) (io.ReaderAt, error) {
	return bytes.NewReader([]byte("CLOBBERED")), nil
}

func (h *hostileSFTP) Filewrite(_ *sftp.Request) (io.WriterAt, error) {
	return nil, os.ErrPermission
}

func (h *hostileSFTP) Filecmd(_ *sftp.Request) error {
	return os.ErrPermission
}

func (h *hostileSFTP) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	if r.Method == "List" {
		return fixedListerAt{
			fakeFileInfo{name: "evil", mode: os.ModeSymlink | 0o777},
			fakeFileInfo{name: "evil", mode: 0o644, size: 9},
		}, nil
	}

	return fixedListerAt{fakeFileInfo{name: filepath.Base(r.Filepath), mode: os.ModeDir | 0o755}}, nil
}

// Lstat reports the duplicated entry as a symlink on the first visit and as a regular file afterwards.
func (h *hostileSFTP) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	if r.Filepath == "/" {
		return fixedListerAt{fakeFileInfo{name: "/", mode: os.ModeDir | 0o755}}, nil
	}

	h.lstatCalls++
	if h.lstatCalls == 1 {
		return fixedListerAt{fakeFileInfo{name: filepath.Base(r.Filepath), mode: os.ModeSymlink | 0o777}}, nil
	}

	return fixedListerAt{fakeFileInfo{name: filepath.Base(r.Filepath), mode: 0o644, size: 9}}, nil
}

func (h *hostileSFTP) Readlink(_ string) (string, error) {
	return h.linkTarget, nil
}

// TestRecursivePullPlantedSymlink checks that a recursive pull never writes through a symlink it created itself (GHSA-wfvq-qh87-gm4j).
func TestRecursivePullPlantedSymlink(t *testing.T) {
	work := t.TempDir()
	target := filepath.Join(work, "target")
	victim := filepath.Join(work, "victim")

	require.NoError(t, os.Mkdir(target, 0o755))
	require.NoError(t, os.WriteFile(victim, []byte("KEEP"), 0o644))

	handler := &hostileSFTP{linkTarget: victim}
	handlers := sftp.Handlers{
		FileGet:  handler,
		FilePut:  handler,
		FileCmd:  handler,
		FileList: handler,
	}

	clientConn, serverConn := net.Pipe()

	server := sftp.NewRequestServer(serverConn, handlers)
	go func() { _ = server.Serve() }()
	defer func() { _ = server.Close() }()

	client, err := sftp.NewClientPipe(clientConn, clientConn)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	// Pull the root's content into the target, as "incus file pull -r" does.
	rootInfo := fakeFileInfo{name: "/", mode: os.ModeDir | 0o755}
	err = sftpRecursivePullFile(client, rootInfo, "/", "/", target, true, false, false, false)
	require.NoError(t, err)

	// A vulnerable client follows the planted symlink and overwrites the file outside the target.
	got, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "KEEP", string(got))

	// The planted name must have been replaced by a regular file.
	fi, err := os.Lstat(filepath.Join(target, "evil"))
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular())
}
