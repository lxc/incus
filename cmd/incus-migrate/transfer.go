package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/migration"
	"github.com/lxc/incus/v6/internal/rsync"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/ws"
)

// Send an rsync stream of a path over a websocket.
func rsyncSend(ctx context.Context, conn *websocket.Conn, path string, rsyncArgs string, instanceType api.InstanceType) error {
	cmd, dataSocket, stderr, err := rsyncSendSetup(ctx, path, rsyncArgs, instanceType)
	if err != nil {
		return err
	}

	if dataSocket != nil {
		defer func() { _ = dataSocket.Close() }()
	}

	readDone, writeDone := ws.Mirror(conn, dataSocket)
	<-writeDone
	_ = dataSocket.Close()

	output, err := io.ReadAll(stderr)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("Failed to rsync: %v\n%s", err, output)
	}

	err = cmd.Wait()
	<-readDone

	if err != nil {
		return fmt.Errorf("Failed to rsync: %v\n%s", err, output)
	}

	return nil
}

// Spawn the rsync process.
func rsyncSendSetup(ctx context.Context, path string, rsyncArgs string, instanceType api.InstanceType) (*exec.Cmd, net.Conn, io.ReadCloser, error) {
	auds := fmt.Sprintf("@incus-migrate/%s", uuid.New().String())
	if len(auds) > linux.ABSTRACT_UNIX_SOCK_LEN-1 {
		auds = auds[:linux.ABSTRACT_UNIX_SOCK_LEN-1]
	}

	l, err := net.Listen("unix", auds)
	if err != nil {
		return nil, nil, nil, err
	}

	execPath, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return nil, nil, nil, err
	}

	if !util.PathExists(execPath) {
		execPath = os.Args[0]
	}

	rsyncCmd := fmt.Sprintf("sh -c \"%s netcat %s\"", execPath, auds)

	args := []string{
		"-ar",
		"--devices",
		"--numeric-ids",
		"--partial",
		"--sparse",
	}

	if instanceType == api.InstanceTypeContainer {
		args = append(args, "--xattrs", "--delete", "--compress", "--compress-level=2")
	}

	if instanceType == api.InstanceTypeVM {
		args = append(args, "--exclude", "root.img")
	}

	if rsync.AtLeast("3.1.3") {
		args = append(args, "--filter=-x security.selinux")
	}

	if rsync.AtLeast("3.1.0") {
		args = append(args, "--ignore-missing-args")
	}

	if rsyncArgs != "" {
		args = append(args, strings.Split(rsyncArgs, " ")...)
	}

	args = append(args, []string{path, "localhost:/tmp/foo"}...)
	args = append(args, []string{"-e", rsyncCmd}...)

	cmd := exec.CommandContext(ctx, "rsync", args...)
	cmd.Stdout = os.Stderr

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, err
	}

	err = cmd.Start()
	if err != nil {
		return nil, nil, nil, err
	}

	conn, err := l.Accept()
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, nil, err
	}

	_ = l.Close()

	return cmd, conn, stderr, nil
}

func protoSendError(ws *websocket.Conn, err error) {
	migration.ProtoSendControl(ws, err)

	if err != nil {
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		_ = ws.WriteMessage(websocket.CloseMessage, closeMsg)
		_ = ws.Close()
	}
}
