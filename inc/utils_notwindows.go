//go:build (linux && !appengine) || darwin || freebsd || openbsd

package main

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func getStdout() io.WriteCloser {
	return os.Stdout
}

func getStdoutFd() int {
	return unix.Stdout
}

func getStdinFd() int {
	return unix.Stdin
}

func getEnviron() []string {
	return unix.Environ()
}

func doExec(argv0 string, argv []string, envv []string) error {
	return unix.Exec(argv0, argv, envv)
}
