package subprocess

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RunError is the error from the RunCommand family of functions.
type RunError struct {
	cmd    string
	args   []string
	err    error
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func (e RunError) Error() string {
	if e.stderr.Len() == 0 {
		return fmt.Sprintf("Failed to run: %s %s: %v", e.cmd, strings.Join(e.args, " "), e.err)
	}

	return fmt.Sprintf("Failed to run: %s %s: %v (%s)", e.cmd, strings.Join(e.args, " "), e.err, strings.TrimSpace(e.stderr.String()))
}

func (e RunError) Unwrap() error {
	return e.err
}

// StdOut returns the stdout buffer.
func (e RunError) StdOut() *bytes.Buffer {
	return e.stdout
}

// StdErr returns the stdout buffer.
func (e RunError) StdErr() *bytes.Buffer {
	return e.stderr
}

// NewRunError returns new RunError.
func NewRunError(cmd string, args []string, err error, stdout *bytes.Buffer, stderr *bytes.Buffer) error {
	return RunError{
		cmd:    cmd,
		args:   args,
		err:    err,
		stdout: stdout,
		stderr: stderr,
	}
}

// RunCommandSplit runs a command with a supplied environment and optional arguments and returns the
// resulting stdout and stderr output as separate variables. If the supplied environment is nil then
// the default environment is used. If the command fails to start or returns a non-zero exit code
// then an error is returned containing the output of stderr too.
func RunCommandSplit(ctx context.Context, env []string, filesInherit []*os.File, name string, arg ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, arg...)

	if env != nil {
		cmd.Env = env
	}

	if filesInherit != nil {
		cmd.ExtraFiles = filesInherit
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return stdout.String(), stderr.String(), NewRunError(name, arg, err, &stdout, &stderr)
	}

	return stdout.String(), stderr.String(), nil
}

// RunCommandContext runs a command with optional arguments and returns stdout. If the command fails to
// start or returns a non-zero exit code then an error is returned containing the output of stderr.
func RunCommandContext(ctx context.Context, name string, arg ...string) (string, error) {
	stdout, _, err := RunCommandSplit(ctx, nil, nil, name, arg...)
	return stdout, err
}

// RunCommand runs a command with optional arguments and returns stdout. If the command fails to
// start or returns a non-zero exit code then an error is returned containing the output of stderr.
// Deprecated: Use RunCommandContext.
func RunCommand(name string, arg ...string) (string, error) {
	stdout, _, err := RunCommandSplit(context.TODO(), nil, nil, name, arg...)
	return stdout, err
}

// RunCommandInheritFds runs a command with optional arguments and passes a set
// of file descriptors to the newly created process, returning stdout. If the
// command fails to start or returns a non-zero exit code then an error is
// returned containing the output of stderr.
func RunCommandInheritFds(ctx context.Context, filesInherit []*os.File, name string, arg ...string) (string, error) {
	stdout, _, err := RunCommandSplit(ctx, nil, filesInherit, name, arg...)
	return stdout, err
}

// RunCommandCLocale runs a command with a LC_ALL=C.UTF-8 and LANGUAGE=en environment set with optional arguments and
// returns stdout. If the command fails to start or returns a non-zero exit code then an error is
// returned containing the output of stderr.
func RunCommandCLocale(name string, arg ...string) (string, error) {
	stdout, _, err := RunCommandSplit(context.TODO(), append(os.Environ(), "LC_ALL=C.UTF-8", "LANGUAGE=en"), nil, name, arg...)
	return stdout, err
}

// RunCommandWithFds runs a command with supplied file descriptors.
func RunCommandWithFds(ctx context.Context, stdin io.Reader, stdout io.Writer, name string, arg ...string) error {
	cmd := exec.CommandContext(ctx, name, arg...)

	if stdin != nil {
		cmd.Stdin = stdin
	}

	if stdout != nil {
		cmd.Stdout = stdout
	}

	var buffer bytes.Buffer
	cmd.Stderr = &buffer

	err := cmd.Run()
	if err != nil {
		return NewRunError(name, arg, err, nil, &buffer)
	}

	return nil
}

// TryRunCommand runs the specified command up to 20 times with a 500ms delay between each call
// until it runs without an error. If after 20 times it is still failing then returns the error.
func TryRunCommand(name string, arg ...string) (string, error) {
	return TryRunCommandAttemptsDuration(20, 500*time.Millisecond, name, arg...)
}

// TryRunCommandAttemptsDuration runs the specified command up to a specified number times with a
// specified delay between each call until it runs without an error. If after the number of times
// it is still failing then returns the error.
func TryRunCommandAttemptsDuration(attempts int, delay time.Duration, name string, arg ...string) (string, error) {
	var err error
	var output string

	for range attempts {
		output, err = RunCommand(name, arg...)
		if err == nil {
			break
		}

		time.Sleep(delay)
	}

	return output, err
}
