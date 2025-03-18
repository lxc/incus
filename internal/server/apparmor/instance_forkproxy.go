package apparmor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/sys"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/util"
)

// Internal copy of the device interface.
type device interface {
	Config() deviceConfig.Device
	Name() string
}

// forkproxyProfile generates the AppArmor profile template from the given network.
func forkproxyProfile(sysOS *sys.OS, inst instance, dev device) (string, error) {
	// Add any socket used by forkproxy.
	sockets := []string{}

	fields := strings.SplitN(dev.Config()["listen"], ":", 2)
	if fields[0] == "unix" && !strings.HasPrefix(fields[1], "@") {
		sockets = append(sockets, fields[1])
	}

	fields = strings.SplitN(dev.Config()["connect"], ":", 2)
	if fields[0] == "unix" && !strings.HasPrefix(fields[1], "@") {
		sockets = append(sockets, fields[1])
	}

	// AppArmor requires deref of all paths.
	for k := range sockets {
		// Skip non-existing because of the additional entry for the host side.
		if !util.PathExists(sockets[k]) {
			continue
		}

		v, err := filepath.EvalSymlinks(sockets[k])
		if err != nil {
			return "", err
		}

		if !slices.Contains(sockets, v) {
			sockets = append(sockets, v)
		}
	}

	execPath := localUtil.GetExecPath()
	execPathFull, err := filepath.EvalSymlinks(execPath)
	if err == nil {
		execPath = execPathFull
	}

	// Render the profile.
	var sb *strings.Builder = &strings.Builder{}
	err = forkproxyProfileTpl.Execute(sb, map[string]any{
		"name":        ForkproxyProfileName(inst, dev),
		"varPath":     internalUtil.VarPath(""),
		"exePath":     execPath,
		"logPath":     inst.LogPath(),
		"libraryPath": strings.Split(os.Getenv("LD_LIBRARY_PATH"), ":"),
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
