package apparmor

import (
	"text/template"
)

var forkproxyProfileTpl = template.Must(template.New("forkproxyProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  # Capabilities
  capability chown,
  capability dac_read_search,
  capability dac_override,
  capability fowner,
  capability fsetid,
  capability kill,
  capability net_bind_service,
  capability setgid,
  capability setuid,
  capability sys_admin,
  capability sys_chroot,
  capability sys_ptrace,

  # Network access
  network inet dgram,
  network inet6 dgram,
  network inet stream,
  network inet6 stream,
  network unix stream,

  # Forkproxy operation
  {{ .logPath }}/** rw,
  @{PROC}/** rw,
  / rw,
  ptrace (read),
  ptrace (trace),

  /etc/machine-id r,
  /run/systemd/resolve/stub-resolv.conf r,
  /run/{resolvconf,NetworkManager,systemd/resolve,connman,netconfig}/resolv.conf r,
  /usr/lib/systemd/resolv.conf r,

  # Allow /dev/shm access (for Wayland)
  /dev/shm/** rwkl,

  # Needed for the fork sub-commands
  {{ .exePath }} mr,
  @{PROC}/@{pid}/cmdline r,
  /{etc,lib,usr/lib}/os-release r,
{{if .sockets -}}
{{range $index, $element := .sockets}}
  {{$element}} rw,
{{- end }}
{{- end }}

  # Things that we definitely don't need
  deny @{PROC}/@{pid}/cgroup r,
  deny /sys/module/apparmor/parameters/enabled r,
  deny /sys/kernel/mm/transparent_hugepage/hpage_pmd_size r,
  deny /sys/devices/virtual/dmi/id/product_uuid r,

{{if .libraryPath }}
  # Entries from LD_LIBRARY_PATH
{{range $index, $element := .libraryPath}}
  {{$element}}/** mr,
{{- end }}
{{- end }}
}
`))
