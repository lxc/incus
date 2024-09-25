//go:build windows

package main

import (
	"errors"
	"io"
	"os"

	"github.com/mattn/go-colorable"
	"golang.org/x/sys/windows"
)

func getStdout() io.WriteCloser {
	return &WrappedWriteCloser{os.Stdout, colorable.NewColorableStdout()}
}

func getStdoutFd() int {
	return int(windows.Stdout)
}

func getStdinFd() int {
	return int(windows.Stdin)
}

func getEnviron() []string {
	return windows.Environ()
}

func doExec(argv0 string, argv []string, envv []string) error {
	return errors.New("not supported by windows")
}
