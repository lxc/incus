package ovn

import (
	"strconv"
	"strings"
)

// unquote passes s through strconv.Unquote if the first character is a ", otherwise returns s unmodified.
// This is useful as openvswitch's tools can sometimes return values double quoted if they start with a number.
func unquote(s string) (string, error) {
	if strings.HasPrefix(s, `"`) {
		return strconv.Unquote(s)
	}

	return s, nil
}
