// Package i18n provides message translation.
package i18n

import (
	"slices"
	"strings"
)

// localeAliases maps locales onto catalogs named differently, Chinese by script and Norwegian Bokmål by code.
var localeAliases = map[string]string{
	"zh_CN": "zh_Hans",
	"zh_SG": "zh_Hans",
	"zh_MY": "zh_Hans",
	"zh_TW": "zh_Hant",
	"zh_HK": "zh_Hant",
	"zh_MO": "zh_Hant",
	"no":    "nb",
	"no_NO": "nb_NO",
}

// catalogNames lists the catalogs to try for a locale, most specific first, aliases before the bare language.
func catalogNames(locale string) []string {
	names := []string{}
	add := func(name string) {
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	add(locale)
	add(localeAliases[locale])

	language, _, _ := strings.Cut(locale, "_")
	add(language)
	add(localeAliases[language])

	return names
}

// detectLocale returns the configured locale without its encoding and modifier, or "" for none.
func detectLocale(getenv func(string) string) string {
	for _, name := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		value := getenv(name)
		if value == "" || value == "C" || value == "POSIX" {
			continue
		}

		// LANGUAGE holds a colon separated list of preferences.
		value, _, _ = strings.Cut(value, ":")
		value, _, _ = strings.Cut(value, ".")
		value, _, _ = strings.Cut(value, "@")
		if value != "" {
			return value
		}
	}

	return ""
}
