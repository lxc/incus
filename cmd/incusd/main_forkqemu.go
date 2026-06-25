package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	goselinux "github.com/opencontainers/selinux/go-selinux"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var reLimitsArg = regexp.MustCompile(`^limit=(\w+):(\w+):(\w+)$`)

// secontext=USER:ROLE:TYPE[:LEVEL] — kept deliberately permissive;
// the kernel performs the real validation at execve(2) time.
var reSEContextArg = regexp.MustCompile(`^secontext=([^\s]+:[^\s]+:[^\s]+(?::[^\s]+)?)$`)

type cmdForkqemu struct {
	global *cmdGlobal
}

func (c *cmdForkqemu) command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkqemu [fd=<number>...] [limit=<name>:<softlimit>:<hardlimit>...] [secontext=<user>:<role>:<type>[:<level>]] -- <command> [<arg>...]"
	cmd.Short = "Execute a QEMU process with optional file descriptors, limits and SELinux context"

	cmd.Long = `Description:
  Execute a QEMU process with specific limits, passed-through file descriptors
  and, when SELinux is enabled on the host, the SELinux process context
  the daemon has resolved for the instance.

  This internal command is used to spawn a command with limits set. It can also pass through one or more file descriptors
  specified by fd=n arguments.
  These are passed through in the order they are specified. If a secontext=... argument is
  given, SetExecLabel(3) is invoked just before the final exec(2) so the new
  process transitions into the requested SELinux domain.`
	cmd.RunE = c.run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkqemu) run(cmd *cobra.Command, _ []string) error {
	// Use raw args instead of cobra passed args, as we need to access the "--" argument.
	args := c.global.rawArgs(cmd)

	if len(args) == 0 {
		_ = cmd.Help()
		return nil
	}

	// Only root should run this
	if os.Geteuid() != 0 {
		return errors.New("This must be run as root")
	}

	type limit struct {
		name string
		soft string
		hard string
	}

	var limits []limit
	var fds []uintptr
	var seCtx string
	var cmdParts []string

	for i, arg := range args {
		matches := reLimitsArg.FindStringSubmatch(arg)
		if len(matches) == 4 {
			limits = append(limits, limit{
				name: matches[1],
				soft: matches[2],
				hard: matches[3],
			})
		} else if strings.HasPrefix(arg, "fd=") {
			fdParts := strings.SplitN(arg, "=", 2)
			fdNum, err := strconv.Atoi(fdParts[1])
			if err != nil {
				_ = cmd.Help()
				return errors.New("Invalid file descriptor number")
			}

			fds = append(fds, uintptr(fdNum))
		} else if seMatches := reSEContextArg.FindStringSubmatch(arg); len(seMatches) == 2 {
			if seCtx != "" {
				_ = cmd.Help()
				return errors.New("Duplicate secontext argument")
			}

			seCtx = seMatches[1]
		} else if arg == "--" {
			if len(args)-1 > i {
				cmdParts = args[i+1:]
			}

			break // No more passing of arguments needed.
		} else {
			_ = cmd.Help()
			return errors.New("Unrecognised argument")
		}
	}

	// Setup rlimits.
	for _, limit := range limits {
		var resource int
		var rLimit unix.Rlimit

		if limit.name != "memlock" {
			return fmt.Errorf("Unsupported limit type: %q", limit.name)
		}

		resource = unix.RLIMIT_MEMLOCK

		if limit.soft == "unlimited" {
			rLimit.Cur = unix.RLIM_INFINITY
		} else {
			softLimit, err := strconv.ParseUint(limit.soft, 10, 64)
			if err != nil {
				return fmt.Errorf("Invalid soft limit for %q", limit.name)
			}

			rLimit.Cur = softLimit
		}

		if limit.hard == "unlimited" {
			rLimit.Max = unix.RLIM_INFINITY
		} else {
			hardLimit, err := strconv.ParseUint(limit.hard, 10, 64)
			if err != nil {
				return fmt.Errorf("Invalid hard limit for %q", limit.name)
			}

			rLimit.Max = hardLimit
		}

		err := unix.Setrlimit(resource, &rLimit)
		if err != nil {
			return err
		}
	}

	if len(cmdParts) == 0 {
		_ = cmd.Help()
		return errors.New("Missing required command argument")
	}

	// Clear the cloexec flag on the file descriptors we are passing through.
	for _, fd := range fds {
		_, _, syscallErr := unix.Syscall(unix.SYS_FCNTL, fd, unix.F_SETFD, uintptr(0))
		if syscallErr != 0 {
			err := os.NewSyscallError(fmt.Sprintf("fcntl failed on FD %d", fd), syscallErr)
			if err != nil {
				return err
			}
		}
	}

	// Apply the SELinux exec context as the last action before the
	// final exec. SetExecLabel is thread-local on Linux, so we must
	// keep the goroutine pinned to its OS thread until execve(2) has
	// replaced the process image. We deliberately do NOT
	// UnlockOSThread: either exec succeeds (process is gone), or it
	// fails and we return — in which case the process exits anyway.
	if seCtx != "" {
		if !goselinux.GetEnabled() {
			return fmt.Errorf("secontext=%q given but SELinux is not enabled on this host", seCtx)
		}

		runtime.LockOSThread()

		err := goselinux.SetExecLabel(seCtx)
		if err != nil {
			return fmt.Errorf("Failed to set SELinux exec label %q: %w", seCtx, err)
		}
	}

	return unix.Exec(cmdParts[0], cmdParts, os.Environ())
}
