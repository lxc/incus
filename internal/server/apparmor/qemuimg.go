package apparmor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/server/sys"
	"github.com/lxc/incus/v6/shared/ioprogress"
	"github.com/lxc/incus/v6/shared/subprocess"
)

type nullWriteCloser struct {
	io.Writer
}

func (nwc *nullWriteCloser) Close() error {
	return nil
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(b []byte) (n int, err error) {
	return w(b)
}

func handleWriter(out io.Writer, hand func(int64, int64)) io.Writer {
	var current int64
	return writerFunc(func(b []byte) (int, error) {
		n, _ := out.Write(b)
		ss := strings.Split(strings.Trim(string(b), "(%) \t\n\v\f\r"), "/")
		f, err := strconv.ParseFloat(ss[0], 64)
		if err != nil {
			return n, nil
		}

		percent := int64(f)
		if percent != current {
			current = percent
			hand(percent, 0)
		}

		return n, nil
	})
}

// QemuImg runs qemu-img with an AppArmor profile based on the imgPath and dstPath supplied.
// The first element of the cmd slice is expected to be a priority limiting command (such as nice or prlimit) and
// will be added as an allowed command to the AppArmor profile. The remaining elements of the cmd slice are
// expected to be the qemu-img command and its arguments.
func QemuImg(sysOS *sys.OS, cmd []string, imgPath string, dstPath string, tracker *ioprogress.ProgressTracker) (string, error) {
	// It is assumed that command starts with a program which sets resource limits, like prlimit or nice
	allowedCmds := []string{"qemu-img", cmd[0]}

	allowedCmdPaths := []string{}
	for _, c := range allowedCmds {
		cmdPath, err := exec.LookPath(c)
		if err != nil {
			return "", fmt.Errorf("Failed to find executable %q: %w", c, err)
		}

		allowedCmdPaths = append(allowedCmdPaths, cmdPath)
	}

	// Attempt to deref all paths.
	imgFullPath, err := filepath.EvalSymlinks(imgPath)
	if err == nil {
		imgPath = imgFullPath
	}

	if dstPath != "" {
		dstFullPath, err := filepath.EvalSymlinks(dstPath)
		if err == nil {
			dstPath = dstFullPath
		}
	}

	profileName, err := qemuImgProfileLoad(sysOS, imgPath, dstPath, allowedCmdPaths)
	if err != nil {
		return "", fmt.Errorf("Failed to load qemu-img profile: %w", err)
	}

	defer func() {
		_ = deleteProfile(sysOS, profileName, profileName)
	}()

	var buffer bytes.Buffer
	var output bytes.Buffer
	var writer io.Writer = &output
	if tracker != nil && tracker.Handler != nil {
		writer = handleWriter(&output, tracker.Handler)
	}

	p := subprocess.NewProcessWithFds(cmd[0], cmd[1:], nil, &nullWriteCloser{writer}, &nullWriteCloser{&buffer})
	p.SetApparmor(profileName)

	err = p.Start(context.Background())
	if err != nil {
		return "", fmt.Errorf("Failed running qemu-img: %w", err)
	}

	_, err = p.Wait(context.Background())
	if err != nil {
		return "", subprocess.NewRunError(cmd[0], cmd[1:], err, nil, &buffer)
	}

	return output.String(), nil
}

// qemuImgProfileLoad ensures that the qemu-img's policy is loaded into the kernel.
func qemuImgProfileLoad(sysOS *sys.OS, imgPath string, dstPath string, allowedCmdPaths []string) (string, error) {
	name := fmt.Sprintf("<%s>_<%s>", strings.ReplaceAll(strings.Trim(imgPath, "/"), "/", "-"), strings.ReplaceAll(strings.Trim(dstPath, "/"), "/", "-"))
	profileName := profileName("qemu-img", name)
	profilePath := filepath.Join(aaPath, "profiles", profileName)
	content, err := os.ReadFile(profilePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	updated, err := qemuImgProfile(profileName, imgPath, dstPath, allowedCmdPaths)
	if err != nil {
		return "", err
	}

	if string(content) != string(updated) {
		err = os.WriteFile(profilePath, []byte(updated), 0o600)
		if err != nil {
			return "", err
		}
	}

	err = loadProfile(sysOS, profileName)
	if err != nil {
		return "", err
	}

	return profileName, nil
}

// qemuImgProfile generates the AppArmor profile template from the given destination path.
func qemuImgProfile(profileName string, imgPath string, dstPath string, allowedCmdPaths []string) (string, error) {
	// Render the profile.
	var sb *strings.Builder = &strings.Builder{}
	err := qemuImgProfileTpl.Execute(sb, map[string]any{
		"name":            profileName,
		"pathToImg":       imgPath,
		"dstPath":         dstPath,
		"allowedCmdPaths": allowedCmdPaths,
		"libraryPath":     strings.Split(os.Getenv("LD_LIBRARY_PATH"), ":"),
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
