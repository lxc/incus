package main

/*
#include "config.h"

#include <errno.h>
#include <fcntl.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <limits.h>
#include <unistd.h>

#include "incus.h"
#include "memory_utils.h"

pid_t incus_forkbpf_pid;
int incus_forkbpf_socket_parent;
int incus_forkbpf_socket_child;

static int dosetns_file(char *file)
{
	__do_close int ns_fd = -EBADF;

	ns_fd = open(file, O_RDONLY);
	if (ns_fd < 0) {
		fprintf(stderr, "%m - Failed to open \"%s\": %s", file, strerror(errno));
		return -1;
	}

	if (setns(ns_fd, 0) < 0) {
		fprintf(stderr, "%m - Failed to attach to namespace \"%s\": %s", file, strerror(errno));
		return -1;
	}

	return 0;
}

void forkbpf(void)
{
	char *pidstr;
	char path[PATH_MAX];
	int fds[2];

	pidstr = getenv("LXC_PID");
	if (!pidstr) {
		fprintf(stderr, "No LXC_PID in environment\n");
		_exit(EXIT_FAILURE);
	}

	if (socketpair(AF_UNIX, SOCK_STREAM, 0, fds) < 0) {
		fprintf(stderr, "Failed to create socket pair: %s", strerror(errno));
		_exit(EXIT_FAILURE);
	}

	incus_forkbpf_socket_parent = fds[0];
	incus_forkbpf_socket_child = fds[1];

	incus_forkbpf_pid = fork();
	if (incus_forkbpf_pid < 0) {
		fprintf(stderr, "%s - Failed to create new process\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	if (incus_forkbpf_pid == 0) {
		// Attach to the user namespace.
		snprintf(path, sizeof(path), "/proc/%s/ns/user", pidstr);
		if (dosetns_file(path) < 0) {
			_exit(EXIT_FAILURE);
		}

		// Attach to the mount namespace.
		snprintf(path, sizeof(path), "/proc/%s/ns/mnt", pidstr);
		if (dosetns_file(path) < 0) {
			_exit(EXIT_FAILURE);
		}
	}
}
*/
import "C"

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	// Used by cgo.
	_ "github.com/lxc/incus/v6/shared/cgo"
)

type cmdForkbpf struct {
	global *cmdGlobal
}

func (c *cmdForkbpf) command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkbpf <mount-path> <cmd-types> <map-types> <prog-types> <attach-types>"
	cmd.Args = cobra.ExactArgs(5)
	cmd.Short = "Mount bpffs"
	cmd.Long = `Description:
  Mount bpffs

  This internal command is used to mount a bpf filesystem into the container for bpf token delegation.
`
	cmd.Hidden = true
	cmd.Run = c.run
	return cmd
}

func (c *cmdForkbpf) run(_ *cobra.Command, args []string) {
	mountPath := args[0]
	cmdTypes := strings.Split(args[1], ",")
	mapTypes := strings.Split(args[2], ",")
	progTypes := strings.Split(args[3], ",")
	attachTypes := strings.Split(args[4], ",")

	if C.incus_forkbpf_pid == 0 {
		err := c.runChild(int(C.incus_forkbpf_socket_child), mountPath)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "[child]: %v", err)
			os.Exit(1) // nolint:revive
		}
	} else {
		childProc, err := os.FindProcess(int(C.incus_forkbpf_pid))
		if err != nil {
			_, _ = fmt.Fprint(os.Stdout, "[parent]: Couldn't find child, assuming it already failed, exiting")
			os.Exit(1) // nolint:revive
		}

		err = c.runParent(int(C.incus_forkbpf_socket_parent), cmdTypes, mapTypes, progTypes, attachTypes)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "[parent]: Encountered error, killing child: %v", err)
			err2 := childProc.Kill()
			if err2 != nil {
				_, _ = fmt.Fprintf(os.Stdout, "[parent]: Failed to kill child: %v", err2)
			} else {
				_, err2 = childProc.Wait()
				if err2 != nil {
					_, _ = fmt.Fprintf(os.Stdout, "[parent]: Failed to wait for child: %v", err2)
				}
			}

			os.Exit(1) // nolint:revive
		}

		procState, err := childProc.Wait()
		if err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "[parent]: Failed to wait for child: %v", err)
		} else {
			if !procState.Success() {
				_, _ = fmt.Fprint(os.Stdout, "[parent]: Child process failed")
				os.Exit(1) // nolint:revive
			}
		}
	}
}

