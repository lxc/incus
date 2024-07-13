package apparmor

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/lxc/incus/v6/internal/server/sys"
	internalUtil "github.com/lxc/incus/v6/internal/util"
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

  # Allow to restart dnsmasq
  signal (receive) set=("hup","kill"),

  # Logging path
  {{ .logPath }}/dnsmasq.{{ .networkName }}.log rw,

  # Additional system files
  @{PROC}/sys/net/ipv6/conf/*/mtu r,
  @{PROC}/@{pid}/fd/ r,
  /etc/localtime  r,
  /usr/share/zoneinfo/**  r,

  # System configuration access
  /etc/gai.conf           r,
  /etc/group              r,
  /etc/host.conf          r,
  /etc/hosts              r,
  /etc/nsswitch.conf      r,
  /etc/passwd             r,
  /etc/protocols          r,

  /etc/resolv.conf        r,
  /etc/resolvconf/run/resolv.conf r,

  /run/{resolvconf,NetworkManager,systemd/resolve,connman,netconfig}/resolv.conf r,
  /run/systemd/resolve/stub-resolv.conf r,
  /mnt/wsl/resolv.conf r,

  # The binary itself (for nesting)
  /{,usr/}sbin/dnsmasq                    mr,
}
`))

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
