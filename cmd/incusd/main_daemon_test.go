package main

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestDaemonLock(t *testing.T) {
	dir := t.TempDir()
	fd, err := lockDaemon(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	})
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	require.NoError(t, err)
	require.Equal(t, unix.FD_CLOEXEC, flags&unix.FD_CLOEXEC)

	listener, err := net.Listen("unix", filepath.Join(dir, "unix.socket"))
	require.NoError(t, err)
	require.NoError(t, listener.Close())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDaemonLockChild$")
	child.Env = append(os.Environ(), "INCUS_TEST_DAEMON_LOCK="+dir)
	output, err := child.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "another daemon may still be running")

	// An exec child must not keep its parent's lock alive after the parent closes it.
	child = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDaemonLockChild$")
	child.Env = append(os.Environ(), "INCUS_TEST_DAEMON_LOCK="+dir, "INCUS_TEST_DAEMON_LOCK_WAIT=1")
	stdin, err := child.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, child.Start())
	t.Cleanup(func() {
		_ = stdin.Close()
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	require.NoError(t, unix.Close(fd))
	fd = -1
	_, err = stdin.Write([]byte{1})
	require.NoError(t, err)
	require.NoError(t, stdin.Close())
	require.NoError(t, child.Wait())

	fd, err = lockDaemon(dir)
	require.NoError(t, err)
}

func TestDaemonLockWaitsForDescriptorRelease(t *testing.T) {
	dir := t.TempDir()
	fd, err := lockDaemon(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	})

	done := make(chan struct {
		fd  int
		err error
	}, 1)
	go func() {
		newFD, lockErr := lockDaemon(dir)
		done <- struct {
			fd  int
			err error
		}{newFD, lockErr}
	}()

	select {
	case result := <-done:
		if result.fd >= 0 {
			_ = unix.Close(result.fd)
		}

		t.Fatalf("Lock attempt returned before descriptor release: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}

	require.NoError(t, unix.Close(fd))
	fd = -1
	result := <-done
	require.NoError(t, result.err)
	require.NoError(t, unix.Close(result.fd))
}

func TestDaemonLockChild(t *testing.T) {
	dir := os.Getenv("INCUS_TEST_DAEMON_LOCK")
	if dir == "" {
		return
	}

	if os.Getenv("INCUS_TEST_DAEMON_LOCK_WAIT") == "1" {
		_, err := io.ReadFull(os.Stdin, make([]byte, 1))
		require.NoError(t, err)
	}

	_, err := lockDaemon(dir)
	require.NoError(t, err)
}
