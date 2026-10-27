package filter

import (
	"strings"
)

// RegexpToLike converts a regular expression, as interpreted by DefaultParseRegexp, into an SQL LIKE
// pattern using backslash as the escape character. Only literal characters, ".", ".*" and the "^" and
// "$" anchors are supported, false is returned for anything else.
func RegexpToLike(value string) (string, bool) {
	if strings.Contains(value, ",") {
		return "", false
	}

	pattern := value
	if !strings.Contains(pattern, "^") && !strings.Contains(pattern, "$") {
		pattern = "^" + pattern + "$"
	}

	prefix := "%"
	if strings.HasPrefix(pattern, "^") {
		prefix = ""
		pattern = pattern[1:]
	}

	suffix := "%"
	if strings.HasSuffix(pattern, "$") {
		suffix = ""
		pattern = pattern[:len(pattern)-1]
	}

	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]

		switch {
		case c == '.' && i+1 < len(pattern) && pattern[i+1] == '*':
			b.WriteString("%")
			i++
		case c == '.':
			b.WriteString("_")
		case c == '%' || c == '_':
			b.WriteByte('\\')
			b.WriteByte(c)
		case strings.IndexByte(`^$*+?()[]{}|\`, c) >= 0:
			return "", false
		default:
			b.WriteByte(c)
		}
	}

	return prefix + b.String() + suffix, true
}
