//go:build !linux || !cgo

package i18n

import (
	"sync"
	"testing"
)

func TestLoadTranslations(t *testing.T) {
	_, err := readCatalog("fr")
	if err != nil {
		t.Skip("No embedded catalogs, run generate-catalogs first")
	}

	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "fr_FR.UTF-8")

	translationsOnce = sync.Once{}
	translations = nil

	G("")
	if len(translations) == 0 {
		t.Fatal("No translation loaded for fr_FR")
	}
}
