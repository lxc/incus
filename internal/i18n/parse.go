// Package i18n provides message translation.
//
// On Linux with cgo, translations come from GNU gettext, so distributions can
// keep shipping them in separate packages and update them independently of the
// binary. Everywhere else — which is how most distributions and Windows build
// incus — the catalogues in po/ are embedded and read directly, because a
// cgo-free build has no gettext to consult and previously had no translations
// at all.
//
// The helpers below are backend-independent: parsing, locale detection and the
// catalogue naming rules are the same whichever backend a build selects, and
// they are compiled and tested in every configuration.
package i18n

import (
	"bufio"
	"strings"
)

// shortCode reduces "zh_Hans" to "zh", or returns "" when there is no "_".
func shortCode(lang string) string {
	i := strings.IndexByte(lang, '_')
	if i > 0 {
		return lang[:i]
	}

	return ""
}

// detectLanguage returns the locale name to look up, or "" for none.
//
// LANGUAGE may hold a colon-separated list, and every variable may carry an
// encoding (.UTF-8) and a modifier (@euro), both stripped before the catalogue
// name is formed. C and POSIX mean "no translation".
func detectLanguage(getenv func(string) string) string {
	for _, name := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		val := getenv(name)
		if val == "" || val == "C" || val == "POSIX" {
			continue
		}

		i := strings.IndexByte(val, ':')
		if i >= 0 {
			val = val[:i]
		}

		i = strings.IndexByte(val, '.')
		if i >= 0 {
			val = val[:i]
		}

		i = strings.IndexByte(val, '@')
		if i >= 0 {
			val = val[:i]
		}

		if val != "" {
			return val
		}
	}

	return ""
}

// parsePo reads a catalogue into a msgid -> msgstr map.
//
// Entries with an empty msgstr are skipped: they are untranslated and G()
// already returns the msgid for them. Plural forms are not represented,
// because incus does not call ngettext.
func parsePo(data string) map[string]string {
	out := make(map[string]string)

	var msgid, msgstr string

	var inID, inStr bool

	flush := func() {
		if msgid != "" && msgstr != "" {
			out[msgid] = msgstr
		}

		msgid, msgstr = "", ""
		inID, inStr = false, false
	}

	scanner := bufio.NewScanner(strings.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "#"):
			// comment, reference or flag line
		case strings.HasPrefix(line, `msgid `):
			if msgid != "" {
				flush()
			}

			msgid = unquotePo(strings.TrimPrefix(line, `msgid `))
			inID, inStr = true, false
		case strings.HasPrefix(line, `msgstr `):
			msgstr = unquotePo(strings.TrimPrefix(line, `msgstr `))
			inID, inStr = false, true
		case inID:
			msgid += unquotePo(line)
		case inStr:
			msgstr += unquotePo(line)
		}
	}

	flush()

	return out
}

// unquotePo strips one pair of surrounding quotes and unescapes the result.
//
// Trimming exactly one leading and one trailing quote matters: a value ending
// in an escaped quote, like "say \"hi\"", would be mangled by strings.Trim.
func unquotePo(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}

	return unescapePo(s)
}

// unescapePo decodes the C-style escapes used in .po strings.
//
// Backslash must be decoded last, otherwise "\\n" would turn into a newline
// instead of a literal backslash followed by the letter n.
func unescapePo(s string) string {
	s = strings.ReplaceAll(s, `\\`, "\x00")
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\n`, "\n")

	return strings.ReplaceAll(s, "\x00", `\`)
}
