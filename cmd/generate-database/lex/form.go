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

	if s[len(s)-1] != 's' {
		return s + "s"
	}

	return s
}

// Singular converts to singular form ("foos" -> "foo").
func Singular(s string) string {
	// TODO: smarter algorithm? :)
	if s[len(s)-1] == 's' {
		return s[:len(s)-1]
	}

	return s
}
