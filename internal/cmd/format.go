package cmd

import (
	"strings"
)

// FormatSection properly indents a text section.
func FormatSection(header string, content string) string {
	out := ""

	// Add section header
	if header != "" {
		out += header + ":\n"
	}

	// Indent the content
	for _, line := range strings.Split(content, "\n") {
		if line != "" {
			out += "  "
		}

		out += line + "\n"
	}

	if header != "" {
		// Section separator (when rendering a full section
		out += "\n"
	} else {
		// Remove last newline when rendering partial section
		out = strings.TrimSuffix(out, "\n")
	}

	return out
}
