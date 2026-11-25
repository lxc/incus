// Package catalog reads and writes gettext translation catalogs.
package catalog

import (
	"bufio"
	"slices"
	"strings"
)

// Parse reads a catalog into a msgid to msgstr map, skipping fuzzy and untranslated entries like msgfmt does.
func Parse(data string) map[string]string {
	translations := map[string]string{}

	var msgid, msgstr string

	var fuzzy, inMsgid, inMsgstr bool

	flush := func() {
		if msgid != "" && msgstr != "" && !fuzzy {
			translations[msgid] = msgstr
		}

		msgid, msgstr = "", ""
		fuzzy, inMsgid, inMsgstr = false, false, false
	}

	scanner := bufio.NewScanner(strings.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "#,"):
			fuzzy = fuzzy || slices.Contains(strings.Fields(strings.ReplaceAll(line[2:], ",", " ")), "fuzzy")
		case strings.HasPrefix(line, "#"):
			// Translator comments, references and obsolete entries.
		case strings.HasPrefix(line, "msgid "):
			msgid = unquote(strings.TrimPrefix(line, "msgid "))
			inMsgid, inMsgstr = true, false
		case strings.HasPrefix(line, "msgstr "):
			msgstr = unquote(strings.TrimPrefix(line, "msgstr "))
			inMsgid, inMsgstr = false, true
		case strings.HasPrefix(line, "msgid_plural ") || strings.HasPrefix(line, "msgstr["):
			// Plural forms aren't used by the client.
			inMsgid, inMsgstr = false, false
		case inMsgid:
			msgid += unquote(line)
		case inMsgstr:
			msgstr += unquote(line)
		}
	}

	flush()

	return translations
}

// unquote strips the surrounding quotes of a catalog string and decodes its escapes.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}

	// Backslashes are decoded last so that an escaped backslash followed by n stays literal.
	s = strings.ReplaceAll(s, `\\`, "\x00")
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\t`, "\t")

	return strings.ReplaceAll(s, "\x00", `\`)
}
