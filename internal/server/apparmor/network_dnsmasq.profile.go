package apparmor

import (
	"text/template"
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
  network unix stream,
  network unix dgram,

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
