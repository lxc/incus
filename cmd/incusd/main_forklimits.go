package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var reLimitsArg = regexp.MustCompile(`^limit=(\w+):(\w+):(\w+)$`)

type cmdForklimits struct {
	global *cmdGlobal
}

func (c *cmdForklimits) command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forklimits [fd=<number>...] [limit=<name>:<softlimit>:<hardlimit>...] -- <command> [<arg>...]"
	cmd.Short = "Execute a task inside the container"
	cmd.Long = `Description:
  Execute a command with specific limits set.

  This internal command is used to spawn a command with limits set. It can also pass through one or more filed escriptors specified by fd=n arguments.
  These are passed through in the order they are specified.
`
	cmd.RunE = c.run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForklimits) run(cmd *cobra.Command, _ []string) error {
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

		if limit.name == "memlock" {
			resource = unix.RLIMIT_MEMLOCK
		} else {
			return fmt.Errorf("Unsupported limit type: %q", limit.name)
		}

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

	return unix.Exec(cmdParts[0], cmdParts, os.Environ())
}