func (c *cmdForkbpf) runChild(socket int, mountPath string) error {
	fsfd, err := unix.Fsopen("bpf", 0)
	if err != nil {
		return fmt.Errorf("Failed to open bpf fs: %v", err)
	}

	rights := unix.UnixRights(fsfd)
	err = unix.Sendmsg(socket, nil, rights, nil, 0)
	if err != nil {
		return fmt.Errorf("Failed to send bpf fs fd to parent: %v", err)
	}

	data := make([]byte, unix.CmsgSpace(4))
	_, _, _, _, err = unix.Recvmsg(socket, nil, data, 0)
	if err != nil {
		return fmt.Errorf("Failed to receive from parent: %v", err)
	}

	cmsgs, err := unix.ParseSocketControlMessage(data)
	if err != nil {
		return fmt.Errorf("Failed to parse message from parent: %v", err)
	}

	fds, err := unix.ParseUnixRights(&cmsgs[0])
	if err != nil {
		return fmt.Errorf("Failed to parse fd from parent: %v", err)
	}

	err = unix.MoveMount(fds[0], "", unix.AT_FDCWD, mountPath, unix.MOVE_MOUNT_F_EMPTY_PATH)
	if err != nil {
		return fmt.Errorf("Failed to attach bpf fs mount: %v", err)
	}

	return nil
}

func (c *cmdForkbpf) setBpfDelegate(bpfFd int, key string, values []string) error {
	valuesTrimmed := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			valuesTrimmed = append(valuesTrimmed, trimmed)
		}
	}

	if len(valuesTrimmed) == 0 {
		return nil
	}

	valuesJoined := strings.Join(valuesTrimmed, ":")

	err := unix.FsconfigSetString(bpfFd, key, valuesJoined)
	if err != nil {
		return fmt.Errorf("Failed to set %s=%v on bpf fs: %v", key, valuesJoined, err)
	}

	return nil
}

func (c *cmdForkbpf) runParent(socket int, cmdTypes []string, mapTypes []string, progTypes []string, attachTypes []string) error {
	data := make([]byte, unix.CmsgSpace(4))
	_, _, _, _, err := unix.Recvmsg(socket, nil, data, 0)
	if err != nil {
		return fmt.Errorf("Failed to receive from child: %v", err)
	}

	cmsgs, err := unix.ParseSocketControlMessage(data)
	if err != nil {
		return fmt.Errorf("Failed to parse message from child: %v", err)
	}

	fds, err := unix.ParseUnixRights(&cmsgs[0])
	if err != nil {
		return fmt.Errorf("Failed to parse fd from child: %v", err)
	}

	bpfFd := fds[0]
	err = c.setBpfDelegate(bpfFd, "delegate_cmds", cmdTypes)
	if err != nil {
		return err
	}

	err = c.setBpfDelegate(bpfFd, "delegate_maps", mapTypes)
	if err != nil {
		return err
	}

	err = c.setBpfDelegate(bpfFd, "delegate_progs", progTypes)
	if err != nil {
		return err
	}

	err = c.setBpfDelegate(bpfFd, "delegate_attachs", attachTypes)
	if err != nil {
		return err
	}

	err = unix.FsconfigCreate(bpfFd)
	if err != nil {
		return fmt.Errorf("Failed to create bpf fs: %v", err)
	}

	mountFd, err := unix.Fsmount(bpfFd, 0, 0)
	if err != nil {
		return fmt.Errorf("Failed to mount bpf fs: %v", err)
	}

	rights := unix.UnixRights(mountFd)
	err = unix.Sendmsg(socket, nil, rights, nil, 0)
	if err != nil {
		return fmt.Errorf("Failed to send mount fd to child: %v", err)
	}

	return nil
}
