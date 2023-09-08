//go:build linux && cgo

package i18n

import (
	"github.com/gosexy/gettext"
)

// G returns the translated string.
func G(msgid string) string {
	return gettext.DGettext("incus", msgid)
}

func init() {
	gettext.SetLocale(gettext.LC_ALL, "")
}
