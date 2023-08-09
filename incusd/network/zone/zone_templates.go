package zone

import (
	"text/template"
)

// DNS zone template.
var zoneTemplate = template.Must(template.New("zoneTemplate").Parse(`
{{.zone}}. 3600 IN SOA {{.zone}}. {{.primary}}. {{.serial}} 120 60 86400 30
{{- range $index, $element := .nameservers}}
{{$.zone}}. 300 IN NS {{$element}}.
{{- end}}
{{- range .records}}
{{.name}}.{{$.zone}}. {{.ttl}} IN {{.type}} {{.value}}
{{- end}}
{{.zone}}. 3600 IN SOA {{.zone}}. {{.primary}}. {{.serial}} 120 60 86400 30
`))
