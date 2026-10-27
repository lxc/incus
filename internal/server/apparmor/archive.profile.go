package apparmor

import (
	"text/template"
)

var archiveProfileTpl = template.Must(template.New("archiveProfile").Parse(`#include <tunables/global>
profile "{{.name}}" {
  #include <abstractions/base>
  #include <abstractions/nameservice>

{{range $index, $element := .allowedCommandPaths}}
  {{$element}} mixr,
{{- end }}

  {{ .outputPath }}/ rw,
  {{ .outputPath }}/** rwl,
  {{ .backupsPath }}/** rw,
  {{ .imagesPath }}/** r,

  signal (receive) set=("term"),

  # Capabilities
  capability chown,
  capability dac_override,
  capability dac_read_search,
  capability fowner,
  capability fsetid,
  capability mknod,
  capability setfcap,
}
`))
