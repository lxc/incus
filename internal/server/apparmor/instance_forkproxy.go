package apparmor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/sys"
	localUtil "github.com/lxc/incus/v7/internal/server/util"
	internalUtil "github.com/lxc/incus/v7/internal/util"
	"github.com/lxc/incus/v7/shared/util"
)

// Internal copy of the device interface.
type device interface {
	Config() deviceConfig.Device
	Name() string
}

// forkproxyProfile generates the AppArmor profile template from the given network.
func forkproxyProfile(sysOS *sys.OS, inst instance, dev device) (string, error) {
	// Add any socket used by forkproxy. Only the host side is a path on this host; resolve it there,
	// and leave the instance side as configured since it's resolved in the instance's own namespace.
	sockets := []string{}

	hostField := dev.Config()["connect"]
	instField := dev.Config()["listen"]
	bindHost := dev.Config()["bind"] == "" || dev.Config()["bind"] == "host"
	if bindHost {
		hostField = dev.Config()["listen"]
		instField = dev.Config()["connect"]
	}

	hostFields := strings.SplitN(hostField, ":", 2)
	if len(hostFields) == 2 && hostFields[0] == "unix" && !strings.HasPrefix(hostFields[1], "@") {
		if bindHost {
			// forkproxy removes and re-binds a host listen socket, so a link at it is never followed; resolve only its directory.
			dir, err := filepath.EvalSymlinks(filepath.Dir(hostFields[1]))
			if err != nil {
				return "", err
			}

			sockets = append(sockets, filepath.Join(dir, filepath.Base(hostFields[1])))
		} else if util.PathExists(hostFields[1]) {
			v, err := filepath.EvalSymlinks(hostFields[1])
			if err != nil {
				return "", err
			}

			sockets = append(sockets, v)
		} else {
			sockets = append(sockets, hostFields[1])
		}
	}

	instFields := strings.SplitN(instField, ":", 2)
	if len(instFields) == 2 && instFields[0] == "unix" && !strings.HasPrefix(instFields[1], "@") {
		if !slices.Contains(sockets, instFields[1]) {
			sockets = append(sockets, instFields[1])
		}
	}

	// A path with AppArmor metacharacters would break out of its rule even when quoted.
	for _, socket := range sockets {
		if strings.ContainsFunc(socket, unicode.IsControl) || strings.ContainsAny(socket, "\"\\*?[]{}") {
			return "", fmt.Errorf("Socket path %q can't be used in an AppArmor profile", socket)
		}
	}

	execPath := localUtil.GetExecPath()
	execPathFull, err := filepath.EvalSymlinks(execPath)
	if err == nil {
		execPath = execPathFull
	}

	// Render the profile.
	sb := &strings.Builder{}
	err = forkproxyProfileTpl.Execute(sb, map[string]any{
		"name":        ForkproxyProfileName(inst, dev),
		"varPath":     internalUtil.VarPath(""),
		"exePath":     execPath,
		"logPath":     inst.LogPath(),
		"libraryPath": libraryPaths(),
		"sockets":     sockets,
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}

// ForkproxyProfileName returns the AppArmor profile name.
func ForkproxyProfileName(inst instance, dev device) string {
	path := internalUtil.VarPath("")
	name := fmt.Sprintf("%s_%s_<%s>", dev.Name(), project.Instance(inst.Project().Name, inst.Name()), path)
	return profileName("forkproxy", name)
}

// forkproxyProfileFilename returns the name of the on-disk profile name.
func forkproxyProfileFilename(inst instance, dev device) string {
	name := fmt.Sprintf("%s_%s", dev.Name(), project.Instance(inst.Project().Name, inst.Name()))
	return profileName("forkproxy", name)
}

// ForkproxyLoad ensures that the instances's policy is loaded into the kernel so the it can boot.
func ForkproxyLoad(sysOS *sys.OS, inst instance, dev device) error {
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
	profile := filepath.Join(aaPath, "profiles", forkproxyProfileFilename(inst, dev))
	content, err := os.ReadFile(profile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	updated, err := forkproxyProfile(sysOS, inst, dev)
	if err != nil {
		return err
	}

	if string(content) != string(updated) {
		err = os.WriteFile(profile, []byte(updated), 0o600)
		if err != nil {
			return err
		}
	}

	err = loadProfile(sysOS, forkproxyProfileFilename(inst, dev))
	if err != nil {
		return err
	}

	return nil
}

// ForkproxyUnload ensures that the instances's policy namespace is unloaded to free kernel memory.
// This does not delete the policy from disk or cache.
func ForkproxyUnload(sysOS *sys.OS, inst instance, dev device) error {
	return unloadProfile(sysOS, ForkproxyProfileName(inst, dev), forkproxyProfileFilename(inst, dev))
}

// ForkproxyDelete removes the policy from cache/disk.
func ForkproxyDelete(sysOS *sys.OS, inst instance, dev device) error {
	return deleteProfile(sysOS, ForkproxyProfileName(inst, dev), forkproxyProfileFilename(inst, dev))
}
