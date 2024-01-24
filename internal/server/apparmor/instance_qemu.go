package apparmor

import (
	"text/template"
)

var qemuProfileTpl = template.Must(template.New("qemuProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>
  #include <abstractions/consoles>
  #include <abstractions/nameservice>

  capability dac_override,
  capability dac_read_search,
  capability ipc_lock,
  capability setgid,
  capability setuid,
  capability sys_chroot,
  capability sys_resource,

  # Needed by qemu
  /dev/hugepages/**                         rw,
  /dev/kvm                                  rw,
  /dev/net/tun                              rw,
  /dev/ptmx                                 rw,
  /dev/sev                                  rw,
  /dev/vfio/**                              rw,
  /dev/vhost-net                            rw,
  /dev/vhost-vsock                          rw,
  /etc/ceph/**                              r,
  /etc/machine-id                           r,
  /run/udev/data/*                          r,
  /sys/bus/                                 r,
  /sys/bus/nd/devices/                      r,
  /sys/bus/usb/devices/                     r,
  /sys/bus/usb/devices/**                   r,
  /sys/class/                               r,
  /sys/devices/**                           r,
  /sys/module/vhost/**                      r,
  /tmp/incus_sev_*                          r,
  {{ .ovmfPath }}/**                        kr,
  /usr/share/qemu/**                        kr,
  /usr/share/seabios/**                     kr,
  owner @{PROC}/@{pid}/cpuset               r,
  owner @{PROC}/@{pid}/task/@{tid}/comm     rw,
  /etc/nsswitch.conf         r,
  /etc/passwd                r,
  /etc/group                 r,
  @{PROC}/version                           r,

  # Extra binaries
{{- range $index, $element := .extra_binaries }}
{{ $element }}                               mrix,
{{- end }}

  # Used by qemu for live migration NBD server and client
  unix (bind, listen, accept, send, receive, connect) type=stream,

  # Instance specific paths
  {{ .logPath }}/** rwk,
  {{ .runPath }}/** rwk,
  {{ .path }}/** rwk,
  {{ .devicesPath }}/** rwk,

  # Needed for the fork sub-commands
  {{ .exePath }} mr,
  @{PROC}/@{pid}/cmdline r,
  /{etc,lib,usr/lib}/os-release r,

  # Things that we definitely don't need
  deny @{PROC}/@{pid}/cgroup r,
  deny /sys/module/apparmor/parameters/enabled r,
  deny /sys/kernel/mm/transparent_hugepage/hpage_pmd_size r,

{{if .agentPath -}}
  {{ .agentPath }}/ r,
  {{ .agentPath }}/* r,
{{- end }}

{{if .libraryPath -}}
  # Entries from LD_LIBRARY_PATH
{{range $index, $element := .libraryPath}}
  {{$element}}/** mr,
{{- end }}
{{- end }}

{{- if .raw }}

  ### Configuration: raw.apparmor
{{ .raw }}
{{- end }}
}
`))
