package apparmor

import (
	"fmt"
	"strings"

	"github.com/lxc/incus/v6/internal/server/sys"
	internalUtil "github.com/lxc/incus/v6/internal/util"
)

// dnsmasqProfile generates the AppArmor profile template from the given network.
func dnsmasqProfile(sysOS *sys.OS, n network) (string, error) {
	// Render the profile.
	var sb *strings.Builder = &strings.Builder{}
	err := dnsmasqProfileTpl.Execute(sb, map[string]any{
		"name":        DnsmasqProfileName(n),
		"networkName": n.Name(),
		"logPath":     internalUtil.LogPath(""),
		"varPath":     internalUtil.VarPath(""),
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}

// DnsmasqProfileName returns the AppArmor profile name.
func DnsmasqProfileName(n network) string {
	path := internalUtil.VarPath("")
	name := fmt.Sprintf("%s_<%s>", n.Name(), path)
	return profileName("dnsmasq", name)
}

// dnsmasqProfileFilename returns the name of the on-disk profile name.
func dnsmasqProfileFilename(n network) string {
	return profileName("dnsmasq", n.Name())
}
