package i18n

import (
	"slices"
	"testing"
)

func TestDetectLocale(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"LANG": "zh_CN.UTF-8"}, "zh_CN"},
		{map[string]string{"LANG": "zh_CN.UTF-8", "LC_ALL": "fr_FR.UTF-8"}, "fr_FR"},
		{map[string]string{"LANGUAGE": "zh_Hans:en", "LANG": "fr"}, "zh_Hans"},
		{map[string]string{"LANG": "fr.UTF-8@euro"}, "fr"},
		{map[string]string{"LANG": "C"}, ""},
		{map[string]string{"LANG": "POSIX"}, ""},
		{map[string]string{}, ""},
	}

	for _, c := range cases {
		got := detectLocale(func(name string) string { return c.env[name] })
		if got != c.want {
			t.Errorf("env=%v: got %q, want %q", c.env, got, c.want)
		}
	}
}

func TestCatalogNames(t *testing.T) {
	cases := map[string][]string{
		"zh_CN":   {"zh_CN", "zh_Hans", "zh"},
		"zh_TW":   {"zh_TW", "zh_Hant", "zh"},
		"zh_Hans": {"zh_Hans", "zh"},
		"no_NO":   {"no_NO", "nb_NO", "no", "nb"},
		"nn_NO":   {"nn_NO", "nn"},
		"fr_FR":   {"fr_FR", "fr"},
		"fr":      {"fr"},
	}

	for locale, want := range cases {
		got := catalogNames(locale)
		if !slices.Equal(got, want) {
			t.Errorf("%q: got %v, want %v", locale, got, want)
		}
	}
}
