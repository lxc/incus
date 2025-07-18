package util

import (
	"errors"
	"fmt"
	"strings"

	"github.com/flosch/pongo2/v6"
)

// RenderTemplate renders a pongo2 template with nesting support.
// This supports up to 3 levels of nesting (to avoid loops).
func RenderTemplate(template string, ctx pongo2.Context) (string, error) {
	// Prepare a custom set.
	custom := pongo2.NewSet("render-template", pongo2.DefaultLoader)

	// Block the use of some tags.
	for _, tag := range []string{"extends", "import", "include", "ssi"} {
		err := custom.BanTag(tag)
		if err != nil {
			return "", fmt.Errorf("Failed to configure custom pongo2 parser: Failed to block tag tag %q: %w", tag, err)
		}
	}

	// Limit recursion to 3 levels.
	for range 3 {
		// Load template from string
		tpl, err := custom.FromString("{% autoescape off %}" + template + "{% endautoescape %}")
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
