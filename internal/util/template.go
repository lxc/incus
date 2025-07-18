package util

import (
	"errors"
	"strings"

	"github.com/flosch/pongo2/v6"
)

// RenderTemplate renders a pongo2 template with nesting support.
// This supports up to 3 levels of nesting (to avoid loops).
func RenderTemplate(template string, ctx pongo2.Context) (string, error) {
	// Limit recursion to 3 levels.
	for range 3 {
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

		// Check if another pass is needed.
		if !strings.Contains(ret, "{{") && !strings.Contains(ret, "{%") {
			return ret, nil
		}

		// Prepare for another run.
		template = ret
	}

	return "", errors.New("Maximum template recursion limit reached")
}
