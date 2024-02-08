package apparmor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/internal/server/cgroup"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/sys"
	localUtil "github.com/lxc/incus/internal/server/util"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/util"
)

// Internal copy of the instance interface.
type instance interface {
	Project() api.Project
	Name() string
	ExpandedConfig() map[string]string
	Type() instancetype.Type
	LogPath() string
	RunPath() string
	Path() string
	DevicesPath() string
}

// InstanceProfileName returns the instance's AppArmor profile name.
func InstanceProfileName(inst instance) string {
	path := internalUtil.VarPath("")
	name := fmt.Sprintf("%s_<%s>", project.Instance(inst.Project().Name, inst.Name()), path)
	return profileName("", name)
}

// InstanceNamespaceName returns the instance's AppArmor namespace.
func InstanceNamespaceName(inst instance) string {
	// Unlike in profile names, / isn't an allowed character so replace with a -.
	path := strings.Replace(strings.Trim(internalUtil.VarPath(""), "/"), "/", "-", -1)
	name := fmt.Sprintf("%s_<%s>", project.Instance(inst.Project().Name, inst.Name()), path)
	return profileName("", name)
}

// instanceProfileFilename returns the name of the on-disk profile name.
func instanceProfileFilename(inst instance) string {
	name := project.Instance(inst.Project().Name, inst.Name())
	return profileName("", name)
}

// InstanceLoad ensures that the instances's policy is loaded into the kernel so the it can boot.
func InstanceLoad(sysOS *sys.OS, inst instance, extraBinaries []string) error {
	if inst.Type() == instancetype.Container {
		err := createNamespace(sysOS, InstanceNamespaceName(inst))
		if err != nil {
			return err
		}
	}

	err := instanceProfileGenerate(sysOS, inst, extraBinaries)
	if err != nil {
		return err
	}

	err = loadProfile(sysOS, instanceProfileFilename(inst))
	if err != nil {
		return err
	}

	return nil
}

// InstanceUnload ensures that the instances's policy namespace is unloaded to free kernel memory.
// This does not delete the policy from disk or cache.
func InstanceUnload(sysOS *sys.OS, inst instance) error {
	if inst.Type() == instancetype.Container {
		err := deleteNamespace(sysOS, InstanceNamespaceName(inst))
		if err != nil {
			return err
		}
	}

	err := unloadProfile(sysOS, InstanceProfileName(inst), instanceProfileFilename(inst))
	if err != nil {
		return err
	}

	return nil
}

// InstanceValidate generates the instance profile file and validates it.
func InstanceValidate(sysOS *sys.OS, inst instance, extraBinaries []string) error {
	err := instanceProfileGenerate(sysOS, inst, extraBinaries)
	if err != nil {
		return err
	}

	return parseProfile(sysOS, instanceProfileFilename(inst))
}

// InstanceDelete removes the policy from cache/disk.
func InstanceDelete(sysOS *sys.OS, inst instance) error {
	return deleteProfile(sysOS, InstanceProfileName(inst), instanceProfileFilename(inst))
}

// instanceProfileGenerate generates instance apparmor profile policy file.
func instanceProfileGenerate(sysOS *sys.OS, inst instance, extraBinaries []string) error {
	/* In order to avoid forcing a profile parse (potentially slow) on
	 * every container start, let's use AppArmor's binary policy cache,
	 * which checks mtime of the files to figure out if the policy needs to
	 * be regenerated.
	 *
	 * Since it uses mtimes, we shouldn't just always write out our local
	 * AppArmor template; instead we should check to see whether the
	 * template is the same as ours. If it isn't we should write our
	 * version out so that the new changes are reflected and we definitely
	 * force a recompile.
	 */
	profile := filepath.Join(aaPath, "profiles", instanceProfileFilename(inst))
	content, err := os.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	updated, err := instanceProfile(sysOS, inst, extraBinaries)
	if err != nil {
		return err
	}

	if string(content) != string(updated) {
		err = os.WriteFile(profile, []byte(updated), 0600)
		if err != nil {
			return err
		}
	}

	return nil
}

// instanceProfile generates the AppArmor profile template from the given instance.
func instanceProfile(sysOS *sys.OS, inst instance, extraBinaries []string) (string, error) {
	// Prepare raw.apparmor.
	rawContent := ""
	rawApparmor, ok := inst.ExpandedConfig()["raw.apparmor"]
	if ok {
		for _, line := range strings.Split(strings.Trim(rawApparmor, "\n"), "\n") {
			rawContent += fmt.Sprintf("  %s\n", line)
		}
	}

	// Check for features.
	unixSupported, err := parserSupports(sysOS, "unix")
	if err != nil {
		return "", err
	}

	// Deref the extra binaries.
	for i, entry := range extraBinaries {
		fullPath, err := filepath.EvalSymlinks(entry)
		if err != nil {
			continue
		}

		extraBinaries[i] = fullPath
	}

	// Render the profile.
	var sb *strings.Builder = &strings.Builder{}
	if inst.Type() == instancetype.Container {
		err = lxcProfileTpl.Execute(sb, map[string]any{
			"extra_binaries":   extraBinaries,
			"feature_cgns":     sysOS.CGInfo.Namespacing,
			"feature_cgroup2":  sysOS.CGInfo.Layout == cgroup.CgroupsUnified || sysOS.CGInfo.Layout == cgroup.CgroupsHybrid,
			"feature_stacking": sysOS.AppArmorStacking && !sysOS.AppArmorStacked,
			"feature_unix":     unixSupported,
			"kernel_binfmt":    util.IsFalseOrEmpty(inst.ExpandedConfig()["security.privileged"]) && sysOS.UnprivBinfmt,
			"name":             InstanceProfileName(inst),
			"namespace":        InstanceNamespaceName(inst),
			"nesting":          util.IsTrue(inst.ExpandedConfig()["security.nesting"]),
			"raw":              rawContent,
			"unprivileged":     util.IsFalseOrEmpty(inst.ExpandedConfig()["security.privileged"]) || sysOS.RunningInUserNS,
		})
		if err != nil {
			return "", err
		}
	} else {
		// AppArmor requires deref of all paths.
		path, err := filepath.EvalSymlinks(inst.Path())
		if err != nil {
			return "", err
		}

		ovmfPath := "/usr/share/OVMF"
		if os.Getenv("INCUS_OVMF_PATH") != "" {
			ovmfPath = os.Getenv("INCUS_OVMF_PATH")
		}

		ovmfPath, err = filepath.EvalSymlinks(ovmfPath)
		if err != nil {
			return "", err
		}

		agentPath := ""
		if os.Getenv("INCUS_AGENT_PATH") != "" {
			agentPath, err = filepath.EvalSymlinks(os.Getenv("INCUS_AGENT_PATH"))
			if err != nil {
				return "", err
			}
		}

		execPath := localUtil.GetExecPath()
		execPathFull, err := filepath.EvalSymlinks(execPath)
		if err == nil {
			execPath = execPathFull
		}

		err = qemuProfileTpl.Execute(sb, map[string]any{
			"devicesPath":    inst.DevicesPath(),
			"exePath":        execPath,
			"extra_binaries": extraBinaries,
			"libraryPath":    strings.Split(os.Getenv("LD_LIBRARY_PATH"), ":"),
			"logPath":        inst.LogPath(),
			"runPath":        inst.RunPath(),
			"name":           InstanceProfileName(inst),
			"path":           path,
			"raw":            rawContent,
			"ovmfPath":       ovmfPath,
			"agentPath":      agentPath,
		})
		if err != nil {
			return "", err
		}
	}

	return sb.String(), nil
}
