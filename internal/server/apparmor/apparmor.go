package apparmor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/v6/internal/server/sys"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

const (
	cmdLoad   = "r"
	cmdUnload = "R"
	cmdParse  = "Q"
)

var (
	aaCacheDir string
	aaPath     = internalUtil.VarPath("security", "apparmor")
	aaVersion  *version.DottedVersion
)

// Init performs initial version and feature detection.
func Init() error {
	// Fill in aaVersion.
	out, err := subprocess.RunCommand("apparmor_parser", "--version")
	if err != nil {
		return err
	}

	fields := strings.Fields(strings.Split(out, "\n")[0])
	parsedVersion, err := version.Parse(fields[len(fields)-1])
	if err != nil {
		return err
	}

	aaVersion = parsedVersion

	// Fill in aaCacheDir.
	basePath := filepath.Join(aaPath, "cache")

	// Multiple policy cache directories were only added in v2.13.
	minVer, err := version.NewDottedVersion("2.13")
	if err != nil {
		return err
	}

	if aaVersion.Compare(minVer) < 0 {
		aaCacheDir = basePath
	} else {
		output, err := subprocess.RunCommand("apparmor_parser", "-L", basePath, "--print-cache-dir")
		if err != nil {
			return err
		}

		aaCacheDir = strings.TrimSpace(output)
	}

	return nil
}

// runApparmor runs the relevant AppArmor command.
func runApparmor(sysOS *sys.OS, command string, name string) error {
	if !sysOS.AppArmorAvailable {
		return nil
	}

	_, err := subprocess.RunCommand("apparmor_parser", []string{
		fmt.Sprintf("-%sWL", command),
		filepath.Join(aaPath, "cache"),
		filepath.Join(aaPath, "profiles", name),
	}...)
	if err != nil {
		return err
	}

	return nil
}

// createNamespace creates a new AppArmor namespace.
func createNamespace(sysOS *sys.OS, name string) error {
	if !sysOS.AppArmorAvailable {
		return nil
	}

	if !sysOS.AppArmorStacking || sysOS.AppArmorStacked {
		return nil
	}

	p := filepath.Join("/sys/kernel/security/apparmor/policy/namespaces", name)
	err := os.Mkdir(p, 0o755)
	if err != nil && !os.IsExist(err) {
		return err
	}

	return nil
}

// deleteNamespace destroys an AppArmor namespace.
func deleteNamespace(sysOS *sys.OS, name string) error {
	if !sysOS.AppArmorAvailable {
		return nil
	}

	if !sysOS.AppArmorStacking || sysOS.AppArmorStacked {
		return nil
	}

	p := filepath.Join("/sys/kernel/security/apparmor/policy/namespaces", name)
	err := os.Remove(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return nil
}

// hasProfile checks if the profile is already loaded.
func hasProfile(sysOS *sys.OS, name string) (bool, error) {
	mangled := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(name, "/", "."), "<", ""), ">", "")

	profilesPath := "/sys/kernel/security/apparmor/policy/profiles"
	if util.PathExists(profilesPath) {
		entries, err := os.ReadDir(profilesPath)
		if err != nil {
			return false, err
		}

		for _, entry := range entries {
			fields := strings.Split(entry.Name(), ".")
			if mangled == strings.Join(fields[0:len(fields)-1], ".") {
				return true, nil
			}
		}
	}

	return false, nil
}

// parseProfile parses the profile without loading it into the kernel.
func parseProfile(sysOS *sys.OS, name string) error {
	if !sysOS.AppArmorAvailable {
		return nil
	}

	return runApparmor(sysOS, cmdParse, name)
}

// loadProfile loads the AppArmor profile into the kernel.
func loadProfile(sysOS *sys.OS, name string) error {
	if !sysOS.AppArmorAdmin {
		return nil
	}

	return runApparmor(sysOS, cmdLoad, name)
}

// unloadProfile removes the profile from the kernel.
func unloadProfile(sysOS *sys.OS, fullName string, name string) error {
	if !sysOS.AppArmorAvailable {
		return nil
	}

	ok, err := hasProfile(sysOS, fullName)
	if err != nil {
		return err
	}

	if !ok {
		return nil
	}

	return runApparmor(sysOS, cmdUnload, name)
}

// deleteProfile unloads and delete profile and cache for a profile.
func deleteProfile(sysOS *sys.OS, fullName string, name string) error {
	if !sysOS.AppArmorAvailable || !sysOS.AppArmorAdmin {
		return nil
	}

	if aaCacheDir == "" {
		return errors.New("Couldn't identify AppArmor cache directory")
	}

	err := unloadProfile(sysOS, fullName, name)
	if err != nil {
		return err
	}

	err = os.Remove(filepath.Join(aaCacheDir, name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Failed to remove %s: %w", filepath.Join(aaCacheDir, name), err)
	}

	err = os.Remove(filepath.Join(aaPath, "profiles", name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Failed to remove %s: %w", filepath.Join(aaPath, "profiles", name), err)
	}

	return nil
}

// parserSupports checks if the parser supports a particular feature.
func parserSupports(sysOS *sys.OS, feature string) (bool, error) {
	if !sysOS.AppArmorAvailable {
		return false, nil
	}

	if aaVersion == nil {
		return false, errors.New("Couldn't identify AppArmor version")
	}

	if feature == "unix" {
		minVer, err := version.NewDottedVersion("2.10.95")
		if err != nil {
			return false, err
		}

		return aaVersion.Compare(minVer) >= 0, nil
	}

	if feature == "userns" {
		minVer, err := version.NewDottedVersion("4.0.0")
		if err != nil {
			return false, err
		}

		return aaVersion.Compare(minVer) >= 0, nil
	}

	return false, nil
}

// profileName handles generating valid profile names.
func profileName(prefix string, name string) string {
	separators := 1
	if len(prefix) > 0 {
		separators = 2
	}

	// Max length in AppArmor is 253 chars.
	if len(name)+len(prefix)+3+separators >= 253 {
		hash256 := sha256.New()
		_, _ = io.WriteString(hash256, name)
		name = fmt.Sprintf("%x", hash256.Sum(nil))
	}

	if len(prefix) > 0 {
		return fmt.Sprintf("incus_%s-%s", prefix, name)
	}

	return fmt.Sprintf("incus-%s", name)
}
