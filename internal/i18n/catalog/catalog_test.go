package catalog

import (
	"maps"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	translations := Parse(`# Translator comment
msgid ""
msgstr ""
"Project-Id-Version: incus\n"

msgid "single"
msgstr "un"

msgid "line1\n"
        "line2"
msgstr "ligne1\n"
        "ligne2"

#: cmd/incus/main.go:1
#, fuzzy, c-format
msgid "fuzzy %s"
msgstr "approximatif %s"

msgid "untranslated"
msgstr ""

msgid "say \"hi\" a\\b"
msgstr "dire \"salut\" a\\b"

#~ msgid "obsolete"
#~ msgstr "obsolète"
`)

	expected := map[string]string{
		"single":       "un",
		"line1\nline2": "ligne1\nligne2",
		`say "hi" a\b`: `dire "salut" a\b`,
	}

	if len(translations) != len(expected) {
		t.Fatalf("Unexpected translations: %v", translations)
	}

	for msgid, msgstr := range expected {
		if translations[msgid] != msgstr {
			t.Errorf("%q: got %q, want %q", msgid, translations[msgid], msgstr)
		}
	}
}

func TestWriteRoundTrip(t *testing.T) {
	translations := map[string]string{
		"single":        "un",
		"line1\nline2":  "ligne1\nligne2",
		`say "hi" a\b`:  `dire "salut" a\b`,
		"tab\tand\\n":   "tabulation\tet\\n",
		"percent %s %d": "pourcentage %s %d",
	}

	b := &strings.Builder{}
	err := Write(b, translations)
	if err != nil {
		t.Fatal(err)
	}

	parsed := Parse(b.String())
	if !maps.Equal(parsed, translations) {
		t.Fatalf("Round trip mismatch: got %v, want %v", parsed, translations)
	}
}
