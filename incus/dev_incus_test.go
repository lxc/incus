package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/cyphar/incus/incus/sys"
	"github.com/cyphar/incus/incus/ucred"
)

var testDir string

type DevLxdDialer struct {
	Path string
}

func (d DevLxdDialer) DevLxdDial(ctx context.Context, network, path string) (net.Conn, error) {
	addr, err := net.ResolveUnixAddr("unix", d.Path)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return nil, err
	}

	return conn, err
}

func setupDir() error {
	var err error

	testDir, err = os.MkdirTemp("", "lxd_test_devlxd_")
	if err != nil {
		return err
	}

	err = sys.SetupTestCerts(testDir)
	if err != nil {
		return err
	}

	err = os.Chmod(testDir, 0700)
	if err != nil {
		return err
	}

	_ = os.MkdirAll(fmt.Sprintf("%s/devlxd", testDir), 0755)

	return os.Setenv("LXD_DIR", testDir)
}

func setupSocket() (*net.UnixListener, error) {
	_ = setupDir()
	path := filepath.Join(testDir, "test-devlxd-sock")
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, err
	}

	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, err
	}

	return listener, nil
}

func connect(path string) (*net.UnixConn, error) {
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func TestCredsSendRecv(t *testing.T) {
	result := make(chan int32, 1)

	listener, err := setupSocket()
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = listener.Close() }()
	defer func() { _ = os.RemoveAll(testDir) }()

	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			t.Log(err)
			result <- -1
			return
		}

		defer func() { _ = conn.Close() }()

		cred, err := ucred.GetCred(conn)
		if err != nil {
			t.Log(err)
			result <- -1
			return
		}

		result <- cred.Pid
	}()

	conn, err := connect(fmt.Sprintf("%s/test-devlxd-sock", testDir))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conn.Close() }()

	pid := <-result
	if pid != int32(os.Getpid()) {
		t.Fatal("pid mismatch: ", pid, os.Getpid())
	}
}

/*
 * Here we're not really testing the API functionality (we can't, since it
 * expects us to be inside a container to work), but it is useful to test that
 * all the grotty connection extracting stuff works (that is, it gets to the
 * point where it realizes the pid isn't in a container without crashing).
 */
func TestHttpRequest(t *testing.T) {
	_ = setupDir()
	defer func() { _ = os.RemoveAll(testDir) }()

	d := defaultDaemon()
	d.os.MockMode = true
	err := d.Init()
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = d.Stop(context.Background(), unix.SIGQUIT) }()

	c := http.Client{Transport: &http.Transport{DialContext: DevLxdDialer{Path: fmt.Sprintf("%s/devlxd/sock", testDir)}.DevLxdDial}}

	raw, err := c.Get("http://1.0")
	if err != nil {
		t.Fatal(err)
	}

	if raw.StatusCode != 500 {
		t.Fatal(err)
	}

	resp, err := io.ReadAll(raw.Body)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(resp), pidNotInContainerErr.Error()) {
		t.Fatal("resp error not expected: ", string(resp))
	}
}
