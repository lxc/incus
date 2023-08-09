package apparmor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/lxc/incus/incusd/sys"
	"github.com/lxc/incus/incusd/util"
	"github.com/lxc/incus/shared"
)

var forkdnsProfileTpl = template.Must(template.New("forkdnsProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  # Capabilities
  capability net_bind_service,

  # Network access
  network inet dgram,
  network inet6 dgram,

  # Network-specific paths
  {{ .varPath }}/networks/{{ .networkName }}/dnsmasq.leases r,
  {{ .varPath }}/networks/{{ .networkName }}/forkdns.servers/servers.conf r,

  # Needed for lxd fork commands
  @{PROC}/@{pid}/cpuset r,
  {{ .exePath }} mr,
  @{PROC}/@{pid}/cmdline r,
  /{etc,lib,usr/lib}/os-release r,
  /run/systemd/resolve/stub-resolv.conf r,

  # Things that we definitely don't need
  deny @{PROC}/@{pid}/cgroup r,
  deny /sys/module/apparmor/parameters/enabled r,
  deny /sys/kernel/mm/transparent_hugepage/hpage_pmd_size r,

{{if .libraryPath -}}
  # Entries from LD_LIBRARY_PATH
{{range $index, $element := .libraryPath}}
  {{$element}}/** mr,
{{- end }}
{{- end }}
}
`))

// forkdnsProfile generates the AppArmor profile template from the given network.
func forkdnsProfile(sysOS *sys.OS, n network) (string, error) {
	// Deref paths.
	execPath := util.GetExecPath()
	execPathFull, err := filepath.EvalSymlinks(execPath)
	if err == nil {
		execPath = execPathFull
	}

	// Render the profile.
	var sb *strings.Builder = &strings.Builder{}
	err = forkdnsProfileTpl.Execute(sb, map[string]any{
		"name":        ForkdnsProfileName(n),
		"networkName": n.Name(),
		"varPath":     shared.VarPath(""),
		"libraryPath": strings.Split(os.Getenv("LD_LIBRARY_PATH"), ":"),
		"exePath":     execPath,
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}

// ForkdnsProfileName returns the AppArmor profile name.
func ForkdnsProfileName(n network) string {
	path := shared.VarPath("")
	name := fmt.Sprintf("%s_<%s>", n.Name(), path)
	return profileName("forkdns", name)
}

// forkdnsProfileFilename returns the name of the on-disk profile name.
func forkdnsProfileFilename(n network) string {
	return profileName("forkdns", n.Name())
}
