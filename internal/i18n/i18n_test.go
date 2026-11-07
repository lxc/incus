package i18n

import (
	"testing"
)

func TestUnquotePo(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"hello"`, "hello"},
		{`"say \"hi\""`, `say "hi"`},
		{`"a\\b"`, `a\b`},
		{`"line1\nline2"`, "line1\nline2"},
		{`"trailing\""`, `trailing"`},
	}

	for _, c := range cases {
		got := unquotePo(c.in)
		if got != c.want {
			t.Errorf("unquotePo(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParsePoMultiline(t *testing.T) {
	src := `# comment
msgid ""
msgstr ""
"Project-Id-Version: incus\n"

msgid "single"
msgstr "un"

msgid "line1\n"
        "line2"
msgstr "ligne1\n"
        "ligne2"
`
	table := parsePo(src)

	if table["single"] != "un" {
		t.Errorf("single = %q", table["single"])
	}

	if table["line1\nline2"] != "ligne1\nligne2" {
		t.Errorf("multiline = %q", table["line1\nline2"])
	}

	_, ok := table[""]
	if ok {
		t.Errorf("header must not be in the table")
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"LANG": "zh_CN.UTF-8"}, "zh_CN"},
		{map[string]string{"LANG": "zh_CN.UTF-8", "LC_ALL": "fr_FR.UTF-8"}, "fr_FR"},
		{map[string]string{"LANGUAGE": "zh_Hans:en", "LANG": "fr"}, "zh_Hans"},
		{map[string]string{"LANG": "C"}, ""},
		{map[string]string{"LANG": "POSIX"}, ""},
		{map[string]string{}, ""},
		{map[string]string{"LANG": "fr.UTF-8@euro"}, "fr"},
	}

	for _, c := range cases {
		getenv := func(name string) string { return c.env[name] }

		got := detectLanguage(getenv)
		if got != c.want {
			t.Errorf("env=%v: got %q, want %q", c.env, got, c.want)
		}
	}
}

// TestCatalogueNames covers the alias chain, especially the Chinese
// region-to-script mapping that the region-named catalogues require.
func TestCatalogueNames(t *testing.T) {
	cases := []struct {
		lang string
		want []string
	}{
		{"zh_CN", []string{"zh_CN", "zh_Hans", "zh"}},
		{"zh_TW", []string{"zh_TW", "zh_Hant", "zh"}},
		{"zh_Hans", []string{"zh_Hans", "zh"}},
		{"no_NO", []string{"no_NO", "nb_NO", "no", "nb"}},
		{"nn_NO", []string{"nn_NO", "nn"}},
		{"pt_BR", []string{"pt_BR", "pt"}},
		{"fr_FR", []string{"fr_FR", "fr"}},
	}

	for _, c := range cases {
		got := catalogueNames(c.lang)
		if len(got) != len(c.want) {
			t.Errorf("catalogueNames(%q) = %v, want %v", c.lang, got, c.want)

			continue
		}

		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("catalogueNames(%q) = %v, want %v", c.lang, got, c.want)

				break
			}
		}
	}
}
