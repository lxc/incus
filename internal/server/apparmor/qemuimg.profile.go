package apparmor

import (
	"text/template"
)

var qemuImgProfileTpl = template.Must(template.New("qemuImgProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  capability dac_override,
  capability dac_read_search,
  capability ipc_lock,

  /proc/sys/vm/max_map_count r,
  /sys/devices/**/block/*/queue/max_segments  r,
  /sys/devices/**/block/*/zoned  r,
  /sys/devices/system/node r,
  /sys/devices/system/node/** r,

{{range $index, $element := .allowedCmdPaths}}
  {{$element}} mixr,
{{- end }}

  {{ .pathToImg }} rk,

{{- if .dstPath }}
  {{ .dstPath }} rwk,
{{- end }}

{{if .libraryPath -}}
  # Entries from LD_LIBRARY_PATH
{{range $index, $element := .libraryPath}}
  {{$element}}/** mr,
{{- end }}
{{- end }}
}
`))
