package lex

import (
	"fmt"
	"strings"
)

// KeyValue extracts the key and value encoded in the given string and
// separated by '=' (foo=bar -> foo, bar).
func KeyValue(s string) (string, string, error) {
	parts := strings.Split(s, "=")

	if len(parts) != 2 {
		return "", "", fmt.Errorf("The token %q is not a key/value pair", s)
	}

	return parts[0], parts[1], nil
}
