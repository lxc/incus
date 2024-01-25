//go:build linux

package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/shared/ioprogress"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/subprocess"
)

// RunWrapper is an optional function that's used to wrap rsync, useful for confinement like AppArmor.
var RunWrapper func(cmd *exec.Cmd, output string, allowedCmds []string) (func(), error)

type nullWriteCloser struct {
	*bytes.Buffer
}

func (nwc *nullWriteCloser) Close() error {
	return nil
}

// ExtractWithFds runs extractor process under specifc AppArmor profile.
// The allowedCmds argument specify commands which are allowed to run by apparmor.
// The cmd argument is automatically added to allowedCmds slice.
//
// This uses RunWrapper if set.
func ExtractWithFds(cmdName string, args []string, allowedCmds []string, stdin io.ReadCloser, output *os.File) error {
	// Needed for RunWrapper.
	outputPath := output.Name()
	allowedCmds = append(allowedCmds, cmdName)

	// Setup the command.
	var buffer bytes.Buffer
	cmd := exec.Command(cmdName, args...)
	cmd.Stdin = stdin
	cmd.Stdout = output
	cmd.Stderr = &nullWriteCloser{&buffer}

	// Call the wrapper if defined.
	if RunWrapper != nil {
		cleanup, err := RunWrapper(cmd, outputPath, allowedCmds)
		if err != nil {
			return err
		}

		defer cleanup()
	}

	// Run the command.
	err := cmd.Run()
	if err != nil {
		return subprocess.NewRunError(cmdName, args, err, nil, &buffer)
	}

	return nil
}

// CompressedTarReader returns a tar reader from the supplied (optionally compressed) tarball stream.
// The unpacker arguments are those returned by DetectCompressionFile().
// The returned cancelFunc should be called when finished with reader to clean up any resources used.
// This can be done before reading to the end of the tarball if desired.
//
// This uses RunWrapper if set.
func CompressedTarReader(ctx context.Context, r io.ReadSeeker, unpacker []string, outputPath string) (*tar.Reader, context.CancelFunc, error) {
	_, cancelFunc := context.WithCancel(ctx)

	_, err := r.Seek(0, io.SeekStart)
	if err != nil {
		return nil, cancelFunc, err
	}

	var tr *tar.Reader

	if len(unpacker) > 0 {
		// Setup the command.
		var buffer bytes.Buffer
		pipeReader, pipeWriter := io.Pipe()
		cmd := exec.Command(unpacker[0], unpacker[1:]...)
		cmd.Stdin = io.NopCloser(r)
		cmd.Stdout = pipeWriter
		cmd.Stderr = &nullWriteCloser{&buffer}

		// Call the wrapper if defined.
		var cleanup func()
		if RunWrapper != nil {
			cleanup, err = RunWrapper(cmd, outputPath, []string{unpacker[0]})
			if err != nil {
				return nil, cancelFunc, err
			}
		}

		// Run the command.
		err := cmd.Start()
		if err != nil {
			return nil, cancelFunc, subprocess.NewRunError(unpacker[0], unpacker[1:], err, nil, &buffer)
		}

		ctxCancelFunc := cancelFunc

		// Now that unpacker process has started, wrap context cancel function with one that waits for
		// the unpacker process to complete.
		cancelFunc = func() {
			ctxCancelFunc()
			_ = pipeWriter.Close()
			_ = cmd.Wait()

			if cleanup != nil {
				cleanup()
			}
		}

		tr = tar.NewReader(pipeReader)
	} else {
		tr = tar.NewReader(r)
	}

	return tr, cancelFunc, nil
}

// Unpack extracts image from archive.
func Unpack(file string, path string, blockBackend bool, maxMemory int64, tracker *ioprogress.ProgressTracker) error {
	extractArgs, extension, unpacker, err := DetectCompression(file)
	if err != nil {
		return err
	}

	command := ""
	args := []string{}
	var allowedCmds []string
	var reader io.Reader
	if strings.HasPrefix(extension, ".tar") {
		command = "tar"
		// We can't create char/block devices in unpriv containers so avoid extracting them.
		args = append(args, "--wildcards")
		args = append(args, "--exclude=dev/*")
		args = append(args, "--exclude=./dev/*")
		args = append(args, "--exclude=rootfs/dev/*")
		args = append(args, "--exclude=rootfs/./dev/*")

		args = append(args, "--restrict", "--force-local")
		args = append(args, "-C", path, "--numeric-owner", "--xattrs-include=*")
		args = append(args, extractArgs...)
		args = append(args, "-")

		f, err := os.Open(file)
		if err != nil {
			return err
		}

		defer func() { _ = f.Close() }()

		reader = f

		// Attach the ProgressTracker if supplied.
		if tracker != nil {
			fsinfo, err := f.Stat()
			if err != nil {
				return err
			}

			tracker.Length = fsinfo.Size()
			reader = &ioprogress.ProgressReader{
				ReadCloser: f,
				Tracker:    tracker,
			}
		}

		// Allow supplementary commands for the unpacker to use.
		if len(unpacker) > 0 {
			allowedCmds = append(allowedCmds, unpacker[0])
		}
	} else if strings.HasPrefix(extension, ".squashfs") {
		// unsquashfs does not support reading from stdin,
		// so ProgressTracker is not possible.
		command = "unsquashfs"
		args = append(args, "-f", "-d", path, "-n")

		if maxMemory != 0 {
			// If maximum memory consumption is less than 256MiB, restrict unsquashfs and limit to a single thread.
			mem := maxMemory / 1024 / 1024
			if err == nil && mem < 256 {
				args = append(args, "-da", fmt.Sprintf("%d", mem), "-fr", fmt.Sprintf("%d", mem), "-p", "1")
			}
		}

		args = append(args, file)
	} else {
		return fmt.Errorf("Unsupported image format: %s", extension)
	}

	outputDir, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("Error opening directory: %w", err)
	}

	defer func() { _ = outputDir.Close() }()

	var readCloser io.ReadCloser
	if reader != nil {
		readCloser = io.NopCloser(reader)
	}

	err = ExtractWithFds(command, args, allowedCmds, readCloser, outputDir)
	if err != nil {
		// We can't create char/block devices in unpriv containers so ignore related errors.
		if command == "unsquashfs" {
			runError, ok := err.(subprocess.RunError)
			if !ok {
				return err
			}

			stdErr := runError.StdErr().String()
			if stdErr == "" {
				return err
			}

			// Confirm that all errors are related to character or block devices.
			found := false
			for _, line := range strings.Split(stdErr, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}

				if strings.Contains(line, "failed to create block device") {
					continue
				}

				if strings.Contains(line, "failed to create character device") {
					continue
				}

				// We found an actual error.
				found = true
			}

			if !found {
				// All good, assume everything unpacked.
				return nil
			}
		}

		// Check if we ran out of space
		fs := unix.Statfs_t{}

		err1 := unix.Statfs(path, &fs)
		if err1 != nil {
			return err1
		}

		// Check if we're running out of space
		if int64(fs.Bfree) < 10 {
			if blockBackend {
				return fmt.Errorf("Unable to unpack image, run out of disk space (consider increasing your pool's volume.size)")
			}

			return fmt.Errorf("Unable to unpack image, run out of disk space")
		}

		logger.Warn("Unpack failed", logger.Ctx{"file": file, "allowedCmds": allowedCmds, "extension": extension, "path": path, "err": err})
		return fmt.Errorf("Unpack failed: %w", err)
	}

	return nil
}
