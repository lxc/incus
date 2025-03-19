//go:build linux

package version

import (
	"strings"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/shared/osarch"
)

func getPlatformVersionStrings() []string {
	versions := []string{}

	// Add kernel version.
	uname, err := linux.Uname()
	if err != nil {
		return versions
	}

	versions = append(versions, strings.Split(uname.Release, "-")[0])

	// Add distribution info.
	osRelease, err := osarch.GetOSRelease()
	if err == nil && osRelease["NAME"] != "" {
		versions = append(versions, osRelease["NAME"])

		if osRelease["VERSION_ID"] != "" {
			versions = append(versions, osRelease["VERSION_ID"])
		}
	}

	return versions
}
