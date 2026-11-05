package util

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/flosch/pongo2/v6"
)

// bannedTemplateTags and bannedTemplateFilters read files or produce output unrelated to the template's size.
var (
	bannedTemplateTags    = []string{"extends", "import", "include", "ssi", "lorem", "macro"}
	bannedTemplateFilters = []string{"center", "floatformat", "ljust", "rjust"}
)

// templateMaxOutput bounds the rendered output.
const templateMaxOutput = 16 * 1024 * 1024

// newTemplateSet returns a template set with the banned tags and filters blocked.
func newTemplateSet() (*pongo2.TemplateSet, error) {
	custom := pongo2.NewSet("render-template", pongo2.DefaultLoader)

	for _, tag := range bannedTemplateTags {
		err := custom.BanTag(tag)
		if err != nil {
			return nil, fmt.Errorf("Failed to configure custom pongo2 parser: Failed to block tag %q: %w", tag, err)
		}
	}

	for _, filter := range bannedTemplateFilters {
		err := custom.BanFilter(filter)
		if err != nil {
			return nil, fmt.Errorf("Failed to configure custom pongo2 parser: Failed to block filter %q: %w", filter, err)
		}
	}

	return custom, nil
}

// boundedWriter fails once more than templateMaxOutput bytes have been written.
type boundedWriter struct {
	w io.Writer
	n int
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	b.n += len(p)
	if b.n > templateMaxOutput {
		return 0, errors.New("Template output is too large")
	}

	return b.w.Write(p)
}

// executeTemplate renders a template to w, turning a renderer panic into an error.
func executeTemplate(tpl *pongo2.Template, ctx pongo2.Context, w io.Writer) (err error) {
	defer func() {
		r := recover()
		if r != nil {
			err = fmt.Errorf("Template rendering failed: %v", r)
		}
	}()

	return tpl.ExecuteWriterUnbuffered(ctx, &boundedWriter{w: w})
}

// RenderTemplate renders a pongo2 template with nesting support.
// This supports up to 3 levels of nesting (to avoid loops).
func RenderTemplate(template string, ctx pongo2.Context) (string, error) {
	custom, err := newTemplateSet()
	if err != nil {
		return "", err
	}

	// Limit recursion to 3 levels.
	for range 3 {
		// Load template from string
		tpl, err := custom.FromString("{% autoescape off %}" + template + "{% endautoescape %}")
		if err != nil {
			return "", err
		}

		// Get rendered template
		var sb strings.Builder

		err = executeTemplate(tpl, ctx, &sb)
		if err != nil {
			return sb.String(), err
		}

		ret := sb.String()

		// Check if another pass is needed.
		if !strings.Contains(ret, "{{") && !strings.Contains(ret, "{%") {
			return ret, nil
		}

		// Prepare for another run.
		template = ret
	}

	return "", errors.New("Maximum template recursion limit reached")
}

// RenderTemplateFile renders a pongo2 template to a file.
// No nesting is supported in this scenario.
func RenderTemplateFile(w io.Writer, template string, ctx pongo2.Context) error {
	custom, err := newTemplateSet()
	if err != nil {
		return err
	}

	// Load template from string
	tpl, err := custom.FromString("{% autoescape off %}" + template + "{% endautoescape %}")
	if err != nil {
		return err
	}

	// Get rendered template
	return executeTemplate(tpl, ctx, w)
}
