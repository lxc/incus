//go:build !linux || !cgo

package i18n

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"sync"

	"github.com/lxc/incus/v7/internal/i18n/catalog"
	"github.com/lxc/incus/v7/po"
)

var (
	translationsOnce sync.Once
	translations     map[string]string
)

// G returns the translated string.
func G(msgid string) string {
	translationsOnce.Do(loadTranslations)

	translated, ok := translations[msgid]
	if ok {
		return translated
	}

	return msgid
}

// loadTranslations parses the embedded catalog matching the configured locale.
func loadTranslations() {
	locale := detectLocale(os.Getenv)
	if locale == "" {
		return
	}

	for _, name := range catalogNames(locale) {
		data, err := readCatalog(name)
		if err != nil {
			continue
		}

		translations = catalog.Parse(data)

		return
	}
}

// readCatalog decompresses the embedded catalog of the given name.
func readCatalog(name string) (string, error) {
	data, err := po.Files.ReadFile("catalogs/" + name + ".po.gz")
	if err != nil {
		return "", err
	}

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}

	content, err := io.ReadAll(zr)
	if err != nil {
		return "", err
	}

	return string(content), nil
}
