package apparmor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/lxc/incus/v6/internal/server/sys"
	"github.com/lxc/incus/v6/shared/subprocess"
)

var qemuImgProfileTpl = template.Must(template.New("qemuImgProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  capability dac_override,
  capability dac_read_search,
  capability ipc_lock,

  /proc/sys/vm/max_map_count r,
  /sys/devices/**/block/*/queue/max_segments  r,
  /sys/devices/**/block/*/zoned  r,
  /sys/devices/system/node r,
  /sys/devices/system/node/** r,

{{range $index, $element := .allowedCmdPaths}}
  {{$element}} mixr,
{{- end }}

  {{ .pathToImg }} rk,

{{- if .dstPath }}
  {{ .dstPath }} rwk,
{{- end }}

{{if .libraryPath -}}
  # Entries from LD_LIBRARY_PATH
{{range $index, $element := .libraryPath}}
  {{$element}}/** mr,
{{- end }}
{{- end }}
}
`))

type nullWriteCloser struct {
	*bytes.Buffer
}

func (nwc *nullWriteCloser) Close() error {
	return nil
}

// QemuImg runs qemu-img with an AppArmor profile based on the imgPath and dstPath supplied.
// The first element of the cmd slice is expected to be a priority limiting command (such as nice or prlimit) and
// will be added as an allowed command to the AppArmor profile. The remaining elements of the cmd slice are
// expected to be the qemu-img command and its arguments.
func QemuImg(sysOS *sys.OS, cmd []string, imgPath string, dstPath string) (string, error) {
	//It is assumed that command starts with a program which sets resource limits, like prlimit or nice
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
	p := subprocess.NewProcessWithFds(cmd[0], cmd[1:], nil, &nullWriteCloser{&output}, &nullWriteCloser{&buffer})
	if err != nil {
		return "", fmt.Errorf("Failed creating qemu-img subprocess: %w", err)
	}

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
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	updated, err := qemuImgProfile(profileName, imgPath, dstPath, allowedCmdPaths)
	if err != nil {
		return "", err
	}

	if string(content) != string(updated) {
		err = os.WriteFile(profilePath, []byte(updated), 0600)
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
