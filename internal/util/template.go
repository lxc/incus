package util

import (
	"strings"

	"github.com/flosch/pongo2"
)

// RenderTemplate renders a pongo2 template.
func RenderTemplate(template string, ctx pongo2.Context) (string, error) {
	// Load template from string
	tpl, err := pongo2.FromString("{% autoescape off %}" + template + "{% endautoescape %}")
	if err != nil {
		return "", err
	}

	// Get rendered template
	ret, err := tpl.Execute(ctx)
	if err != nil {
		return ret, err
	}

	// Looks like we're nesting templates so run pongo again
	if strings.Contains(ret, "{{") || strings.Contains(ret, "{%") {
		return RenderTemplate(ret, ctx)
	}

	return ret, err
}
