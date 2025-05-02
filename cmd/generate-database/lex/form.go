package lex

import (
	"strings"
)

// Plural converts to plural form ("foo" -> "foos").
func Plural(s string) string {
	// TODO: smarter algorithm? :)

	if strings.HasSuffix(strings.ToLower(s), "config") {
		return s
	}

	if strings.HasSuffix(s, "ch") || strings.HasSuffix(s, "sh") || strings.HasSuffix(s, "ss") {
		return s + "es"
	}

	if strings.HasSuffix(s, "y") {
		return s[:len(s)-1] + "ies"
	}

	if s[len(s)-1] != 's' {
		return s + "s"
	}

	return s
}

// Singular converts to singular form ("foos" -> "foo").
func Singular(s string) string {
	// TODO: smarter algorithm? :)
	before, ok := strings.CutSuffix(s, "ies")
	if ok {
		return before + "y"
	}

	before, ok = strings.CutSuffix(s, "es")
	if ok && (strings.HasSuffix(before, "ch") || strings.HasSuffix(before, "sh") || strings.HasSuffix(before, "ss")) {
		return before
	}

	if s[len(s)-1] == 's' {
		return s[:len(s)-1]
	}

	return s
}
