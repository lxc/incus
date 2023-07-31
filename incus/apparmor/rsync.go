package apparmor

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/pborman/uuid"

	"github.com/cyphar/incus/lxd/revert"
	"github.com/cyphar/incus/lxd/sys"
	"github.com/cyphar/incus/shared"
)

var rsyncProfileTpl = template.Must(template.New("rsyncProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  capability chown,
  capability dac_override,
  capability dac_read_search,
  capability fowner,
  capability fsetid,
  capability mknod,
  capability setfcap,

  unix (connect, send, receive) type=stream,

  @{PROC}/@{pid}/cmdline r,
  @{PROC}/@{pid}/cpuset r,
  {{ .rootPath }}/{etc,lib,usr/lib}/os-release r,

  {{ .logPath }}/*/netcat.log rw,

  {{ .rootPath }}/run/{resolvconf,NetworkManager,systemd/resolve,connman,netconfig}/resolv.conf r,
  {{ .rootPath }}/run/systemd/resolve/stub-resolv.conf r,

{{- if .sourcePath }}
  {{ .sourcePath }}/** r,
  {{ .sourcePath }}/ r,
{{- end }}

{{- if .dstPath }}
  {{ .dstPath }}/** rwkl,
  {{ .dstPath }}/ rwkl,
{{- end }}

{{- if .snap }}
  /snap/lxd/*/bin/rsync mrix,

  # Snap-specific libraries
  /snap/lxd/*/lib/**.so* mr,
{{- end }}

  {{ .execPath }} mixr,

{{if .libraryPath -}}
  # Entries from LD_LIBRARY_PATH
{{range $index, $element := .libraryPath}}
  {{$element}}/** mr,
{{- end }}
{{- end }}

  # Silence denials on files that aren't required.
  deny {{ .rootPath }}/etc/ssl/openssl.cnf r,
  deny /sys/devices/virtual/dmi/id/product_uuid r,
  deny /sys/kernel/mm/transparent_hugepage/hpage_pmd_size r,
}
`))

// RsyncWrapper is used as a RunWrapper in the rsync package.
func RsyncWrapper(sysOS *sys.OS, cmd *exec.Cmd, sourcePath string, dstPath string) (func(), error) {
	if !sysOS.AppArmorAvailable {
		return func() {}, nil
	}

	revert := revert.New()
	defer revert.Fail()

	// Attempt to deref all paths.
	if sourcePath != "" {
		fullPath, err := filepath.EvalSymlinks(sourcePath)
		if err == nil {
			sourcePath = fullPath
		}
	}

	if dstPath != "" {
		fullPath, err := filepath.EvalSymlinks(dstPath)
		if err == nil {
			dstPath = fullPath
		}
	}

	// Load the profile.
	profileName, err := rsyncProfileLoad(sysOS, sourcePath, dstPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to load rsync profile: %w", err)
	}

	revert.Add(func() { _ = deleteProfile(sysOS, profileName, profileName) })

	// Resolve aa-exec.
	execPath, err := exec.LookPath("aa-exec")
	if err != nil {
		return nil, err
	}

	// Override the command.
	newArgs := []string{"aa-exec", "-p", profileName}
	newArgs = append(newArgs, cmd.Args...)
	cmd.Args = newArgs
	cmd.Path = execPath

	// All done, setup a cleanup function and disarm reverter.
	cleanup := func() {
		_ = deleteProfile(sysOS, profileName, profileName)
	}

	revert.Success()

	return cleanup, nil
}

func rsyncProfileLoad(sysOS *sys.OS, sourcePath string, dstPath string) (string, error) {
	revert := revert.New()
	defer revert.Fail()

	// Generate a temporary profile name.
	name := profileName("rsync", uuid.New())
	profilePath := filepath.Join(aaPath, "profiles", name)

	// Generate the profile
	content, err := rsyncProfile(sysOS, name, sourcePath, dstPath)
	if err != nil {
		return "", err
	}

	// Write it to disk.
	err = ioutil.WriteFile(profilePath, []byte(content), 0600)
	if err != nil {
		return "", err
	}

	revert.Add(func() { os.Remove(profilePath) })

	// Load it.
	err = loadProfile(sysOS, name)
	if err != nil {
		return "", err
	}

	revert.Success()
	return name, nil
}

// rsyncProfile generates the AppArmor profile template from the given destination path.
func rsyncProfile(sysOS *sys.OS, name string, sourcePath string, dstPath string) (string, error) {
	// Render the profile.
	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	logPath := shared.LogPath("")

	// Fully deref the executable path.
	execPath := sysOS.ExecPath
	fullPath, err := filepath.EvalSymlinks(execPath)
	if err == nil {
		execPath = fullPath
	}

	var sb *strings.Builder = &strings.Builder{}
	err = rsyncProfileTpl.Execute(sb, map[string]any{
		"name":        name,
		"execPath":    execPath,
		"sourcePath":  sourcePath,
		"dstPath":     dstPath,
		"snap":        shared.InSnap(),
		"rootPath":    rootPath,
		"logPath":     logPath,
		"libraryPath": strings.Split(os.Getenv("LD_LIBRARY_PATH"), ":"),
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
