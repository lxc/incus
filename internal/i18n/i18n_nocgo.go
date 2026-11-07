//go:build !linux || !cgo

package i18n

import (
	"os"
	"sync"

	"github.com/lxc/incus/v7/po"
)

// entries caches the msgid -> msgstr table for the active locale.
var (
	once         sync.Once
	detectedLang string
	entries      map[string]string
)

// G returns the translation of msgid for the active locale.
//
// The lookup matches gettext: the locale named by LANGUAGE, LC_ALL,
// LC_MESSAGES or LANG, then its short language code, then the msgid itself. A
// msgid missing from the catalogue, or present with an empty msgstr, yields the
// msgid, so a catalogue lagging behind the source degrades to an untranslated
// string rather than crashing the process.
func G(msgid string) string {
	once.Do(load)

	translated, ok := entries[msgid]
	if ok && translated != "" {
		return translated
	}

	return msgid
}

// load resolves the active locale and parses its catalogue.
func load() {
	detectedLang = detectLanguage(os.Getenv)
	if detectedLang == "" {
		return
	}

	for _, candidate := range []string{detectedLang, shortCode(detectedLang)} {
		if candidate == "" {
			continue
		}

		data, err := po.Files.ReadFile(candidate + ".po")
		if err != nil {
			continue
		}

		entries = parsePo(string(data))

		return
	}
}
