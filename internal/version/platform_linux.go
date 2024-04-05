//go:build linux

package version

import (
	"os"
	"strings"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/util"
)

func getPlatformVersionStrings() []string {
	versions := []string{}

	// Add kernel version
	uname, err := linux.Uname()
	if err != nil {
		return versions
	}

	versions = append(versions, strings.Split(uname.Release, "-")[0])

	// Add distribution info
	lsbRelease, err := osarch.GetLSBRelease()
	if err == nil {
		for _, key := range []string{"NAME", "VERSION_ID"} {
			value, ok := lsbRelease[key]
			if ok {
				versions = append(versions, value)
			}
		}
	}

	// Add chromebook info
	if len(versions) == 1 && util.PathExists("/run/cros_milestone") {
		content, err := os.ReadFile("/run/cros_milestone")
		if err == nil {
			versions = append(versions, "Chrome OS")
			versions = append(versions, strings.TrimSpace(string(content)))
		}
	}

	return versions
}
