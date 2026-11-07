//go:build !linux || !cgo

package i18n

import (
	"sync"
	"testing"
)

// TestGTranslatesChinese is the regression test for the locale naming bug:
// a zh_CN locale used to fall back to English because no zh_CN.po exists, only
// zh_Hans.po.
func TestGTranslatesChinese(t *testing.T) {
	for _, lang := range []string{"zh_CN", "zh_SG", "zh_Hans"} {
		t.Setenv("LANGUAGE", "")
		t.Setenv("LC_ALL", "")
		t.Setenv("LC_MESSAGES", "")
		t.Setenv("LANG", lang+".UTF-8")

		// Reset the package cache so load() runs again.
		once = sync.Once{}
		entries = nil

		got := G("Description:")
		if got == "Description:" {
			t.Errorf("LANG=%s: still untranslated", lang)
		}
	}
}
