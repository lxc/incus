package apparmor

import (
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/lxc/incus/incus/sys"
	"github.com/lxc/incus/shared"
)

var archiveProfileTpl = template.Must(template.New("archiveProfile").Parse(`#include <tunables/global>
profile "{{.name}}" {
  #include <abstractions/base>
  #include <abstractions/nameservice>

{{range $index, $element := .allowedCommandPaths}}
  {{$element}} mixr,
{{- end }}

  {{ .outputPath }}/ rw,
  {{ .outputPath }}/** rwl,
  {{ .backupsPath }}/** rw,
  {{ .imagesPath }}/** r,

  signal (receive) set=("term") peer=unconfined,

  # Capabilities
  capability chown,
  capability dac_override,
  capability dac_read_search,
  capability fowner,
  capability fsetid,
  capability mknod,
  capability setfcap,
}
`))

// ArchiveLoad ensures that the archive's policy is loaded into the kernel.
func ArchiveLoad(sysOS *sys.OS, outputPath string, allowedCommandPaths []string) error {
	profile := filepath.Join(aaPath, "profiles", ArchiveProfileFilename(outputPath))
	content, err := os.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	updated, err := archiveProfile(outputPath, allowedCommandPaths)
	if err != nil {
		return err
	}

	if string(content) != string(updated) {
		err = os.WriteFile(profile, []byte(updated), 0600)
		if err != nil {
			return err
		}
	}

	err = loadProfile(sysOS, ArchiveProfileFilename(outputPath))
	if err != nil {
		return err
	}

	return nil
}

// ArchiveUnload ensures that the archive's policy namespace is unloaded to free kernel memory.
// This does not delete the policy from disk or cache.
func ArchiveUnload(sysOS *sys.OS, outputPath string) error {
	err := unloadProfile(sysOS, ArchiveProfileName(outputPath), ArchiveProfileFilename(outputPath))
	if err != nil {
		return err
	}

	return nil
}

// ArchiveDelete removes the profile from cache/disk.
func ArchiveDelete(sysOS *sys.OS, outputPath string) error {
	return deleteProfile(sysOS, ArchiveProfileName(outputPath), ArchiveProfileFilename(outputPath))
}

// archiveProfile generates the AppArmor profile template from the given destination path.
func archiveProfile(outputPath string, allowedCommandPaths []string) (string, error) {
	// Attempt to deref all paths.
	outputPathFull, err := filepath.EvalSymlinks(outputPath)
	if err != nil {
		outputPathFull = outputPath // Use requested path if cannot resolve it.
	}

	backupsPath := shared.VarPath("backups")
	backupsPathFull, err := filepath.EvalSymlinks(backupsPath)
	if err == nil {
		backupsPath = backupsPathFull
	}

	imagesPath := shared.VarPath("images")
	imagesPathFull, err := filepath.EvalSymlinks(imagesPath)
	if err == nil {
		imagesPath = imagesPathFull
	}

	derefCommandPaths := make([]string, len(allowedCommandPaths))
	for i, cmd := range allowedCommandPaths {
		cmdFull, err := filepath.EvalSymlinks(cmd)
		if err == nil {
			derefCommandPaths[i] = cmdFull
		} else {
			derefCommandPaths[i] = cmd
		}
	}

	// Render the profile.
	var sb *strings.Builder = &strings.Builder{}
	err = archiveProfileTpl.Execute(sb, map[string]any{
		"name":                ArchiveProfileName(outputPath), // Use non-deferenced outputPath for name.
		"outputPath":          outputPathFull,                 // Use deferenced path in AppArmor profile.
		"backupsPath":         backupsPath,
		"imagesPath":          imagesPath,
		"allowedCommandPaths": derefCommandPaths,
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}

// ArchiveProfileName returns the AppArmor profile name.
func ArchiveProfileName(outputPath string) string {
	name := strings.Replace(strings.Trim(outputPath, "/"), "/", "-", -1)
	return profileName("archive", name)
}

// ArchiveProfileFilename returns the name of the on-disk profile name.
func ArchiveProfileFilename(outputPath string) string {
	name := strings.Replace(strings.Trim(outputPath, "/"), "/", "-", -1)
	return profileName("archive", name)
}
