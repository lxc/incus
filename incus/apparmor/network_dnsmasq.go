package apparmor

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/cyphar/incus/incus/sys"
	"github.com/cyphar/incus/shared"
)

var dnsmasqProfileTpl = template.Must(template.New("dnsmasqProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>
  #include <abstractions/dbus>
  #include <abstractions/nameservice>

  # Capabilities
  capability chown,
  capability net_bind_service,
  capability setgid,
  capability setuid,
  capability dac_override,
  capability dac_read_search,
  capability net_admin,         # for DHCP server
  capability net_raw,           # for DHCP server ping checks

  # Network access
  network inet raw,
  network inet6 raw,

  # Network-specific paths
  {{ .varPath }}/networks/{{ .networkName }}/dnsmasq.hosts/{,*} r,
  {{ .varPath }}/networks/{{ .networkName }}/dnsmasq.leases rw,
  {{ .varPath }}/networks/{{ .networkName }}/dnsmasq.raw r,

  # Logging path
  {{ .logPath }}/dnsmasq.{{ .networkName }}.log rw,

  # Additional system files
  @{PROC}/sys/net/ipv6/conf/*/mtu r,
  @{PROC}/@{pid}/fd/ r,
  {{ .rootPath }}/etc/localtime  r,
  {{ .rootPath }}/usr/share/zoneinfo/**  r,

  # System configuration access
  {{ .rootPath }}/etc/gai.conf           r,
  {{ .rootPath }}/etc/group              r,
  {{ .rootPath }}/etc/host.conf          r,
  {{ .rootPath }}/etc/hosts              r,
  {{ .rootPath }}/etc/nsswitch.conf      r,
  {{ .rootPath }}/etc/passwd             r,
  {{ .rootPath }}/etc/protocols          r,

  {{ .rootPath }}/etc/resolv.conf        r,
  {{ .rootPath }}/etc/resolvconf/run/resolv.conf r,

  {{ .rootPath }}/run/{resolvconf,NetworkManager,systemd/resolve,connman,netconfig}/resolv.conf r,
  {{ .rootPath }}/run/systemd/resolve/stub-resolv.conf r,
  {{ .rootPath }}/mnt/wsl/resolv.conf r,

{{- if .snap }}

  # The binary itself (for nesting)
  /snap/lxd/*/bin/dnsmasq                 mr,

  # Snap-specific libraries
  /snap/lxd/*/lib/**.so*                  mr,
{{ else }}
  # The binary itself (for nesting)
  /{,usr/}sbin/dnsmasq                    mr,
{{- end }}
}
`))

// dnsmasqProfile generates the AppArmor profile template from the given network.
func dnsmasqProfile(sysOS *sys.OS, n network) (string, error) {
	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	// Render the profile.
	var sb *strings.Builder = &strings.Builder{}
	err := dnsmasqProfileTpl.Execute(sb, map[string]any{
		"name":        DnsmasqProfileName(n),
		"networkName": n.Name(),
		"logPath":     shared.LogPath(""),
		"varPath":     shared.VarPath(""),
		"rootPath":    rootPath,
		"snap":        shared.InSnap(),
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}

// DnsmasqProfileName returns the AppArmor profile name.
func DnsmasqProfileName(n network) string {
	path := shared.VarPath("")
	name := fmt.Sprintf("%s_<%s>", n.Name(), path)
	return profileName("dnsmasq", name)
}

// dnsmasqProfileFilename returns the name of the on-disk profile name.
func dnsmasqProfileFilename(n network) string {
	return profileName("dnsmasq", n.Name())
}
