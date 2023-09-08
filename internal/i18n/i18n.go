//go:build !linux || !cgo

package i18n

// G returns the translated string
func G(msgid string) string {
	return msgid
}
